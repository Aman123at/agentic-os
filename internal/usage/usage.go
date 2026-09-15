// Package usage accounts for model usage and its estimated cost (PLAN.md §8.4):
// per Task and per day, priced from the user-editable prices.yaml.
package usage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	aosv1 "github.com/amantiwari/agentic-os/gen/go/aos/v1"
	"github.com/amantiwari/agentic-os/internal/llm"
	"github.com/amantiwari/agentic-os/internal/store"
)

// DefaultPrices is prices.yaml as aosd first writes it.
const DefaultPrices = `# Model prices for estimated costs and Cost Limits (PLAN.md §8.4), in USD per
# 1M tokens: OpenAI's Standard tier for short contexts, as listed at
# https://developers.openai.com/api/docs/pricing on 2026-09-14.
#
# Prices change: edit this file (docker compose exec aos nano /var/lib/aos/prices.yaml).
# aosd reads it again before every model request. A model without an entry
# has an unknown cost, and Cost Limits cannot apply to it.
gpt-5.6-terra:
  input: 2.00
  cached_input: 0.20
  output: 12.00
`

// Price is what a model costs, in USD per million tokens.
type Price struct {
	Input, CachedInput, Output float64
}

// Prices maps model names to prices.
type Prices map[string]Price

// Lookup returns the price of model: its own entry, or the longest entry its
// name continues with a dash (a dated snapshot such as gpt-x-2026-01-01).
func (p Prices) Lookup(model string) (Price, bool) {
	if pr, ok := p[model]; ok {
		return pr, true
	}
	best := ""
	for name := range p {
		if strings.HasPrefix(model, name+"-") && len(name) > len(best) {
			best = name
		}
	}
	pr, ok := p[best]
	return pr, ok && best != ""
}

// Cost estimates what u cost with model. Reasoning tokens are part of the
// output tokens.
func (p Prices) Cost(model string, u llm.Usage) (float64, bool) {
	pr, ok := p.Lookup(model)
	if !ok {
		return 0, false
	}
	cached := min(u.CachedInputTokens, u.InputTokens)
	fresh := u.InputTokens - cached
	return (float64(fresh)*pr.Input + float64(cached)*pr.CachedInput + float64(u.OutputTokens)*pr.Output) / 1e6, true
}

// Parse reads prices.yaml: a model name followed by its indented prices.
//
//	gpt-example:
//	  input: 1.25         # USD per 1M input tokens
//	  cached_input: 0.125 # USD per 1M cached input tokens
//	  output: 10          # USD per 1M output tokens
func Parse(data []byte) (Prices, error) {
	prices := Prices{}
	model := ""
	for i, line := range strings.Split(string(data), "\n") {
		if c := strings.Index(line, "#"); c == 0 || c > 0 && (line[c-1] == ' ' || line[c-1] == '\t') {
			line = line[:c]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		key, value = strings.Trim(strings.TrimSpace(key), `"'`), strings.TrimSpace(value)
		switch {
		case !ok || key == "":
			return nil, fmt.Errorf("prices.yaml line %d: expected \"name:\" or \"field: number\"", i+1)
		case line[0] != ' ' && line[0] != '\t':
			if value != "" {
				return nil, fmt.Errorf("prices.yaml line %d: a model name ends with \":\" and its prices follow, indented", i+1)
			}
			model = key
			prices[model] = Price{}
		case model == "":
			return nil, fmt.Errorf("prices.yaml line %d: a price before any model name", i+1)
		default:
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("prices.yaml line %d: %q is not a price", i+1, value)
			}
			pr := prices[model]
			switch key {
			case "input":
				pr.Input = n
			case "cached_input":
				pr.CachedInput = n
			case "output":
				pr.Output = n
			default:
				return nil, fmt.Errorf("prices.yaml line %d: unknown field %q (use input, cached_input or output)", i+1, key)
			}
			prices[model] = pr
		}
	}
	return prices, nil
}

// File is prices.yaml, read again whenever it changes.
type File struct {
	Path string

	mu     sync.Mutex
	mod    time.Time
	size   int64
	prices Prices
	err    error
}

