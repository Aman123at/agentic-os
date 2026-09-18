package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aman123at/agentic-os/internal/auth"
	"github.com/Aman123at/agentic-os/internal/store"
)

const (
	testUser     = "aman"
	testPassword = "a-strong-password"
)

// The whole api package's tests share one signing key and clock, so testToken —
// a valid access token minted once — verifies against every Auth newAuth hands
// out (VerifyAccess checks only the signature and expiry, not the database).
var (
	testKey   = []byte("a-fixed-api-test-signing-key")
	testClock = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	testToken = mintTestToken()
)

func mintTestToken() string {
	dir, err := os.MkdirTemp("", "aos-api-auth-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	m := &auth.Model{DB: db, Key: testKey, Now: func() time.Time { return testClock }}
	if err := m.SetPassword(context.Background(), testUser, testPassword); err != nil {
		panic(err)
	}
	access, _, err := m.SignIn(context.Background(), testUser, testPassword)
	if err != nil {
		panic(err)
	}
	return access
}

// whoami echoes the authenticated actor.
var whoami = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, ActorFrom(r.Context()))
})

// newAuth returns an Auth backed by a fresh database with the one user set, and
// the pointer to its clock so a test can move time forward.
func newAuth(t *testing.T) (*Auth, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := testClock
	m := &auth.Model{DB: db, Key: testKey, Now: func() time.Time { return clock }}
	if err := m.SetPassword(context.Background(), testUser, testPassword); err != nil {
		t.Fatal(err)
	}
	return &Auth{Model: m, SelfPort: 7700}, &clock
}

