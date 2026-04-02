// blocks/flow/block.go
//
// Package flow provides a pipeline-style request processing block for
// go-code-blocks. It composes independent steps — validation, enrichment,
// CEL decisions, transformation and response — into a single server.Handler
// that is plugged directly into any server block (HTTP, Lambda, TCP).
//
// A Flow replaces the imperative code inside a route handler with a
// declarative sequence of named, testable steps:
//
//	f := flow.New("order-flow",
//	    flow.NewStep("validate-customer",
//	        flow.Validate(validator, "is-pj", func(req *server.Request, _ *flow.State) map[string]any {
//	            return map[string]any{"customer_type": req.Header("X-Customer-Type")}
//	        })),
//
//	    flow.NewStep("load-customer",
//	        flow.Enrich(func(ctx context.Context, req *server.Request, _ *flow.State) (any, error) {
//	            return customersDB.GetItem(ctx, req.PathParam("id"), nil)
//	        })),
//
//	    flow.NewStep("check-limit",
//	        flow.Decide(validator, func(req *server.Request, s *flow.State) map[string]any {
//	            var c Customer
//	            s.Bind("load-customer", &c)
//	            return map[string]any{"amount": c.CreditLimit}
//	        })),
//
//	    flow.NewStep("respond",
//	        flow.Respond(func(ctx context.Context, req *server.Request, s *flow.State) (*server.Response, error) {
//	            var c Customer
//	            s.Bind("load-customer", &c)
//	            return server.JSON(200, map[string]any{
//	                "customer": c,
//	                "approved": s.Passed("check-limit", "high-value"),
//	            }), nil
//	        })),
//	)
//
//	app.MustRegister(f)
//	router.POST("/orders/:id", f.Handler())
package flow

import (
	"context"
	"fmt"
	"net/http"

	"github.com/raywall/go-code-blocks/blocks/server"
	"github.com/raywall/go-code-blocks/core"
)

// ── StepFn ───────────────────────────────────────────────────────────────────

// StepFn is the function signature for every flow step.
//
// It receives the incoming request and the shared State accumulated so far.
// A step can:
//   - Read enriched data:     state.Get("step-name") / state.Bind("step-name", &dest)
//   - Store enriched data:    state.Set("key", value)
//   - Read CEL results:       state.Decision("step-name") / state.Passed("step", "rule")
//   - Abort the flow:         state.Abort(server.Error(422, "invalid"))
//   - Set final response:     state.Respond(server.JSON(200, result))
//   - Return a fatal error:   return err  →  caller receives HTTP 500
type StepFn func(ctx context.Context, req *server.Request, state *State) error

// ── Step ─────────────────────────────────────────────────────────────────────

// Step associates a name with a StepFn. The name is used for logging,
// error messages, and as the automatic storage key for Enrich and Decide steps.
type Step struct {
	name string
	fn   StepFn
}

// NewStep creates a named Step. It is the primary way to build generic steps
// inline within flow.New. For typed steps prefer EnrichStep and DecideStep,
// which capture the name automatically as the state storage key.
//
//	flow.New("checkout-flow",
//	    flow.NewStep("validate", flow.Validate(...)),
//	    flow.EnrichStep("load-user", fn),
//	    flow.DecideStep("check-limit", rules, fn),
//	    flow.NewStep("respond", flow.Respond(...)),
//	)
func NewStep(name string, fn StepFn) Step {
	return Step{name: name, fn: fn}
}

// ── Block ─────────────────────────────────────────────────────────────────────

// Block is a flow pipeline block.
// It implements core.Block and produces a server.Handler via Handler().
type Block struct {
	name  string
	steps []Step
}

// New creates a new flow Block with the given ordered steps.
func New(name string, steps ...Step) *Block {
	return &Block{name: name, steps: steps}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block. Flow blocks have no I/O resources to open;
// Init validates that at least one step is configured.
func (b *Block) Init(_ context.Context) error {
	if len(b.steps) == 0 {
		return fmt.Errorf("flow %q: no steps configured", b.name)
	}
	return nil
}

// Shutdown implements core.Block. No-op for flows.
func (b *Block) Shutdown(_ context.Context) error { return nil }

// Handler returns a server.Handler that executes the flow for every request.
// The returned handler is safe to register on multiple routes.
//
//	router.POST("/orders/:id", orderFlow.Handler())
//	router.PUT("/orders/:id",  orderFlow.Handler())
func (b *Block) Handler() server.Handler {
	return func(ctx context.Context, req *server.Request) (*server.Response, error) {
		return b.execute(ctx, req)
	}
}

// ── Execution engine ──────────────────────────────────────────────────────────

// execute runs all steps in order, threading the shared State between them.
// It stops early when a step calls state.Abort or returns an error.
func (b *Block) execute(ctx context.Context, req *server.Request) (*server.Response, error) {
	state := newState()

	for _, step := range b.steps {
		// Check context cancellation before each step.
		if ctx.Err() != nil {
			return server.Error(http.StatusServiceUnavailable, "request cancelled"), nil
		}

		if err := step.fn(ctx, req, state); err != nil {
			return server.Error(http.StatusInternalServerError,
				fmt.Sprintf("flow %q step %q: %s", b.name, step.name, err.Error()),
			), nil
		}

		// Abort short-circuits — no further steps run.
		if state.aborted {
			if state.response != nil {
				return state.response, nil
			}
			return server.NoContent(), nil
		}
	}

	// All steps completed — return whatever the Respond step set, or 204.
	if state.response != nil {
		return state.response, nil
	}
	return server.NoContent(), nil
}

// Ensure Block implements core.Block.
var _ core.Block = (*Block)(nil)
