// Package api serves the aosd API (PLAN.md §13): Connect-RPC services, the
// Session WebSocket and authentication (ADR-0007).
package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aman123at/agentic-os/internal/proxy"
)

// SessionCookie is the Desktop's sign-in cookie; Service forwarding never passes it on.
const SessionCookie = proxy.SessionCookie

const (
	loginCodeTTL  = 5 * time.Minute
	sessionMaxAge = 30 * 24 * time.Hour
)

// Auth authenticates API requests.
type Auth struct {
	// Token is the access token, generated on first start or AOS_ACCESS_TOKEN.
	Token string
	// SocketUID is the uid the control socket accepts — the user aosd runs as,
	// root on a native install (M6.2, ADR-0009). The zero value is root, so an
	// unset Auth is root-only; the Daemon sets it to its own uid explicitly.
	SocketUID int
	Now       func() time.Time

	mu    sync.Mutex
	codes map[string]time.Time
}

func (a *Auth) now() time.Time {
	if a.Now == nil {
		return time.Now()
	}
	return a.Now()
}

// NewLoginCode creates a one-time sign-in code (for `aos desktop-url`).
func (a *Auth) NewLoginCode() (string, time.Time) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	code := base64.RawURLEncoding.EncodeToString(b)
	expires := a.now().Add(loginCodeTTL)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.codes == nil {
		a.codes = map[string]time.Time{}
	}
	for c, exp := range a.codes {
		if a.now().After(exp) {
			delete(a.codes, c)
		}
	}
	a.codes[code] = expires
	return code, expires
}

// Exchange turns a login code into the session cookie; each code works once.
func (a *Auth) Exchange(code string) (*http.Cookie, error) {
	a.mu.Lock()
	expires, ok := a.codes[code]
	delete(a.codes, code)
	a.mu.Unlock()
	if !ok || a.now().After(expires) {
		return nil, errors.New("this sign-in link has expired or was already used; run `aos desktop-url` for a new one")
	}
	until := a.now().Add(sessionMaxAge).Unix()
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    "v1." + strconv.FormatInt(until, 10) + "." + a.sign(until),
		Path:     "/",
		MaxAge:   int(sessionMaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}, nil
}

// sign binds a session expiry to the access token, so rotating the token signs
// every Desktop out and nothing needs storing.
func (a *Auth) sign(until int64) string {
	mac := hmac.New(sha256.New, []byte(a.Token))
	mac.Write([]byte("aos-session|v1|" + strconv.FormatInt(until, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Auth) validSession(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "v1" || a.Token == "" {
		return false
	}
	until, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || a.now().Unix() > until {
		return false
	}
	return hmac.Equal([]byte(parts[2]), []byte(a.sign(until)))
}

type actorKey struct{}

// ActorFrom returns who made the request: "user:token", "user:desktop" or "user:cli".
func ActorFrom(ctx context.Context) string {
	actor, _ := ctx.Value(actorKey{}).(string)
	return actor
}

func withActor(r *http.Request, actor string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, actor))
}

// TCP wraps the handler served on port 7700 (PLAN.md §7.6):
//
//   - The Host must be local, which defeats DNS rebinding.
//   - A request with an Origin must come from the Desktop's own origin. Browsers
//     always send one on RPCs and WebSocket upgrades, so a cookie without an
//     Origin (other than a plain page load) is refused too.
//   - Everything except the Desktop's page, the health check and sign-in needs
//     the access token or the session cookie.
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
		api := strings.HasPrefix(r.URL.Path, "/aos.v1.") || strings.HasPrefix(r.URL.Path, "/ws/") ||
			strings.HasPrefix(r.URL.Path, "/files/") || r.URL.Path == "/upload"
		switch {
		case r.URL.Path == "/aos.v1.AuthService/CreateLoginCode":
			http.Error(w, "login codes are created with `aos desktop-url` inside the Machine", http.StatusForbidden)
			return
		case r.URL.Path == "/aos.v1.AuthService/ExchangeLoginCode":
			if origin == "" && r.Header.Get("Authorization") == "" {
				http.Error(w, "missing Origin", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		case !api && (r.Method == http.MethodGet || r.Method == http.MethodHead):
			next.ServeHTTP(w, r)
			return
		}
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && a.validToken(bearer) {
			next.ServeHTTP(w, withActor(r, "user:token"))
			return
		}
		if c, err := r.Cookie(SessionCookie); err == nil && a.validSession(c.Value) {
			if origin == "" && !mediaLoad(r) {
				http.Error(w, "missing Origin", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, withActor(r, "user:desktop"))
			return
		}
		http.Error(w, "sign in with the link from `aos desktop-url`", http.StatusUnauthorized)
	})
}

// mediaLoad reports the one request the Desktop's cookie may make without an
// Origin: an <img>, <video>, fetch or download reading /files/raw, for which
// browsers send none. Sec-Fetch-Site must then say the Desktop's own page made
// it. A page on a forwarded port (<port>.localhost) is the same site as the
// Desktop, so SameSite alone would let it in, but never the same origin.
func mediaLoad(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/files/raw" &&
		r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func (a *Auth) validToken(s string) bool {
	return a.Token != "" && subtle.ConstantTimeCompare([]byte(s), []byte(a.Token)) == 1
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