func do(t *testing.T, h http.Handler, method, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader("{}"))
	req.Host = "localhost:7700"
	for k, v := range header {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTheAPIRequiresAnAccessToken(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	for name, header := range map[string]map[string]string{
		"no credentials": {"Origin": "http://localhost:7700"},
		"wrong token":    {"Authorization": "Bearer nope", "Origin": "http://localhost:7700"},
	} {
		if rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks", header); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
	rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks", map[string]string{"Authorization": "Bearer " + testToken, "Origin": "http://localhost:7700"})
	if rec.Code != http.StatusOK || rec.Body.String() != "user:desktop" {
		t.Errorf("with the token: %d %q", rec.Code, rec.Body.String())
	}
}

func TestSignInAndRefreshAreOpenButSameOrigin(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	for _, path := range []string{"/aos.v1.AuthService/SignIn", "/aos.v1.AuthService/Refresh"} {
		if rec := do(t, h, "POST", path, map[string]string{"Origin": "http://localhost:7700"}); rec.Code != http.StatusOK {
			t.Errorf("%s same-origin: %d, want 200", path, rec.Code)
		}
		if rec := do(t, h, "POST", path, nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s without an Origin: %d, want 403", path, rec.Code)
		}
		if rec := do(t, h, "POST", path, map[string]string{"Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
			t.Errorf("%s cross-origin: %d, want 403", path, rec.Code)
		}
	}
}

func TestATicketAuthorisesOneBrowserLoad(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	m := a.Model

	// An <img>/<video>/download reads /files/raw with a ticket and no Origin.
	tkt := m.Ticket()
	if rec := do(t, h, "GET", "/files/raw?path=~/a.mp4&ticket="+tkt, nil); rec.Code != http.StatusOK || rec.Body.String() != "user:desktop" {
		t.Errorf("a ticketed media load: %d %q", rec.Code, rec.Body.String())
	}
	// The ticket is single-use.
	if rec := do(t, h, "GET", "/files/raw?path=~/a.mp4&ticket="+tkt, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("a reused ticket: %d, want 401", rec.Code)
	}
	// A WebSocket upgrade rides a ticket in the query too.
	ws := m.Ticket()
	if rec := do(t, h, "GET", "/ws/session/t_1?ticket="+ws, map[string]string{"Origin": "http://localhost:7700", "Upgrade": "websocket", "Connection": "Upgrade"}); rec.Code != http.StatusOK {
		t.Errorf("a ticketed WebSocket: %d, want 200", rec.Code)
	}
	// Without a ticket or a token, a browser load is refused.
	if rec := do(t, h, "GET", "/files/raw?path=~/a.mp4", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unticketed media load: %d, want 401", rec.Code)
	}
	// A ticket does not authorise an RPC — only the token does.
	if rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks?ticket="+m.Ticket(), map[string]string{"Origin": "http://localhost:7700"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a ticket on an RPC: %d, want 401", rec.Code)
	}
}

func TestCreatingATicketNeedsTheToken(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	create := "/aos.v1.AuthService/CreateTicket"
	if rec := do(t, h, "POST", create, map[string]string{"Origin": "http://localhost:7700"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("CreateTicket without the token: %d, want 401", rec.Code)
	}
	if rec := do(t, h, "POST", create, map[string]string{"Authorization": "Bearer " + testToken, "Origin": "http://localhost:7700"}); rec.Code != http.StatusOK {
		t.Errorf("CreateTicket with the token: %d, want 200", rec.Code)
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	rpc := "/aos.v1.TaskService/CreateTask"
	token := "Bearer " + testToken

	for _, tc := range []struct {
		name   string
		method string
		path   string
		header map[string]string
		want   int
	}{
		// The Host is no longer checked (M6.4): a token in a header is safe from DNS
		// rebinding, so an arbitrary Host with the token and no cross-origin header
		// is served. The Origin check below is what a browser attack runs into.
		{"an arbitrary Host with the token, no Origin", "POST", rpc, map[string]string{"Host": "evil.example:7700", "Authorization": token}, http.StatusOK},
		{"127.0.0.1 with the token", "POST", rpc, map[string]string{"Host": "127.0.0.1:7700", "Authorization": token}, http.StatusOK},
		{"same-origin RPC with the token", "POST", rpc, map[string]string{"Authorization": token, "Origin": "http://localhost:7700"}, http.StatusOK},
		{"a browser with the token from another origin", "POST", rpc, map[string]string{"Authorization": token, "Origin": "https://evil.example"}, http.StatusForbidden},
		{"a Service page on another port", "POST", rpc, map[string]string{"Authorization": token, "Origin": "http://localhost:3000"}, http.StatusForbidden},
		{"a sandboxed page (opaque origin)", "POST", rpc, map[string]string{"Authorization": token, "Origin": "null"}, http.StatusForbidden},
		{"cross-site WebSocket upgrade", "GET", "/ws/session/t_1", map[string]string{"Authorization": token, "Origin": "https://evil.example", "Upgrade": "websocket", "Connection": "Upgrade"}, http.StatusForbidden},
		{"sign-in needs no credentials", "POST", "/aos.v1.AuthService/SignIn", map[string]string{"Origin": "http://localhost:7700"}, http.StatusOK},
		{"cross-site sign-in", "POST", "/aos.v1.AuthService/SignIn", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"the Desktop page loads without credentials", "GET", "/", nil, http.StatusOK},
		{"health check", "GET", "/healthz", nil, http.StatusOK},
	} {
		if rec := do(t, h, tc.method, tc.path, tc.header); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, strings.TrimSpace(rec.Body.String()))
		}
	}
}

func TestForwardedServiceNeedsThePortCookie(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	m := a.Model

	// A public bind reaches /port/<n>/ directly, so without the cookie or a ticket
	// the forwarder refuses it — nothing Agent-started is open (M6.4).
	if rec := do(t, h, "GET", "/port/8000/index.html", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated service load: %d, want 401", rec.Code)
	}
	// The Desktop opens the Service with a single-use ticket, which is exchanged
	// for a path-scoped cookie and redirected away so the ticket does not linger.
	rec := do(t, h, "GET", "/port/8000/index.html?ticket="+m.Ticket(), nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ticket exchange: %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/port/8000/index.html" {
		t.Errorf("redirect Location %q, want the URL without the ticket", loc)
	}
	var grant *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "aos_port_8000" {
			grant = c
		}
	}
	if grant == nil {
		t.Fatal("the exchange set no aos_port_8000 cookie")
	}
	if grant.Path != "/port/8000/" || !grant.HttpOnly {
		t.Errorf("cookie Path=%q HttpOnly=%v, want /port/8000/ and HttpOnly", grant.Path, grant.HttpOnly)
	}
	// The grant authorises the Service's loads, even from the sandbox's opaque
	// origin (Origin: null), which the Desktop's Origin check would reject.
	ok := map[string]string{"Cookie": grant.Name + "=" + grant.Value, "Origin": "null"}
	if rec := do(t, h, "GET", "/port/8000/index.html", ok); rec.Code != http.StatusOK {
		t.Errorf("service load with the grant: %d, want 200", rec.Code)
	}
	// A grant does not open a different Service, and a forged one is refused.
	if rec := do(t, h, "GET", "/port/9000/index.html", ok); rec.Code != http.StatusUnauthorized {
		t.Errorf("grant reused on another port: %d, want 401", rec.Code)
	}
	if rec := do(t, h, "GET", "/port/8000/index.html", map[string]string{"Cookie": "aos_port_8000=forged"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged grant: %d, want 401", rec.Code)
	}
}

func TestTheLocalhostServiceFormAlsoNeedsATicket(t *testing.T) {
	a, _ := newAuth(t)
	h := a.TCP(whoami)
	m := a.Model

	// <n>.localhost is a local convenience, but a forged Host header could reach it
	// over a public bind, so it is gated the same way — a cookie scoped to its host.
	if rec := do(t, h, "GET", "/", map[string]string{"Host": "8000.localhost:7700"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated .localhost load: %d, want 401", rec.Code)
	}
	rec := do(t, h, "GET", "/?ticket="+m.Ticket(), map[string]string{"Host": "8000.localhost:7700"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("ticket exchange: %d, want 303", rec.Code)
	}
	var grant *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "aos_port_8000" {
			grant = c
		}
	}
	if grant == nil || grant.Path != "/" {
		t.Fatalf("cookie for the .localhost form = %+v, want Path=/", grant)
	}
}
