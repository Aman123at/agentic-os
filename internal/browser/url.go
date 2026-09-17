package browser

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// SearchURL turns address-bar text that isn't an address into a search.
const SearchURL = "https://duckduckgo.com/?q="

// Blank is the page the Browser shows before anything is loaded.
const Blank = "about:blank"

// Policy says which pages the Browser may show. Only the web (http, https)
// and about:blank load: file:, chrome: and the rest would open the Machine's
// files or the browser's internals past the Desktop's own controls.
type Policy struct {
	// AosdPort is aosd's port inside the Machine; its pages are refused so the
	// Desktop can't be signed into from inside itself.
	AosdPort int
}

// Normalize turns what the user typed into the address to load: a URL as
// given, a bare host (example.com, localhost:8000) with a scheme added, or
// anything else as a search.
func (p Policy) Normalize(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", errors.New("type an address or a search")
	}
	if strings.EqualFold(s, Blank) {
		return Blank, nil
	}
	if i := strings.Index(s, "://"); i > 0 && !strings.ContainsAny(s[:i], " /?#") {
		return s, p.Check(s)
	}
	if lower := strings.ToLower(s); strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "javascript:") ||
		strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "chrome:") {
		return "", errors.New("the Browser only opens web pages (http and https)")
	}
	if !strings.ContainsAny(s, " \t") && looksLikeHost(s) {
		scheme := "https://"
		if local(hostOf(s)) {
			scheme = "http://"
		}
		u := scheme + s
		return u, p.Check(u)
	}
	return SearchURL + url.QueryEscape(s), nil
}

// Check reports why a page may not load, or nil when it may.
func (p Policy) Check(raw string) error {
	if raw == Blank {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("that address is not valid")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return errors.New("the Browser only opens web pages (http and https)")
	}
	if u.Hostname() == "" {
		return errors.New("that address has no host")
	}
	if p.AosdPort != 0 && local(u.Hostname()) {
		port := u.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[strings.ToLower(u.Scheme)]
		}
		if port == strconv.Itoa(p.AosdPort) {
			return errors.New("the Desktop can't be opened inside its own Browser")
		}
	}
	return nil
}

// hostOf is the host part of "host[:port][/path…]".
func hostOf(s string) string {
	h := s
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(h, "[]")
}

// looksLikeHost reports whether s starts with something that can only be a
// host: a dotted name, an IP address, or localhost, optionally with a port.
func looksLikeHost(s string) bool {
	h := hostOf(s)
	if h == "" {
		return false
	}
	if local(h) || net.ParseIP(h) != nil {
		return true
	}
	if !strings.Contains(h, ".") || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") {
		return false
	}
	for _, r := range h {
		if !hostRune(r) {
			return false
		}
	}
	return true
}

// hostRune reports whether r can appear in a host name (non-ASCII for IDNs).
func hostRune(r rune) bool {
	return r == '.' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r > 127
}

// local reports whether host is this Machine.
func local(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}
