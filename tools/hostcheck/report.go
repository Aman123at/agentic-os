// Package hostcheck implements `aos doctor --host-check`: the M0 prototypes run
// inside the Machine as a pass/fail report, so every Host (macOS, Windows,
// Linux) can be verified with one command (PLAN.md §18, M0.8).
package hostcheck

import (
	"fmt"
	"io"
	"strings"
)

// Status is the outcome of one check.
type Status string

const (
	Pass Status = "PASS"
	Fail Status = "FAIL"
	Skip Status = "SKIP"
	// Known is a failure that M0 documented as a design finding awaiting a decision.
	// It is reported, but does not fail the host check.
	Known Status = "KNWN"
)

// Result is one line of the report.
type Result struct {
	ID     string // prototype number from PLAN.md §18, e.g. "0.1"
	Name   string
	Status Status
	Detail string
}

// Report is the full host check.
type Report []Result

// Failed reports whether any check failed.
func (r Report) Failed() bool {
	for _, res := range r {
		if res.Status == Fail {
			return true
		}
	}
	return false
}

// Write prints the report for humans.
func (r Report) Write(w io.Writer) {
	counts := map[Status]int{}
	for _, res := range r {
		counts[res.Status]++
		line := fmt.Sprintf("%-4s  %-4s %s", res.Status, res.ID, res.Name)
		if res.Detail != "" {
			line += "  (" + strings.ReplaceAll(res.Detail, "\n", " ") + ")"
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "\n%d passed, %d failed, %d skipped, %d known design findings (docs/m0-findings.md)\n",
		counts[Pass], counts[Fail], counts[Skip], counts[Known])
}

// recorder collects results for one prototype.
type recorder struct {
	id      string
	results *Report
}

func (rec recorder) add(name string, status Status, detail string, args ...any) {
	*rec.results = append(*rec.results, Result{ID: rec.id, Name: name, Status: status, Detail: fmt.Sprintf(detail, args...)})
}

// known records a check whose failure is a documented design finding.
func (rec recorder) known(finding, name string, ok bool, detail string, args ...any) {
	if ok {
		rec.add(name, Pass, detail, args...)
		return
	}
	rec.add(name, Known, "finding "+finding+": "+detail, args...)
}

func (rec recorder) check(name string, ok bool, detail string, args ...any) {
	status := Pass
	if !ok {
		status = Fail
	}
	rec.add(name, status, detail, args...)
}
