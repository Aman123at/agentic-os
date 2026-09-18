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
		// A Service must not plant or shadow anything in AOS's own aos_ namespace.
		w.Header().Add("Set-Cookie", "aos_port_1=stolen; Path=/")
		w.Header().Add("Set-Cookie", "aos_session=stolen; Path=/")
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

// do sends a request through the proxy. It always carries a spent-looking port
// cookie for port and an app cookie, so the tests can watch the port cookie be
// stripped before it reaches the Service.
func do(t *testing.T, host, target string, port string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = host
	req.Header.Set("Cookie", PortCookie(atoiSafe(port))+"=grant; app=1")
	rec := httptest.NewRecorder()
	New(desktop, 7700).ServeHTTP(rec, req)
	return rec
}

func atoiSafe(s string) int {
	n, _ := parsePort(s)
	return n
}

func TestSubdomainForwardsToServicePort(t *testing.T) {
	port := service(t)

	rec := do(t, port+".localhost:7700", "/api/items?x=1", port)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got, want := rec.Body.String(), "path=/api/items query=x=1 cookies=app=1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestPathModeStripsPrefixAndSandboxesThePage(t *testing.T) {
	port := service(t)

	rec := do(t, "localhost:7700", "/port/"+port+"/app/index.html?x=1", port)

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

func TestServicesCannotSetAosCookies(t *testing.T) {
	port := service(t)

	for _, rec := range []*httptest.ResponseRecorder{
		do(t, "localhost:7700", "/port/"+port+"/", port),
		do(t, port+".localhost:7700", "/", port),
	} {
		if got := rec.Header().Values("Set-Cookie"); len(got) != 1 || got[0] != "app=1; Path=/" {
			t.Errorf("Set-Cookie reaching the browser = %q, want only the Service's own app cookie", got)
		}
	}
}

func TestPathModeKeepsEscapedPaths(t *testing.T) {
	port := service(t)

	rec := do(t, "localhost:7700", "/port/"+port+"/files/a%2Fb%20c", port)

	if got, want := rec.Body.String(), "path=/files/a%2Fb%20c query= cookies=app=1"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestPathModeWithoutTrailingSlashRedirects(t *testing.T) {
	rec := do(t, "localhost:7700", "/port/8000?x=1", "8000")

	if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/port/8000/?x=1" {
		t.Errorf("got %d Location=%q, want 308 to /port/8000/?x=1", rec.Code, rec.Header().Get("Location"))
	}
}

// TestOtherRequestsReachTheDesktop covers what the proxy no longer judges: with
// the Host allow-list gone (M6.4), any Host that is not a Service forward passes
// straight to the Desktop, where the authenticator's Origin check is the guard.
func TestOtherRequestsReachTheDesktop(t *testing.T) {
	for _, host := range []string{
		"localhost:7700", "127.0.0.1:7700", "[::1]:7700", "localhost",
		// Formerly rejected as unknown hosts; now the authenticator's job.
		"evil.example:7700", "localhost.evil.example", "8000.localhost.evil.example", "192.168.1.5:7700",
	} {
		if rec := do(t, host, "/", "8000"); rec.Body.String() != "desktop" {
			t.Errorf("Host %s: got %d %q, want the desktop", host, rec.Code, rec.Body)
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
		if rec := do(t, tc.host, tc.target, "8000"); rec.Code != http.StatusBadRequest {
			t.Errorf("%s%s: got %d, want 400", tc.host, tc.target, rec.Code)
		}
	}
}
