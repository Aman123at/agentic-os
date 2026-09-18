package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/store"
)

func TestDaysListEachDayOldestFirstWithZerosForQuietDays(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := time.Date(2026, 9, 13, 12, 0, 0, 0, time.Local)
	tr := &Tracker{DB: db, Now: func() time.Time { return clock }}
	ctx := context.Background()
	record := func(model string, u llm.Usage) {
		t.Helper()
		if _, _, err := tr.Record(ctx, model, u); err != nil {
			t.Fatal(err)
		}
	}

	// The 13th: two models. The 14th: nothing. The 15th: one model.
	record("gpt-a", llm.Usage{InputTokens: 100, OutputTokens: 10})
	record("gpt-b", llm.Usage{InputTokens: 50})
	clock = clock.AddDate(0, 0, 2)
	record("gpt-a", llm.Usage{InputTokens: 7})

	days, err := tr.Days(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		day         string
		in, out     int64
		costUnknown bool // no prices.yaml here, so a day with usage has an unknown cost
	}{
		{"2026-09-12", 0, 0, false},
		{"2026-09-13", 150, 10, true},
		{"2026-09-14", 0, 0, false},
		{"2026-09-15", 7, 0, true},
	}
	if len(days) != len(want) {
		t.Fatalf("got %d days, want %d", len(days), len(want))
	}
	for i, w := range want {
		d := days[i]
		if d.Day != w.day || d.Usage.InputTokens != w.in || d.Usage.OutputTokens != w.out || d.Usage.CostKnown == w.costUnknown {
			t.Errorf("day %d: %s in %d out %d cost known %v; want %+v", i, d.Day, d.Usage.InputTokens, d.Usage.OutputTokens, d.Usage.CostKnown, w)
		}
	}
}
