// Package policy decides whether a Tool call may run, needs an Approval, or is
// denied (PLAN.md §7.3–7.4). Landlock is the enforcement; policy asks first.
package policy

import "fmt"

// Autonomy is how much an Agent may do without asking.
type Autonomy int

const (
	Auto Autonomy = iota + 1
	ConfirmRisky
	ConfirmAll
)

// Op is what a call does to a path.
type Op int

const (
	// Write changes or creates the path.
	Write Op = iota + 1
	// Delete removes the path (or moves it away).
	Delete
	// Discard throws away uncommitted changes in a git working tree.
	Discard
)

// Effect is one path a call changes.
type Effect struct {
	Path string
	Op   Op
}

// Call is a Tool call as policy sees it.
type Call struct {
	Tool string
	// Coordination Tools (ask_user, remember) never need an Approval.
	Coordination bool
	Risky        bool
	Reasons      []string
	Effects      []Effect
	// Folder is where the call acts, for "Allow for the rest of this Task" grants.
	Folder string
}

// Grant is a Task-scoped "allow": the same Tool in the same folder.
type Grant struct {
	Tool   string
	Folder string
}

// Context is everything else a decision depends on.
type Context struct {
	Autonomy Autonomy
	// Interactive is false when nobody can answer an Approval.
	Interactive bool
	Landlock    bool
	Grants      []Grant
	Protected   *Protection
}

// Verdict is the outcome of Decide.
type Verdict int

const (
	Allow Verdict = iota + 1
	Ask
	Deny
)

// Decision explains a Verdict.
type Decision struct {
	Verdict Verdict
	Reasons []string
	// ProtectedPaths are the Protected Paths the call changes; an approved call
	// runs with the sandbox widened to them.
	ProtectedPaths []string
	// Grantable says whether "Allow for the rest of this Task" may be offered.
	Grantable bool
	// By is what decided: "policy", "autonomy" or "grant".
	By string
}

// Decide applies PLAN.md §7.4.
func Decide(c Call, ctx Context) Decision {
	if c.Coordination {
		return Decision{Verdict: Allow, By: "policy"}
	}
	var hits []string
	var reasons []string
	for _, e := range c.Effects {
		if rule, ok := ctx.Protected.Check(e); ok {
			hits = append(hits, e.Path)
			reasons = append(reasons, fmt.Sprintf("changes Protected Path %s", rule))
		}
	}
	if len(hits) > 0 {
		if !ctx.Interactive {
			return Decision{Verdict: Deny, Reasons: append(reasons, "nobody is available to approve it"), ProtectedPaths: hits, By: "policy"}
		}
		return Decision{Verdict: Ask, Reasons: reasons, ProtectedPaths: hits, By: "policy"}
	}
	autonomy := ctx.Autonomy
	if !ctx.Landlock && autonomy == Auto {
		autonomy = ConfirmRisky
	}
	needsApproval := autonomy == ConfirmAll || (c.Risky && autonomy != Auto)
	if !needsApproval {
		return Decision{Verdict: Allow, By: "autonomy"}
	}
	for _, g := range ctx.Grants {
		if g.Tool == c.Tool && c.Folder != "" && within(c.Folder, g.Folder) {
			return Decision{Verdict: Allow, By: "grant"}
		}
	}
	reasons = c.Reasons
	if !c.Risky {
		reasons = []string{"Autonomy is confirm-all"}
	}
	if !ctx.Interactive {
		return Decision{Verdict: Deny, Reasons: append(reasons, "nobody is available to approve it (aos run --autonomy auto allows Risky Actions)"), By: "policy"}
	}
	return Decision{Verdict: Ask, Reasons: reasons, Grantable: c.Folder != "", By: "policy"}
}
