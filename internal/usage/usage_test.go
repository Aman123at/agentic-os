package usage

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aman123at/agentic-os/internal/llm"
	"github.com/Aman123at/agentic-os/internal/store"
)

const example = `# USD per 1M tokens
gpt-big:
  input: 2.00        # fresh input
  cached_input: 0.5
  output: 8
"gpt-big-mini":
  input: 0.25
  output: 1
`

func TestPricesParseAndPriceDatedSnapshots(t *testing.T) {
	p, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	if p["gpt-big"] != (Price{Input: 2, CachedInput: 0.5, Output: 8}) || p["gpt-big-mini"] != (Price{Input: 0.25, Output: 1}) {
		t.Fatalf("prices %+v", p)
	}
	u := llm.Usage{InputTokens: 1_000_000, CachedInputTokens: 400_000, OutputTokens: 100_000, ReasoningTokens: 50_000}
	// 600k fresh × $2 + 400k cached × $0.5 + 100k output × $8 = 1.2 + 0.2 + 0.8.
	for model, want := range map[string]float64{"gpt-big": 2.2, "gpt-big-2026-08-01": 2.2, "gpt-big-mini-2026-08-01": 0.25} {
		cost, ok := p.Cost(model, u)
		if !ok || math.Abs(cost-want) > 1e-9 {
			t.Errorf("%s: $%v, %v; want $%v", model, cost, ok, want)
		}
	}
	if _, ok := p.Cost("gpt-bigger", u); ok {
		t.Error("gpt-bigger is priced as gpt-big")
	}
}

func TestPriceFileErrorsNameTheLine(t *testing.T) {
	for text, want := range map[string]string{
		"gpt-x:\n  input: cheap\n": "line 2",
		"  input: 1\n":             "before any model",
		"gpt-x:\n  inptu: 1\n":     `unknown field "inptu"`,
		"gpt-x: 1\n":               "line 1",
	} {
		if _, err := Parse([]byte(text)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", text, err, want)
		}
	}
}

func TestTheTrackerAddsUpEachDayAndRereadsChangedPrices(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "aos.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 14, 23, 0, 0, 0, time.Local)
	path := filepath.Join(dir, "prices.yaml")
	tr := &Tracker{DB: db, Prices: &File{Path: path}, Now: func() time.Time { return now }}
	ctx := context.Background()
	u := llm.Usage{InputTokens: 500_000, OutputTokens: 50_000}

	if cost, known, err := tr.Record(ctx, "gpt-big", u); err != nil || known || cost != 0 {
		t.Fatalf("without prices.yaml: $%v known=%v %v", cost, known, err)
	}
	if err := os.WriteFile(path, []byte(example), 0o600); err != nil {
		t.Fatal(err)
	}
	if cost, known, _ := tr.Record(ctx, "gpt-big", u); !known || math.Abs(cost-1.4) > 1e-9 {
		t.Fatalf("with prices: $%v known=%v", cost, known)
	}
	today, err := tr.Today(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if today.InputTokens != 1_000_000 || today.OutputTokens != 100_000 || math.Abs(today.CostUsd-1.4) > 1e-9 || today.CostKnown {
		t.Errorf("today %+v", today)
	}
	now = now.Add(2 * time.Hour) // the next day
	if today, _ := tr.Today(ctx); today.InputTokens != 0 || !today.CostKnown {
		t.Errorf("the next day %+v", today)
	}
}
