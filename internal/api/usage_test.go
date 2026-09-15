package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/store"
	"github.com/amantiwari/agentic-os/internal/usage"
)

func TestUsageListsTheLastDaysEndingToday(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tr := &usage.Tracker{DB: db}
	if _, _, err := tr.Record(context.Background(), "gpt-a", llm.Usage{InputTokens: 42}); err != nil {
		t.Fatal(err)
	}
	auth, _ := newAuth(t)
	h := auth.TCP((&Server{Auth: auth, Usage: tr}).Handler())

	rec := call(t, h, "/aos.v1.SystemService/Usage", `{"days":3}`)
	var reply struct {
		Days []struct {
			Day   string
			Usage struct{ InputTokens string } // protojson writes int64 as a string
		}
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || len(reply.Days) != 3 {
		t.Fatalf("Usage for 3 days: %d %s", rec.Code, rec.Body)
	}
	if last := reply.Days[2]; last.Day != time.Now().Format("2006-01-02") || last.Usage.InputTokens != "42" {
		t.Errorf("the last day should be today, with its usage: %+v", last)
	}
	if rec := call(t, h, "/aos.v1.SystemService/Usage", `{"days":400}`); rec.Code != http.StatusBadRequest {
		t.Errorf("400 days: %d, want 400", rec.Code)
	}
}
