// Package decision provides a CEL-based rule evaluation block for go-code-blocks.
//
// It integrates github.com/raywall/go-decision-engine to let you define typed
// business rules using Google's Common Expression Language (CEL), then evaluate
// them against runtime data to drive routing decisions across other blocks.
//
// # Typical usage
//
//	router := decision.New("customer-router",
//	    decision.WithRule("is-pj", `customer_type == "PJ"`, decision.Schema{
//	        "customer_type": decision.String,
//	    }),
//	    decision.WithRule("is-pf", `customer_type == "PF"`, decision.Schema{
//	        "customer_type": decision.String,
//	    }),
//	)
//
//	// Register alongside other blocks
//	app.MustRegister(router)
//	app.InitAll(ctx)
//
//	// Evaluate at request time
//	result, err := router.EvaluateAll(ctx, map[string]any{
//	    "customer_type": "PJ",
//	})
//
//	switch {
//	case result.Passed("is-pj"):
//	    data, _ := dynamoBlock.GetItem(ctx, id, nil)
//	case result.Passed("is-pf"):
//	    return ErrPersonFisicaNotSupported
//	}
package decision

import (
	"context"
	"fmt"

	"github.com/raywall/go-code-blocks/core"
	gde "github.com/raywall/go-decision-engine/decision"
	gdeengine "github.com/raywall/go-decision-engine/decision/engine"
)

// Block is a CEL-based decision block.
// It holds a set of named rules compiled during Init, then evaluated
// on demand at request time.
type Block struct {
	name    string
	cfg     blockConfig
	cache   *gdeengine.RuleCache
	rules   map[string]*gde.DecisionRule // keyed by rule name, populated on Init
	ruleSet *gde.DecisionRuleSet         // ordered slice, used for EvaluateAll
}

// New creates a new Decision Block.
//
//	router := decision.New("customer-router",
//	    decision.WithRule("is-pj", `customer_type == "PJ"`, decision.Schema{
//	        "customer_type": decision.String,
//	    }),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block{
		name:  name,
		cfg:   cfg,
		rules: make(map[string]*gde.DecisionRule),
	}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block.
// It creates the shared rule cache and compiles every registered rule.
// Compilation errors are surfaced immediately so misconfigured expressions
// are caught at startup, not at request time.
func (b *Block) Init(_ context.Context) error {
	b.cache = gdeengine.NewRuleCache()

	compiled := make([]*gde.DecisionRule, 0, len(b.cfg.rules))

	for _, def := range b.cfg.rules {
		rule, err := gde.NewDecisionRule(def.name, def.expression, def.schema, b.cache)
		if err != nil {
			return fmt.Errorf("decision %q: compile rule %q (%q): %w",
				b.name, def.name, def.expression, err)
		}
		b.rules[def.name] = rule
		compiled = append(compiled, rule)
	}

	b.ruleSet = &gde.DecisionRuleSet{Rules: compiled}
	return nil
}

// Shutdown implements core.Block.
func (b *Block) Shutdown(_ context.Context) error { return nil }

// ── helpers ──────────────────────────────────────────────────────────────────

func (b *Block) checkInit() error {
	if b.cache == nil {
		return fmt.Errorf("decision %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}
