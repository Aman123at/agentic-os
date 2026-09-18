// Package proxy forwards Services (PLAN.md §12). It sits inside the authenticator
// (ADR-0007, M6.4): a request reaches it only once the middleware has authorised
// it, so the proxy no longer checks the Host header — the Origin check on the
// Desktop's routes and the per-port cookie on a forward do that work.
//
//   - http://<ip>:7700/port/<port>/… → 127.0.0.1:<port>/…, served in a CSP
//     sandbox so the page gets an opaque origin and cannot act as the Desktop.
//     This is the canonical, remote-reachable form.
//   - http://<port>.localhost:7700/… → 127.0.0.1:<port>/…, a local convenience
//     that resolves to loopback only on the machine running the browser.
package proxy

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// sandboxCSP gives path-forwarded pages an opaque origin: no allow-same-origin.
const sandboxCSP = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"

// PortCookie names the Path=/port/<n>/ cookie that authenticates a forwarded
// Service (ADR-0007, M6.4). The authenticator sets it when a ticket is redeemed;
// the proxy strips it here so the Service never sees it.
func PortCookie(port int) string { return "aos_port_" + strconv.Itoa(port) }

// Forwarded reports the Service port a request would be forwarded to and in
// which form — the canonical /port/<n>/ path, or the <n>.localhost local
// convenience — or ok=false when it is a request for aosd itself. selfPort
// (aosd's own port) is never a Service. The authenticator uses it to decide
// which requests need a per-port cookie instead of the Desktop's credentials.
func Forwarded(r *http.Request, selfPort int) (port int, pathForm, ok bool) {
	service := func(s string) (int, bool) {
		n, ok := parsePort(s)
		return n, ok && n != selfPort
	}
	if sub, isSub := strings.CutSuffix(hostname(r.Host), ".localhost"); isSub {
		if n, ok := service(sub); ok {
			return n, false, true
		}
		return 0, false, false
	}
	if rest, isPath := strings.CutPrefix(r.URL.EscapedPath(), "/port/"); isPath {
		seg, _, _ := strings.Cut(rest, "/")
		if n, ok := service(seg); ok {
			return n, true, true
		}
	}
	return 0, false, false
}

// New returns a handler that forwards Service requests and passes every other
// request to next. selfPort is aosd's own port inside the Machine, which is
// never forwarded. It runs after the authenticator, so it does not authenticate.
func New(next http.Handler, selfPort int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		port, pathForm, ok := Forwarded(r, selfPort)
		if !ok {
			// A /port/… path or <n>.localhost Host with a port that does not parse is
			// a client mistake, not a request for the Desktop.
			if badForward(r, selfPort) {
				http.Error(w, "invalid port", http.StatusBadRequest)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !pathForm {
			forward(w, r, port, r.URL.EscapedPath(), false)
			return
		}
		rest, _ := strings.CutPrefix(r.URL.EscapedPath(), "/port/")
		portText, subpath, hasSlash := strings.Cut(rest, "/")
		if !hasSlash {
			// Relative URLs in the Service's pages only resolve under a trailing slash.
			target := "/port/" + portText + "/"
			if r.URL.RawQuery != "" {
				target += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, target, http.StatusPermanentRedirect)
			return
		}
		forward(w, r, port, "/"+subpath, true)
	})
}

// badForward reports a forward-shaped request whose port does not parse: a
// <n>.localhost Host or a /port/<n>/ path with an invalid or self port. These
// are rejected rather than served as the Desktop.
func badForward(r *http.Request, selfPort int) bool {
	valid := func(s string) bool {
		n, ok := parsePort(s)
		return ok && n != selfPort
	}
	if sub, ok := strings.CutSuffix(hostname(r.Host), ".localhost"); ok {
		return !valid(sub)
	}
	if rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/port/"); ok {
		seg, _, _ := strings.Cut(rest, "/")
		return !valid(seg)
	}
	return false
}

// forward proxies r to the Service on port. escapedPath is the path to request,
// still percent-encoded so %2F and friends reach the Service unchanged.
func forward(w http.ResponseWriter, r *http.Request, port int, escapedPath string, sandbox bool) {
	path, err := url.PathUnescape(escapedPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = target
			pr.Out.URL.Path = path
			pr.Out.URL.RawPath = escapedPath
			pr.Out.Host = r.Host
			removeCookie(pr.Out.Header, PortCookie(port))
		},
		ModifyResponse: func(resp *http.Response) error {
			filterSetCookie(resp.Header)
			if sandbox {
				// Added alongside any policy of the Service's own; browsers enforce all of them.
				resp.Header.Add("Content-Security-Policy", sandboxCSP)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "nothing is listening on port "+strconv.Itoa(port), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// hostname returns the lower-cased Host header without its port.
func hostname(hostport string) string {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return strings.ToLower(strings.Trim(host, "[]"))
}

// parsePort accepts canonical decimal TCP ports 1–65535.
func parsePort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != s {
		return 0, false
	}
	return n, true
}

func removeCookie(h http.Header, name string) {
	var kept []string
	for _, line := range h.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part != "" && !strings.HasPrefix(part, name+"=") {
				kept = append(kept, part)
			}
		}
	}
	h.Del("Cookie")
	if len(kept) > 0 {
		h.Set("Cookie", strings.Join(kept, "; "))
	}
}

// filterSetCookie drops cookies a Service must never set: anything in AOS's own
// aos_ namespace (so a Service cannot plant or shadow a port grant), cookies
// with a Domain attribute (a subdomain Service could toss them onto localhost),
// and anything that does not parse.
func filterSetCookie(h http.Header) {
	values := h.Values("Set-Cookie")
	h.Del("Set-Cookie")
	for _, v := range values {
		name, _, _ := strings.Cut(v, "=")
		name = strings.TrimSpace(name)
		c, err := http.ParseSetCookie(v)
		if err != nil || name == "" || strings.HasPrefix(strings.ToLower(name), "aos_") || c.Domain != "" {
			continue
		}
		h.Add("Set-Cookie", v)
	}
}
