package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aman123at/agentic-os/internal/audit"
	"github.com/Aman123at/agentic-os/internal/store"
)

// fakeKey stands in for aosd's key file.
type fakeKey struct{ key string }

func (f *fakeKey) Set(key string) (string, error) {
	if len(key) < 20 {
		return "", errors.New("that is not an API key")
	}
	f.key = key
	return key[:3] + "…" + key[len(key)-4:], nil
}

func (f *fakeKey) Clear() (string, error) { f.key = ""; return "", nil }

func TestTheAPIKeyNeverComesBackOrReachesTheAuditLog(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	log := &audit.Log{DB: db}
	key := &fakeKey{}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Audit: log, APIKey: key}).Handler())

	const secret = "sk-proj-verysecret-0000000000wxyz"
	rec := call(t, h, "/aos.v1.SettingsService/SetApiKey", `{"key":"`+secret+`"}`)
	var set struct{ Hint string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &set) != nil || set.Hint != "sk-…wxyz" || key.key != secret {
		t.Fatalf("SetApiKey: %d %s (saved %q)", rec.Code, rec.Body, key.key)
	}
	if rec := call(t, h, "/aos.v1.SettingsService/SetApiKey", `{"key":"sk-short"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an invalid key: %d %s, want 400", rec.Code, rec.Body)
	}
	if rec := call(t, h, "/aos.v1.SettingsService/ClearApiKey", `{}`); rec.Code != http.StatusOK || key.key != "" {
		t.Errorf("ClearApiKey: %d %s", rec.Code, rec.Body)
	}

	entries, err := log.List(ctx, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	var tools []string
	for _, e := range entries {
		tools = append(tools, e.Tool)
		for _, field := range []string{e.ArgumentsJson, e.ResultSummary} {
			if strings.Contains(field, "verysecret") {
				t.Errorf("the key reached the Audit Log: %q", field)
			}
		}
	}
	if strings.Join(tools, ",") != "clear_api_key,set_api_key,set_api_key" {
		t.Errorf("audited %v, want each change", tools)
	}
}
