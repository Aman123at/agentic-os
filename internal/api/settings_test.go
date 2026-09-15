package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/amantiwari/agentic-os/internal/audit"
	"github.com/amantiwari/agentic-os/internal/policy"
	"github.com/amantiwari/agentic-os/internal/settings"
	"github.com/amantiwari/agentic-os/internal/store"
)

// settingJSON is a Setting as the API sends it. Responses are decoded, never
// compared as text: protojson varies its whitespace on purpose.
type settingJSON struct{ Key, Value, Source, Env, Fallback string }

// call makes an authenticated JSON RPC, as the Desktop does.
func call(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Host = "localhost:7700"
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Origin", "http://localhost:7700")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSettingsCanBeReadAndChangedAndEveryChangeIsAudited(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := settings.Open(ctx, db, settings.Values{Model: "gpt-5.6-terra", Autonomy: policy.ConfirmRisky, MaxTasks: 3,
		MaxRetries: 3, TrashRetentionDays: 30, TrashMaxGB: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	log := &audit.Log{DB: db}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Audit: log, Settings: st}).Handler())

	saved := settingJSON{Key: "max_tasks", Value: "5", Source: "settings", Env: "AOS_MAX_TASKS", Fallback: "3"}
	var updated struct{ Setting settingJSON }
	rec := call(t, h, "/aos.v1.SettingsService/Update", `{"key":"max_tasks","value":"5"}`)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &updated) != nil || updated.Setting != saved {
		t.Fatalf("Update max_tasks=5: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, h, "/aos.v1.SettingsService/Update", `{"key":"max_tasks","value":"99"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("Update max_tasks=99: %d %s, want 400", rec.Code, rec.Body)
	}
	var list struct{ Settings []settingJSON }
	rec = call(t, h, "/aos.v1.SettingsService/Get", `{}`)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || !slices.Contains(list.Settings, saved) {
		t.Errorf("Get should list max_tasks as saved: %d %s", rec.Code, rec.Body)
	}

	entries, err := log.List(ctx, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Tool != "settings_update" || !strings.Contains(entries[0].ResultSummary, "whole number") ||
		entries[1].ResultSummary != "5" {
		t.Errorf("the Audit Log should hold both changes, the refused one with its reason: %v", entries)
	}
}
