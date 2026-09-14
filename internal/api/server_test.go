package api

import (
	"net/http"
	"testing"
)

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
