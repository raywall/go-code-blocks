package decision

import (
	"context"
	"fmt"

	gdeengine "github.com/raywall/go-decision-engine/decision/engine"
)

// Evaluate runs a single named rule against the provided flat map.
// Returns the bool result of the CEL expression.
//
// Use this when you need to check a specific rule without running the full set.
//
//	ok, err := router.Evaluate(ctx, "is-pj", map[string]any{"customer_type": "PJ"})
func (b *Block) Evaluate(ctx context.Context, ruleName string, inputs map[string]any) (bool, error) {
	if err := b.checkInit(); err != nil {
		return false, err
	}

	rule, ok := b.rules[ruleName]
	if !ok {
		return false, fmt.Errorf("decision %q: rule %q not found", b.name, ruleName)
	}

	result, err := rule.Evaluate(ctx, inputs)
	if err != nil {
		return false, fmt.Errorf("decision %q evaluate %q: %w", b.name, ruleName, err)
	}
	return result, nil
}

// EvaluateAll runs all registered rules concurrently against the provided
// flat map and returns a Result that can be queried by rule name.
//
//	result, err := router.EvaluateAll(ctx, map[string]any{"customer_type": "PJ"})
//	if result.Passed("is-pj") { ... }
//
// Note: each rule is evaluated against the full inputs map; only the fields
// declared in its Schema are validated and used by the CEL expression.
// Fields unknown to a rule are silently ignored by that rule.
func (b *Block) EvaluateAll(ctx context.Context, inputs map[string]any) (*Result, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	raw, err := b.fanOut(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("decision %q evaluate-all: %w", b.name, err)
	}
	return newResult(raw), nil
}

// EvaluateFrom runs a single named rule against a struct annotated with
// `decision:` struct tags. Only tagged fields are extracted and validated.
//
//	type Customer struct {
//	    Type string `decision:"customer_type"`
//	    ID   string `decision:"-"`
//	}
//	ok, err := router.EvaluateFrom(ctx, "is-pj", Customer{Type: "PJ"})
func (b *Block) EvaluateFrom(ctx context.Context, ruleName string, input any) (bool, error) {
	if err := b.checkInit(); err != nil {
		return false, err
	}

	rule, ok := b.rules[ruleName]
	if !ok {
		return false, fmt.Errorf("decision %q: rule %q not found", b.name, ruleName)
	}

	result, err := rule.EvaluateFrom(ctx, input)
	if err != nil {
		return false, fmt.Errorf("decision %q evaluate-from %q: %w", b.name, ruleName, err)
	}
	return result, nil
}

// EvaluateAllFrom runs every rule concurrently against a struct annotated
// with `decision:` tags, using the engine's native fan-out implementation.
//
//	type Customer struct {
//	    Type    string  `decision:"customer_type"`
//	    Revenue float64 `decision:"revenue"`
//	}
//	result, err := router.EvaluateAllFrom(ctx, Customer{Type: "PJ", Revenue: 5000})
//	if result.Passed("high-value-pj") { ... }
func (b *Block) EvaluateAllFrom(ctx context.Context, input any) (*Result, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	raw, err := b.ruleSet.EvaluateAll(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("decision %q evaluate-all-from: %w", b.name, err)
	}
	return newResult(raw), nil
}

// ── internal ──────────────────────────────────────────────────────────────────

// fanOut evaluates all rules concurrently against a plain map input.
// Unlike DecisionRuleSet.EvaluateAll (which uses struct tags), this path works
// with map[string]any inputs directly.
func (b *Block) fanOut(ctx context.Context, inputs map[string]any) ([]gdeengine.RuleResult, error) {
	type item struct {
		name   string
		result bool
		err    error
	}

	ch := make(chan item, len(b.rules))

	for name, rule := range b.rules {
		name, rule := name, rule
		go func() {
			res, err := rule.Evaluate(ctx, inputs)
			ch <- item{name: name, result: res, err: err}
		}()
	}

	out := make([]gdeengine.RuleResult, 0, len(b.rules))
	for range b.rules {
		it := <-ch
		out = append(out, gdeengine.RuleResult{
			Name:   it.name,
			Result: it.result,
			Error:  it.err,
		})
	}
	return out, nil
}
