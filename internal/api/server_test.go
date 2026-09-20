package api

import (
	"encoding/json"
	"net/http"
	"testing"

	aosv1 "github.com/Aman123at/agentic-os/gen/go/aos/v1"
)

func TestAPIResponsesAreNotCompressed(t *testing.T) {
	// The API serves this computer, where gzip saves nothing and costs a
	// compressor per response, so aosd answers plainly even when offered gzip.
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Info: func() *aosv1.InfoResponse { return &aosv1.InfoResponse{Mode: "ui"} }}).Handler())
	rec := do(t, h, "POST", "/aos.v1.SystemService/Info", map[string]string{
		"Authorization": "Bearer " + testToken, "Origin": "http://localhost:7700",
		"Content-Type": "application/json", "Accept-Encoding": "gzip, deflate, br",
	})
	var info struct{ Mode string }
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "" || json.Unmarshal(rec.Body.Bytes(), &info) != nil || info.Mode != "ui" {
		t.Fatalf("status %d, Content-Encoding %q, body %q", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
}

func TestTheHandlerServesThePageAndTheHealthCheck(t *testing.T) {
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth}).Handler())
	for path, want := range map[string]int{"/": http.StatusOK, "/healthz": http.StatusOK, "/aos.v1.TaskService/Nope": http.StatusUnauthorized} {
		method := "GET"
		if path != "/" && path != "/healthz" {
			method = "POST"
		}
		if rec := do(t, h, method, path, nil); rec.Code != want {
			t.Errorf("%s %s: %d, want %d", method, path, rec.Code, want)
		}
	}
}

func TestTheRealmProbeIsPublicAndReportsRootMode(t *testing.T) {
	// The login card names the Realm before anyone signs in (M7.10), so /realm is
	// open to an unauthenticated GET and reports the one boolean.
	auth, _ := newAuth(t)
	for _, root := range []bool{false, true} {
		s := &Server{Auth: auth, Info: func() *aosv1.InfoResponse { return &aosv1.InfoResponse{RootMode: root} }}
		h := auth.TCP(s.Handler())
		rec := do(t, h, "GET", "/realm", nil) // no Authorization header
		var body struct {
			RootMode bool `json:"rootMode"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.RootMode != root {
			t.Fatalf("root=%v: status %d, body %q", root, rec.Code, rec.Body.String())
		}
	}
}
