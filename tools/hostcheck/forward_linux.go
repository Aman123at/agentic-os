package hostcheck

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amantiwari/agentic-os/internal/proxy"
)

// checkForwarding is prototype M0.5 without the browser part: HTTP and WebSocket
// forwarding through the running aosd, by subdomain and by path.
func checkForwarding(ctx context.Context, m *machine, rec recorder) {
	svc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		rec.add("start a test Service", Fail, "%v", err)
		return
	}
	defer svc.Close()
	go http.Serve(svc, testService())
	port := strconv.Itoa(svc.Addr().(*net.TCPAddr).Port)

	addr, via := m.opts.AosdAddr, "aosd"
	if !reachable(addr) {
		// aosd is not running (e.g. in CI's test container): check the same handler in-process.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			rec.add("start in-process proxy", Fail, "%v", err)
			return
		}
		defer ln.Close()
		go http.Serve(ln, proxy.New(http.NotFoundHandler(), 7700))
		addr, via = ln.Addr().String(), "in-process proxy (aosd not running)"
	}

	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	get := func(host, path string) (*http.Response, string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
		req.Host = host
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body), nil
	}

	resp, body, err := get(port+".localhost:7700", "/hello")
	rec.check("subdomain forwarding ("+via+")", err == nil && resp.StatusCode == 200 && body == "service:/hello", "%v %s", err, status(resp, body))

	resp, body, err = get("localhost:7700", "/port/"+port+"/hello")
	csp := ""
	if resp != nil {
		csp = resp.Header.Get("Content-Security-Policy")
	}
	rec.check("path forwarding with CSP sandbox", err == nil && resp.StatusCode == 200 && body == "service:/hello" &&
		strings.HasPrefix(csp, "sandbox") && !strings.Contains(csp, "allow-same-origin"), "%v %s csp=%q", err, status(resp, body), csp)

	resp, body, err = get("aos.attacker.example:7700", "/")
	rec.check("DNS-rebinding Host is rejected", err == nil && resp.StatusCode == http.StatusMisdirectedRequest, "%v %s", err, status(resp, body))

	for _, tc := range []struct{ name, host, path string }{
		{"WebSocket via subdomain", port + ".localhost:7700", "/ws"},
		{"WebSocket via path", "localhost:7700", "/port/" + port + "/ws"},
	} {
		err := echoUpgrade(addr, tc.host, tc.path)
		rec.check(tc.name, err == nil, "%v", err)
	}
}

func status(resp *http.Response, body string) string {
	if resp == nil {
		return ""
	}
	return fmt.Sprintf("HTTP %d %q", resp.StatusCode, truncate(body, 60))
}

func reachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// testService answers /hello and upgrades /ws to a raw echo connection, which is
// all a reverse proxy sees of a WebSocket.
func testService() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "service:"+r.URL.Path)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "upgrade required", http.StatusUpgradeRequired)
			return
		}
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		buf.Flush()
		line, _ := buf.ReadString('\n')
		buf.WriteString("echo:" + line)
		buf.Flush()
	})
	return mux
}

func echoUpgrade(addr, host, path string) error {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", path, host)
	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("HTTP %d, want 101", resp.StatusCode)
	}
	fmt.Fprint(conn, "ping\n")
	got, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	if got != "echo:ping\n" {
		return fmt.Errorf("echo %q", got)
	}
	return nil
}