// Prices returns the file's prices; a missing file has none.
func (f *File) Prices() (Prices, error) {
	if f == nil {
		return nil, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fi, err := os.Stat(f.Path)
	if err != nil {
		f.prices, f.err, f.mod = nil, nil, time.Time{}
		if !os.IsNotExist(err) {
			f.err = err
		}
		return f.prices, f.err
	}
	if !fi.ModTime().Equal(f.mod) || fi.Size() != f.size {
		f.mod, f.size = fi.ModTime(), fi.Size()
		data, err := os.ReadFile(f.Path)
		if err == nil {
			f.prices, f.err = Parse(data)
		} else {
			f.prices, f.err = nil, err
		}
	}
	return f.prices, f.err
}

// Tracker records usage per day.
type Tracker struct {
	DB *store.DB
	// Prices may be nil: costs are then unknown.
	Prices *File
	Now    func() time.Time
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tracker) day() string { return t.now().Format("2006-01-02") }

// Cost estimates what u cost with model; a broken prices.yaml means unknown.
func (t *Tracker) Cost(model string, u llm.Usage) (float64, bool) {
	prices, err := t.Prices.Prices()
	if err != nil {
		return 0, false
	}
	return prices.Cost(model, u)
}

// Record adds one response's usage to today's totals and returns its estimated cost.
func (t *Tracker) Record(ctx context.Context, model string, u llm.Usage) (cost float64, known bool, err error) {
	cost, known = t.Cost(model, u)
	err = t.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage (day, model, input_tokens, cached_input_tokens, output_tokens, reasoning_tokens, cost_usd, cost_known)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (day, model) DO UPDATE SET
			  input_tokens = input_tokens + excluded.input_tokens,
			  cached_input_tokens = cached_input_tokens + excluded.cached_input_tokens,
			  output_tokens = output_tokens + excluded.output_tokens,
			  reasoning_tokens = reasoning_tokens + excluded.reasoning_tokens,
			  cost_usd = cost_usd + excluded.cost_usd,
			  cost_known = cost_known AND excluded.cost_known`,
			t.day(), model, u.InputTokens, u.CachedInputTokens, u.OutputTokens, u.ReasoningTokens, cost, known)
		return err
	})
	return cost, known, err
}

// Today returns today's usage over every model.
func (t *Tracker) Today(ctx context.Context) (*aosv1.Usage, error) {
	u := &aosv1.Usage{}
	err := t.DB.Read().QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens), 0), COALESCE(SUM(cached_input_tokens), 0),
		COALESCE(SUM(output_tokens), 0), COALESCE(SUM(reasoning_tokens), 0), COALESCE(SUM(cost_usd), 0), COALESCE(MIN(cost_known), 1)
		FROM usage WHERE day = ?`, t.day()).Scan(&u.InputTokens, &u.CachedInputTokens, &u.OutputTokens, &u.ReasoningTokens, &u.CostUsd, &u.CostKnown)
	return u, err
}

// Days returns the last n days' usage over every model, today included and
// oldest first. A day without usage has zeros, so a chart needs no gaps filled.
func (t *Tracker) Days(ctx context.Context, n int) ([]*aosv1.DailyUsage, error) {
	now := t.now()
	rows, err := t.DB.Read().QueryContext(ctx, `SELECT day, SUM(input_tokens), SUM(cached_input_tokens), SUM(output_tokens),
		SUM(reasoning_tokens), SUM(cost_usd), MIN(cost_known) FROM usage WHERE day >= ? GROUP BY day`,
		now.AddDate(0, 0, -(n-1)).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDay := map[string]*aosv1.Usage{}
	for rows.Next() {
		var day string
		u := &aosv1.Usage{}
		if err := rows.Scan(&day, &u.InputTokens, &u.CachedInputTokens, &u.OutputTokens, &u.ReasoningTokens, &u.CostUsd, &u.CostKnown); err != nil {
			return nil, err
		}
		byDay[day] = u
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]*aosv1.DailyUsage, 0, n)
	for i := n - 1; i >= 0; i-- {
		day := now.AddDate(0, 0, -i).Format("2006-01-02")
		u := byDay[day]
		if u == nil {
			u = &aosv1.Usage{CostKnown: true}
		}
		out = append(out, &aosv1.DailyUsage{Day: day, Usage: u})
	}
	return out, nil
}
