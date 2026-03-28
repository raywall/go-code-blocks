package decision

// Option configures a Decision Block.
type Option func(*blockConfig)

type blockConfig struct {
	rules []RuleDefinition
}

// WithRule adds a named CEL rule to the block.
//
// name       — unique identifier used to query the result (e.g. "is-pj")
// expression — CEL expression that must evaluate to bool (e.g. `customer_type == "PJ"`)
// schema     — maps each variable referenced in the expression to its ArgType
//
// Rules are compiled once during Init and cached for the lifetime of the block.
// Multiple calls to WithRule register multiple rules; they are all evaluated
// concurrently when EvaluateAll is called.
//
//	decision.WithRule("is-pj", `customer_type == "PJ"`, decision.Schema{
//	    "customer_type": decision.String,
//	})
func WithRule(name, expression string, schema Schema) Option {
	return func(c *blockConfig) {
		c.rules = append(c.rules, RuleDefinition{
			name:       name,
			expression: expression,
			schema:     schema,
		})
	}
}
