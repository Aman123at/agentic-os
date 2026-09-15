package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "tok-test-dummy-0123456789abcdef"

// whoami echoes the authenticated actor.
var whoami = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, ActorFrom(r.Context()))
})

func newAuth(t *testing.T) (*Auth, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	return &Auth{Token: testToken, Now: func() time.Time { return clock }}, &clock
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

func TestTheAPIRequiresTheAccessToken(t *testing.T) {
	auth, _ := newAuth(t)
	h := auth.TCP(whoami)
	for name, header := range map[string]map[string]string{
		"no credentials":  nil,
		"wrong token":     {"Authorization": "Bearer nope"},
		"token as cookie": {"Cookie": "aos_session=" + testToken},
	} {
		if rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks", header); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
	rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks", map[string]string{"Authorization": "Bearer " + testToken})
	if rec.Code != http.StatusOK || rec.Body.String() != "user:token" {
		t.Errorf("with the token: %d %q", rec.Code, rec.Body.String())
	}
}

func TestAOneTimeCodeBecomesAStrictHttpOnlySessionCookie(t *testing.T) {
	auth, clock := newAuth(t)
	h := auth.TCP(whoami)

	code, expires := auth.NewLoginCode()
	if len(code) < 20 || !expires.Equal(clock.Add(5*time.Minute)) {
		t.Fatalf("code %q expires %v", code, expires)
	}
	cookie, err := auth.Exchange(code)
	if err != nil {
		t.Fatal(err)
	}
	if cookie.Name != SessionCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || strings.Contains(cookie.Value, testToken) {
		t.Errorf("cookie %+v", cookie)
	}
	if _, err := auth.Exchange(code); err == nil {
		t.Error("a login code worked twice")
	}
	rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks", map[string]string{"Cookie": cookie.String(), "Origin": "http://localhost:7700"})
	if rec.Code != http.StatusOK || rec.Body.String() != "user:desktop" {
		t.Errorf("request with the cookie: %d %q", rec.Code, rec.Body.String())
	}

	stale, _ := auth.NewLoginCode()
	*clock = clock.Add(6 * time.Minute)
	if _, err := auth.Exchange(stale); err == nil {
		t.Error("an expired login code worked")
	}
	// A new access token signs every Desktop out.
	auth.Token = "tok-test-dummy-rotated-000000000"
	if rec := do(t, h, "POST", "/aos.v1.TaskService/ListTasks", map[string]string{"Cookie": cookie.String(), "Origin": "http://localhost:7700"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("cookie after rotating the token: %d", rec.Code)
	}
}

func TestOnlyTheDesktopsOwnPageReadsFilesWithoutAnOrigin(t *testing.T) {
	auth, _ := newAuth(t)
	h := auth.TCP(whoami)
	code, _ := auth.NewLoginCode()
	cookie, err := auth.Exchange(code)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		method, path, site string
		want               int
	}{
		"the Desktop's own <video>":  {"GET", "/files/raw?path=~/a.mp4", "same-origin", http.StatusOK},
		"a page on a forwarded port": {"GET", "/files/raw?path=~/a.mp4", "same-site", http.StatusForbidden},
		"another site":               {"GET", "/files/raw?path=~/a.mp4", "cross-site", http.StatusForbidden},
		"a typed-in address":         {"GET", "/files/raw?path=~/a.mp4", "none", http.StatusForbidden},
		"no Sec-Fetch-Site":          {"GET", "/files/raw?path=~/a.mp4", "", http.StatusForbidden},
		"an upload":                  {"POST", "/upload?path=~/a.txt", "same-origin", http.StatusForbidden},
		"an RPC":                     {"POST", "/aos.v1.TaskService/ListTasks", "same-origin", http.StatusForbidden},
	} {
		header := map[string]string{"Cookie": cookie.String()}
		if tc.site != "" {
			header["Sec-Fetch-Site"] = tc.site
		}
		if rec := do(t, h, tc.method, tc.path, header); rec.Code != tc.want {
			t.Errorf("%s without an Origin: %d, want %d", name, rec.Code, tc.want)
		}
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	auth, _ := newAuth(t)
	code, _ := auth.NewLoginCode()
	cookie, _ := auth.Exchange(code)
	h := auth.TCP(whoami)
	rpc := "/aos.v1.TaskService/CreateTask"
	token := "Bearer " + testToken

	for _, tc := range []struct {
		name   string
		method string
		path   string
		header map[string]string
		want   int
	}{
		{"DNS rebinding: another Host, even with the token", "POST", rpc, map[string]string{"Host": "evil.example:7700", "Authorization": token}, http.StatusMisdirectedRequest},
		{"127.0.0.1 is a local Host", "POST", rpc, map[string]string{"Host": "127.0.0.1:7700", "Authorization": token}, http.StatusOK},
		{"cross-site RPC with the cookie", "POST", rpc, map[string]string{"Cookie": cookie.String(), "Origin": "https://evil.example"}, http.StatusForbidden},
		{"same-origin RPC with the cookie", "POST", rpc, map[string]string{"Cookie": cookie.String(), "Origin": "http://localhost:7700"}, http.StatusOK},
		{"a Service page on another port", "POST", rpc, map[string]string{"Cookie": cookie.String(), "Origin": "http://localhost:3000"}, http.StatusForbidden},
		{"a sandboxed page (opaque origin)", "POST", rpc, map[string]string{"Cookie": cookie.String(), "Origin": "null"}, http.StatusForbidden},
		{"cookie without an Origin", "POST", rpc, map[string]string{"Cookie": cookie.String()}, http.StatusForbidden},
		{"cross-site WebSocket upgrade", "GET", "/ws/session/t_1", map[string]string{"Cookie": cookie.String(), "Origin": "https://evil.example", "Upgrade": "websocket", "Connection": "Upgrade"}, http.StatusForbidden},
		{"a browser with a Bearer token from another origin", "POST", rpc, map[string]string{"Authorization": token, "Origin": "https://evil.example"}, http.StatusForbidden},
		{"sign-in needs no credentials", "POST", "/aos.v1.AuthService/ExchangeLoginCode", map[string]string{"Origin": "http://localhost:7700"}, http.StatusOK},
		{"cross-site sign-in", "POST", "/aos.v1.AuthService/ExchangeLoginCode", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"creating login codes is for the Unix socket only", "POST", "/aos.v1.AuthService/CreateLoginCode", map[string]string{"Authorization": token}, http.StatusForbidden},
		{"the Desktop page loads without credentials", "GET", "/", nil, http.StatusOK},
		{"health check", "GET", "/healthz", nil, http.StatusOK},
	} {
		if rec := do(t, h, tc.method, tc.path, tc.header); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, strings.TrimSpace(rec.Body.String()))
		}
	}
}
