// Package proxy forwards Services to the Host (PLAN.md §12) and rejects requests
// whose Host header is not local (ADR-0007).
//
//   - http://<port>.localhost:7700/…  → 127.0.0.1:<port>/…
//   - http://localhost:7700/port/<port>/… → 127.0.0.1:<port>/…, served in a CSP
//     sandbox so the page gets an opaque origin and cannot act as the Desktop.
package proxy

import (
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// SessionCookie is the Desktop's sign-in cookie. It is never forwarded to a
// Service, and a Service can never set it.
const SessionCookie = "aos_session"

// sandboxCSP gives path-forwarded pages an opaque origin: no allow-same-origin.
const sandboxCSP = "sandbox allow-scripts allow-forms allow-popups allow-modals allow-downloads"

// New returns a handler that forwards Service requests and passes every other
// request with a local Host to next. selfPort is aosd's own port inside the
// Machine, which is never forwarded.
func New(next http.Handler, selfPort int) http.Handler {
	servicePort := func(s string) (int, bool) {
		n, ok := parsePort(s)
		return n, ok && n != selfPort
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := hostname(r.Host)
		if sub, ok := strings.CutSuffix(name, ".localhost"); ok {
			port, ok := servicePort(sub)
			if !ok {
				http.Error(w, "invalid port", http.StatusBadRequest)
				return
			}
			forward(w, r, port, r.URL.EscapedPath(), false)
			return
		}
		if name != "localhost" && name != "127.0.0.1" && name != "::1" {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/port/")
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		portText, subpath, hasSlash := strings.Cut(rest, "/")
		port, ok := servicePort(portText)
		if !ok {
			http.Error(w, "invalid port", http.StatusBadRequest)
			return
		}
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
			removeCookie(pr.Out.Header, SessionCookie)
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

// filterSetCookie drops cookies a Service must never set: the Desktop's session
// cookie under any spelling a browser would accept, cookies with a Domain
// attribute (a subdomain Service could toss them onto localhost), and anything
// that does not parse.
func filterSetCookie(h http.Header) {
	values := h.Values("Set-Cookie")
	h.Del("Set-Cookie")
	for _, v := range values {
		name, _, _ := strings.Cut(v, "=")
		name = strings.TrimSpace(name)
		c, err := http.ParseSetCookie(v)
		if err != nil || name == "" || strings.EqualFold(name, SessionCookie) || c.Domain != "" {
			continue
		}
		h.Add("Set-Cookie", v)
	}
}
