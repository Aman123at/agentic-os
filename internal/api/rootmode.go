package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
	"github.com/Aman123at/agentic-os/internal/audit"
)

// The Root Mode switch's password lockout (M7.7): five wrong passwords within a
// window lock the switch for a spell. It is deliberately in memory — a restart
// clears it, but a successful switch restarts anyway, and each attempt already
// costs a pbkdf2 verify, so a restart loop buys an attacker nothing.
const (
	rootModeMaxAttempts = 5
	rootModeWindow      = 15 * time.Minute
	rootModeLockFor     = 15 * time.Minute
)

// realmSwitcher commits a Root Mode switch atomically with the Task queue (M7.7);
// *task.Manager implements it. It is a seam so the switch's password, lockout and
// audit logic can be tested without a live Agent behind the queue.
type realmSwitcher interface {
	SwitchRealm(ctx context.Context, commit func() error) ([]*aosv1.Task, error)
}

func (s *Server) switcher() realmSwitcher {
	if s.realmSwitch != nil {
		return s.realmSwitch
	}
	if s.Tasks != nil {
		return s.Tasks
	}
	return nil
}

// rootModeGate counts recent failed Root Mode password attempts and reports when
// the switch is locked. Safe for concurrent use.
type rootModeGate struct {
	now   func() time.Time
	mu    sync.Mutex
	fails []time.Time // failures still inside the window
	until time.Time   // locked until this time; zero when not locked
}

func (g *rootModeGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// locked reports whether the switch is currently locked and, if so, until when.
func (g *rootModeGate) locked() (bool, time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.until.IsZero() || !g.clock().Before(g.until) {
		return false, time.Time{}
	}
	return true, g.until
}

// fail records a wrong password and returns how many attempts remain. When the
// count reaches the limit it arms the lock (for the next try), but this try is
// still reported as a wrong password with zero left — the lock shows on the
// following attempt, so the fifth wrong answers and the sixth is locked out
// (M7.7). Attempts older than the window are forgotten first.
func (g *rootModeGate) fail() (attemptsLeft int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock()
	kept := g.fails[:0]
	for _, t := range g.fails {
		if now.Sub(t) < rootModeWindow {
			kept = append(kept, t)
		}
	}
	g.fails = append(kept, now)
	if len(g.fails) >= rootModeMaxAttempts {
		g.until = now.Add(rootModeLockFor)
	}
	return max(rootModeMaxAttempts-len(g.fails), 0)
}

// reset clears the count after a correct password.
func (g *rootModeGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails = nil
	g.until = time.Time{}
}

func (s *Server) rootModeGateOf() *rootModeGate {
	s.rootGateOnce.Do(func() { s.rootGate = &rootModeGate{now: s.Now} })
	return s.rootGate
}

// SetRootMode turns Root Mode on or off (M7.7). See services.proto for the full
// contract; the order here is deliberate: the no-active-Task gate (inside
// SwitchRealm, under the queue lock) runs before the password is checked, so a
// switch that cannot happen never spends a password attempt or an audit line.
func (sys systemService) SetRootMode(ctx context.Context, req *connect.Request[aosv1.SetRootModeRequest]) (*connect.Response[aosv1.SetRootModeResponse], error) {
	switcher := sys.s.switcher()
	if switcher == nil || sys.s.Settings == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("switching Root Mode is not available here"))
	}
	if sys.s.Restart == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("restart is not available, so Root Mode cannot be switched"))
	}
	// Nothing to do when already in the requested Realm: don't restart, don't ask
	// for a password.
	if sys.s.Info != nil && sys.s.Info().RootMode == req.Msg.Enabled {
		where := "Standard Mode"
		if req.Msg.Enabled {
			where = "Root Mode"
		}
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("already in %s", where))
	}

	actor := ActorFrom(ctx)
	// Over the control socket the caller has already proven root (§7.5), so no
	// password is asked (M7.11); turning Root Mode off lowers privilege and needs
	// none either.
	needPassword := req.Msg.Enabled && actor != "user:cli"

	active, err := switcher.SwitchRealm(ctx, func() error {
		if needPassword {
			if err := sys.checkRootPassword(ctx, req.Msg.Password); err != nil {
				return err
			}
		}
		if err := sys.s.Settings.SetRootMode(req.Msg.Enabled); err != nil {
			return err
		}
		result := "left Root Mode"
		if req.Msg.Enabled {
			result = "entered Root Mode"
		}
		// Audit in the Realm the switch is made from, before the restart writes the
		// first entry of the new Realm's log.
		_ = sys.s.Audit.Record(ctx, audit.Entry{Tool: "root_mode", Result: result, Decision: "allow", DecidedBy: actor, Actor: actor})
		return sys.s.Restart()
	})
	if err != nil {
		return nil, connectError(err)
	}
	if len(active) > 0 {
		return nil, rootModeBlockedError(active)
	}
	return connect.NewResponse(&aosv1.SetRootModeResponse{}), nil
}

// checkRootPassword enforces the lockout and verifies the password, returning a
// Connect error (with its detail) on lockout or a wrong password.
func (sys systemService) checkRootPassword(ctx context.Context, password string) error {
	if sys.s.Auth == nil || sys.s.Auth.Model == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("authentication is not available"))
	}
	gate := sys.s.rootModeGateOf()
	if locked, until := gate.locked(); locked {
		return rootModeLockoutError(until)
	}
	ok, err := sys.s.Auth.Model.VerifyPassword(ctx, password)
	if err != nil {
		return err
	}
	if !ok {
		return rootModeAttemptError(gate.fail())
	}
	gate.reset()
	return nil
}

func rootModeBlockedError(active []*aosv1.Task) error {
	detail := &aosv1.RootModeBlocked{}
	for _, t := range active {
		detail.Tasks = append(detail.Tasks, &aosv1.BlockingTask{Id: t.Id, Title: t.Title, State: t.State})
	}
	err := connect.NewError(connect.CodeFailedPrecondition,
		fmt.Errorf("an Agent is still working (%d Task(s)); let it finish or cancel it, then switch Root Mode", len(active)))
	if d, e := connect.NewErrorDetail(detail); e == nil {
		err.AddDetail(d)
	}
	return err
}

func rootModeAttemptError(attemptsLeft int) error {
	err := connect.NewError(connect.CodeUnauthenticated,
		fmt.Errorf("incorrect password — %d attempt(s) left", attemptsLeft))
	if d, e := connect.NewErrorDetail(&aosv1.RootModeAttempt{AttemptsLeft: int32(attemptsLeft)}); e == nil {
		err.AddDetail(d)
	}
	return err
}

func rootModeLockoutError(until time.Time) error {
	err := connect.NewError(connect.CodeResourceExhausted,
		fmt.Errorf("too many attempts — try again at %s", until.Format("15:04")))
	if d, e := connect.NewErrorDetail(&aosv1.RootModeLockout{Until: timestamppb.New(until)}); e == nil {
		err.AddDetail(d)
	}
	return err
}
