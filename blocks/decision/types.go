package decision

import (
	gdeengine "github.com/raywall/go-decision-engine/decision/engine"
)

// ── Type aliases ─────────────────────────────────────────────────────────────
// Re-exported from go-decision-engine so callers do not need to import
// that package directly when building their blocks.

// ArgType is the CEL-compatible type for a rule input variable.
type ArgType = gdeengine.ArgType

// Schema maps variable names to their CEL type.
// It is the argument type descriptor for a DecisionRule.
type Schema = map[string]ArgType

// Supported ArgType constants.
const (
	String = gdeengine.StringType
	Int    = gdeengine.IntType
	Bool   = gdeengine.BoolType
	Float  = gdeengine.FloatType
)

// ── Rule definition ───────────────────────────────────────────────────────────

// RuleDefinition holds the static declaration of a rule before compilation.
type RuleDefinition struct {
	name       string
	expression string
	schema     Schema
}

// ── Result ────────────────────────────────────────────────────────────────────

// Result is returned by Evaluate and EvaluateAll.
// It provides a clean API for inspecting which rules passed or failed.
type Result struct {
	// raw stores RuleResult indexed by rule name for O(1) access.
	raw map[string]gdeengine.RuleResult
}

// newResult builds a Result from the raw slice returned by the engine.
func newResult(raw []gdeengine.RuleResult) *Result {
	index := make(map[string]gdeengine.RuleResult, len(raw))
	for _, r := range raw {
		index[r.Name] = r
	}
	return &Result{raw: index}
}

// Passed reports whether the named rule evaluated to true.
// Returns false if the rule is unknown or errored.
func (r *Result) Passed(name string) bool {
	rr, ok := r.raw[name]
	return ok && rr.Result && rr.Error == nil
}

// Failed reports whether the named rule evaluated to false (not an error).
// Returns true for unknown names.
func (r *Result) Failed(name string) bool {
	return !r.Passed(name)
}

// Err returns the evaluation error for the named rule, or nil.
func (r *Result) Err(name string) error {
	rr, ok := r.raw[name]
	if !ok {
		return nil
	}
	return rr.Error
}

// PassedNames returns the names of all rules that evaluated to true.
func (r *Result) PassedNames() []string {
	var names []string
	for name, rr := range r.raw {
		if rr.Result && rr.Error == nil {
			names = append(names, name)
		}
	}
	return names
}

// FailedNames returns the names of all rules that evaluated to false or errored.
func (r *Result) FailedNames() []string {
	var names []string
	for name, rr := range r.raw {
		if !rr.Result || rr.Error != nil {
			names = append(names, name)
		}
	}
	return names
}

// Any reports whether at least one rule passed.
func (r *Result) Any() bool {
	for _, rr := range r.raw {
		if rr.Result && rr.Error == nil {
			return true
		}
	}
	return false
}

// All reports whether every rule passed.
func (r *Result) All() bool {
	for _, rr := range r.raw {
		if !rr.Result || rr.Error != nil {
			return false
		}
	}
	return len(r.raw) > 0
}

// None reports whether no rule passed.
func (r *Result) None() bool { return !r.Any() }
