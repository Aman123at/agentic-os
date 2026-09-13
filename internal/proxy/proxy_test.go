package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// service starts a fake Service on 127.0.0.1 that echoes the request it received.
func service(t *testing.T) (port string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Set-Cookie", "app=1; Path=/")
		w.Header().Add("Set-Cookie", SessionCookie+"=stolen; Path=/")
		// Browsers trim the name, so these would also replace the session cookie.
		w.Header().Add("Set-Cookie", SessionCookie+" =stolen; Path=/")
		w.Header().Add("Set-Cookie", "="+SessionCookie+"=stolen; Path=/")
		// A subdomain Service must not toss cookies onto the Desktop's localhost.
		w.Header().Add("Set-Cookie", "tossed=1; Domain=localhost; Path=/")
		fmt.Fprintf(w, "path=%s query=%s cookies=%s", r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Cookie"))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Port()
}

// desktop stands in for everything aosd serves itself.
var desktop = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "desktop")
})

func do(t *testing.T, host, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	req.Header.Set("Cookie", SessionCookie+"=secret; app=1")
	rec := httptest.NewRecorder()
	New(desktop, 7700).ServeHTTP(rec, req)
	return rec
}

func TestSubdomainForwardsToServicePort(t *testing.T) {
	port := service(t)

	rec := do(t, port+".localhost:7700", "/api/items?x=1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got, want := rec.Body.String(), "path=/api/items query=x=1 cookies=app=1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestPathModeStripsPrefixAndSandboxesThePage(t *testing.T) {
	port := service(t)

	rec := do(t, "localhost:7700", "/port/"+port+"/app/index.html?x=1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got, want := rec.Body.String(), "path=/app/index.html query=x=1 cookies=app=1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox ") || strings.Contains(csp, "allow-same-origin") {
		t.Errorf("CSP = %q, want a sandbox without allow-same-origin", csp)
	}
}

func TestServicesCannotSetTheDesktopSessionCookie(t *testing.T) {
	port := service(t)

	for _, rec := range []*httptest.ResponseRecorder{
		do(t, "localhost:7700", "/port/"+port+"/"),
		do(t, port+".localhost:7700", "/"),
	} {
		if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != "app=1; Path=/" {
			t.Errorf("Set-Cookie reaching the browser = %q, want only the Service's own app cookie", got)
		}
	}
}

func TestPathModeKeepsEscapedPaths(t *testing.T) {
	port := service(t)

	rec := do(t, "localhost:7700", "/port/"+port+"/files/a%2Fb%20c")

	if got, want := rec.Body.String(), "path=/files/a%2Fb%20c query= cookies=app=1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestPathModeWithoutTrailingSlashRedirects(t *testing.T) {
	rec := do(t, "localhost:7700", "/port/8000?x=1")

	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/port/8000/?x=1" {
		t.Errorf("got %d Location=%q, want 308 to /port/8000/?x=1", rec.Code, rec.Header().Get("Location"))
	}
}

func TestOtherRequestsReachTheDesktop(t *testing.T) {
	for _, host := range []string{"localhost:7700", "127.0.0.1:7700", "[::1]:7700", "localhost"} {
		if rec := do(t, host, "/"); rec.Body.String() != "desktop" {
			t.Errorf("Host %s: got %d %q, want the desktop", host, rec.Code, rec.Body)
		}
	}
}

func TestUnknownHostsAreRejected(t *testing.T) {
	// DNS rebinding: a website's own domain resolving to 127.0.0.1 (ADR-0007).
	for _, host := range []string{"evil.example:7700", "localhost.evil.example", "8000.localhost.evil.example", "192.168.1.5:7700"} {
		if rec := do(t, host, "/"); rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("Host %s: got %d, want 421", host, rec.Code)
		}
	}
}

func TestInvalidPortsAreRejected(t *testing.T) {
	for _, tc := range []struct{ host, target string }{
		{"0.localhost:7700", "/"},
		{"65536.localhost:7700", "/"},
		{"08000.localhost:7700", "/"},
		{"localhost:7700", "/port/abc/"},
		{"localhost:7700", "/port/99999/"},
		// aosd's own port would forward to itself forever.
		{"7700.localhost:7700", "/"},
		{"localhost:7700", "/port/7700/"},
	} {
		if rec := do(t, tc.host, tc.target); rec.Code != http.StatusBadRequest {
			t.Errorf("%s%s: got %d, want 400", tc.host, tc.target, rec.Code)
		}
	}
}
