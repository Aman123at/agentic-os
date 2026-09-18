// Package api serves the aosd API (PLAN.md §13): Connect-RPC services, the
// Session WebSocket and authentication (ADR-0007).
package api

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/Aman123at/agentic-os/internal/auth"
)

// Auth authenticates API requests: over TCP with the auth Model's tokens and
// tickets (ADR-0007), and over the Unix socket by the caller's uid (M6.2).
type Auth struct {
	// Model verifies access tokens and redeems tickets. The socket-only wiring
	// (which authenticates by uid alone) may leave it nil.
	Model *auth.Model
	// SocketUID is the uid the control socket accepts — the user aosd runs as,
	// root on a native install (M6.2, ADR-0009). The zero value is root, so an
	// unset Auth is root-only; the Daemon sets it to its own uid explicitly.
	SocketUID int
}

type actorKey struct{}

// ActorFrom returns who made the request: "user:desktop" or "user:cli".
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	return actor
}

func withActor(r *http.Request, actor string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, actor))
}

// TCP wraps the handler served on port 7700 (PLAN.md §7.6):
//
//   - The Host must be local, which defeats DNS rebinding. (M6.4 removes this
//     check once the bind goes public; tokens travel in headers, so it gains
//     nothing then.)
//   - A request with an Origin must come from the Desktop's own origin. Browsers
//     always send one on RPCs and WebSocket upgrades, so this is the real CSRF
//     defence and it handles an arbitrary public host (ADR-0007).
//   - Sign-in and refresh are open; the Desktop's page, the health check and the
//     assets load without credentials; everything else needs a valid access
//     token in a header, or — for a browser load that cannot send one — a
//     single-use ticket in the query.
func (a *Auth) TCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
			http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/aos.v1.AuthService/SignIn" || r.URL.Path == "/aos.v1.AuthService/Refresh":
			// Open to anyone with the credentials, but still same-origin: a browser
			// always sends an Origin on an RPC, so a missing one is not the Desktop.
			if origin == "" {
				http.Error(w, "missing Origin", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		case !apiRoute(r) && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			next.ServeHTTP(w, r)
			return
		}
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && a.Model != nil && a.Model.VerifyAccess(bearer) {
			next.ServeHTTP(w, withActor(r, "user:desktop"))
			return
		}
		if ticketable(r) && a.Model != nil && a.Model.RedeemTicket(r.URL.Query().Get("ticket")) {
			next.ServeHTTP(w, withActor(r, "user:desktop"))
			return
		}
		http.Error(w, "sign in to use the Desktop", http.StatusUnauthorized)
	})
}

// apiRoute reports the routes that always require authentication: the RPCs, the
// WebSockets, the file byte streams and uploads.
func apiRoute(r *http.Request) bool {
	p := r.URL.Path
	return strings.HasPrefix(p, "/aos.v1.") || strings.HasPrefix(p, "/ws/") ||
		strings.HasPrefix(p, "/files/") || p == "/upload"
}

// ticketable reports the browser loads a single-use ticket may authorise: the
// WebSockets and the /files/raw reads (media, PDF ranges and the download).
// None of these can send an Authorization header, so removing cookies (ADR-0007)
// removed what used to authenticate them; the ticket, minted over an
// authenticated request and good for one load, takes its place.
func ticketable(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/ws/") || r.URL.Path == "/files/raw"
}

// localHost reports whether a Host header names this computer.
func localHost(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}
