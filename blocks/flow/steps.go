// blocks/flow/steps.go
package flow

import (
	"context"
	"fmt"
	"net/http"

	"github.com/raywall/go-code-blocks/blocks/decision"
	"github.com/raywall/go-code-blocks/blocks/server"
)

// ── Validate ──────────────────────────────────────────────────────────────────

// ValidateOption configures the behaviour of a Validate step.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	failStatus  int
	failMessage func(ruleName string) string
}

// WithFailStatus overrides the HTTP status code returned when validation fails.
// Defaults to 422 Unprocessable Entity.
func WithFailStatus(code int) ValidateOption {
	return func(c *validateConfig) { c.failStatus = code }
}

// WithFailMessage sets a custom message formatter for validation failures.
// The rule name that failed is passed as the argument.
//
//	flow.WithFailMessage(func(rule string) string {
//	    return fmt.Sprintf("business rule %q not satisfied", rule)
//	})
func WithFailMessage(fn func(ruleName string) string) ValidateOption {
	return func(c *validateConfig) { c.failMessage = fn }
}

// Validate runs a single CEL rule from the decision block.
// If the rule fails, the flow is aborted with HTTP 422 (or the status set via
// WithFailStatus). If it passes, the flow continues normally.
//
// inputFn builds the map of CEL variables from the current request and state.
//
//	flow.Step("check-type",
//	    flow.Validate(validator, "is-pj",
//	        func(req *server.Request, _ *flow.State) map[string]any {
//	            return map[string]any{"customer_type": req.Header("X-Customer-Type")}
//	        },
//	        flow.WithFailStatus(http.StatusForbidden),
//	    ))
func Validate(
	d *decision.Block,
	ruleName string,
	inputFn func(req *server.Request, state *State) map[string]any,
	opts ...ValidateOption,
) StepFn {
	cfg := validateConfig{
		failStatus: http.StatusUnprocessableEntity,
		failMessage: func(rule string) string {
			return fmt.Sprintf("validation failed: rule %q not satisfied", rule)
		},
	}
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx context.Context, req *server.Request, state *State) error {
		inputs := inputFn(req, state)
		ok, err := d.Evaluate(ctx, ruleName, inputs)
		if err != nil {
			return fmt.Errorf("validate %q: %w", ruleName, err)
		}
		if !ok {
			state.Abort(server.Error(cfg.failStatus, cfg.failMessage(ruleName)))
		}
		return nil
	}
}

// ValidateFrom runs a single CEL rule against a struct with `decision:` tags.
// The struct is built from the current request and state via inputFn.
//
//	flow.Step("check-customer",
//	    flow.ValidateFrom(validator, "is-pj",
//	        func(req *server.Request, s *flow.State) any {
//	            var c Customer
//	            s.Bind("load-customer", &c)
//	            return c
//	        }))
func ValidateFrom(
	d *decision.Block,
	ruleName string,
	inputFn func(req *server.Request, state *State) any,
	opts ...ValidateOption,
) StepFn {
	cfg := validateConfig{
		failStatus: http.StatusUnprocessableEntity,
		failMessage: func(rule string) string {
			return fmt.Sprintf("validation failed: rule %q not satisfied", rule)
		},
	}
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx context.Context, req *server.Request, state *State) error {
		input := inputFn(req, state)
		ok, err := d.EvaluateFrom(ctx, ruleName, input)
		if err != nil {
			return fmt.Errorf("validateFrom %q: %w", ruleName, err)
		}
		if !ok {
			state.Abort(server.Error(cfg.failStatus, cfg.failMessage(ruleName)))
		}
		return nil
	}
}

// ── Enrich ────────────────────────────────────────────────────────────────────

// Enrich calls an external source (database, REST API, cache, etc.) and stores
// the result in state under the step name. Subsequent steps retrieve it via
// state.Get("step-name") or state.Bind("step-name", &dest).
//
// If fn returns an error, the flow aborts with HTTP 500.
// If fn returns nil as the value, nothing is stored (step is a no-op).
//
//	flow.Step("load-user",
//	    flow.Enrich(func(ctx context.Context, req *server.Request, _ *flow.State) (any, error) {
//	        return usersDB.GetItem(ctx, req.PathParam("id"), nil)
//	    }))
//
// The result is stored under "load-user" and retrieved downstream:
//
//	var u User
//	state.Bind("load-user", &u)
func Enrich(fn func(ctx context.Context, req *server.Request, state *State) (any, error)) StepFn {
	// The step name is injected by the Step wrapper at the Block level.
	// We store under a sentinel and rename during registration.
	// Since StepFn doesn't know its own name, Enrich uses the stepName
	// captured in the closure at Step() call time via a named variant.
	// The Block.execute loop handles storage automatically via EnrichNamed.
	return func(ctx context.Context, req *server.Request, state *State) error {
		// This path is used when Enrich is called directly as a StepFn.
		// Storage key = "__enrich__" will be renamed by the Step wrapper.
		val, err := fn(ctx, req, state)
		if err != nil {
			return err
		}
		if val != nil {
			state.Set("__enrich__", val)
		}
		return nil
	}
}

// enrichStep wraps Enrich with the step name so the result is stored correctly.
// Called internally by Step() when the fn is an enrich function.
func enrichStep(name string, fn func(ctx context.Context, req *server.Request, state *State) (any, error)) StepFn {
	return func(ctx context.Context, req *server.Request, state *State) error {
		val, err := fn(ctx, req, state)
		if err != nil {
			return err
		}
		if val != nil {
			state.Set(name, val)
		}
		return nil
	}
}

// EnrichStep creates a Step that fetches data and stores it under name.
// This is the preferred form — it captures the step name automatically.
//
//	flow.EnrichStep("load-user", usersDB, func(ctx, req, state) (any, error) {
//	    return usersDB.GetItem(ctx, req.PathParam("id"), nil)
//	})
func EnrichStep(name string, fn func(ctx context.Context, req *server.Request, state *State) (any, error)) Step {
	return Step{name: name, fn: enrichStep(name, fn)}
}

// ── Decide ────────────────────────────────────────────────────────────────────

// Decide runs all CEL rules in the decision block and stores the *decision.Result
// in state under the step name. Unlike Validate, a failing rule does NOT abort
// the flow — it is recorded for later steps to inspect.
//
//	flow.Step("route-decision",
//	    flow.Decide(router, func(req *server.Request, s *flow.State) map[string]any {
//	        var customer Customer
//	        s.Bind("load-customer", &customer)
//	        return map[string]any{"customer_type": customer.Type, "amount": customer.Total}
//	    }))
//
// In a later step:
//
//	if state.Passed("route-decision", "is-pj") { ... }
//	if state.Decision("route-decision").Any() { ... }
func Decide(
	d *decision.Block,
	inputFn func(req *server.Request, state *State) map[string]any,
) StepFn {
	return func(ctx context.Context, req *server.Request, state *State) error {
		// stepName is injected by decideStep
		inputs := inputFn(req, state)
		result, err := d.EvaluateAll(ctx, inputs)
		if err != nil {
			return fmt.Errorf("decide: %w", err)
		}
		state.setDecision("__decide__", result)
		return nil
	}
}

// decideStep wraps Decide with the step name for proper state storage.
func decideStep(name string, d *decision.Block, inputFn func(req *server.Request, state *State) map[string]any) StepFn {
	return func(ctx context.Context, req *server.Request, state *State) error {
		inputs := inputFn(req, state)
		result, err := d.EvaluateAll(ctx, inputs)
		if err != nil {
			return fmt.Errorf("decide %q: %w", name, err)
		}
		state.setDecision(name, result)
		return nil
	}
}

// DecideStep creates a Step that evaluates all CEL rules and stores the result.
// This is the preferred form — it captures the step name automatically.
//
//	flow.DecideStep("route-decision", router, func(req, state) map[string]any {
//	    return map[string]any{"customer_type": req.Header("X-Type")}
//	})
func DecideStep(name string, d *decision.Block, inputFn func(req *server.Request, state *State) map[string]any) Step {
	return Step{name: name, fn: decideStep(name, d, inputFn)}
}

// DecideFrom runs all CEL rules against a struct with `decision:` tags.
// The result is stored under the step name.
func DecideFrom(
	d *decision.Block,
	inputFn func(req *server.Request, state *State) any,
) StepFn {
	return func(ctx context.Context, req *server.Request, state *State) error {
		input := inputFn(req, state)
		result, err := d.EvaluateAllFrom(ctx, input)
		if err != nil {
			return fmt.Errorf("decideFrom: %w", err)
		}
		state.setDecision("__decide__", result)
		return nil
	}
}

// DecideFromStep creates a Step that evaluates rules from a struct.
func DecideFromStep(name string, d *decision.Block, inputFn func(req *server.Request, state *State) any) Step {
	return Step{
		name: name,
		fn: func(ctx context.Context, req *server.Request, state *State) error {
			input := inputFn(req, state)
			result, err := d.EvaluateAllFrom(ctx, input)
			if err != nil {
				return fmt.Errorf("decideFrom %q: %w", name, err)
			}
			state.setDecision(name, result)
			return nil
		},
	}
}

// ── Transform ─────────────────────────────────────────────────────────────────

// Transform applies a pure data transformation to state without making any
// external calls. Use it to map, rename, filter or compute values from
// previously enriched data before building the response.
//
//	flow.Step("build-payload",
//	    flow.Transform(func(ctx context.Context, req *server.Request, s *flow.State) error {
//	        var user User
//	        s.Bind("load-user", &user)
//	        var order Order
//	        s.Bind("load-order", &order)
//	        s.Set("payload", map[string]any{
//	            "user_name":    user.Name,
//	            "order_total":  order.Total,
//	            "order_status": order.Status,
//	        })
//	        return nil
//	    }))
func Transform(fn func(ctx context.Context, req *server.Request, state *State) error) StepFn {
	return func(ctx context.Context, req *server.Request, state *State) error {
		return fn(ctx, req, state)
	}
}

// ── Respond ───────────────────────────────────────────────────────────────────

// Respond builds and sets the final HTTP response for the flow.
// After a Respond step, subsequent steps still run but their ability to
// change the response depends on them calling state.Respond again.
// Use state.Abort if you want to stop all further processing.
//
//	flow.Step("send-response",
//	    flow.Respond(func(ctx context.Context, req *server.Request, s *flow.State) (*server.Response, error) {
//	        var payload map[string]any
//	        s.Bind("payload", &payload)
//	        return server.JSON(http.StatusOK, payload), nil
//	    }))
func Respond(fn func(ctx context.Context, req *server.Request, state *State) (*server.Response, error)) StepFn {
	return func(ctx context.Context, req *server.Request, state *State) error {
		resp, err := fn(ctx, req, state)
		if err != nil {
			return err
		}
		if resp != nil {
			state.Respond(resp)
		}
		return nil
	}
}

// ── AbortIf ───────────────────────────────────────────────────────────────────

// AbortIf conditionally aborts the flow based on the current state.
// If condition returns true, the response from respFn is set and the flow stops.
// No-op when condition returns false.
//
//	// Abort if the decision said "not approved"
//	flow.Step("gate-approval",
//	    flow.AbortIf(
//	        func(s *flow.State) bool { return s.Failed("check-limit", "approved") },
//	        func(s *flow.State) *server.Response {
//	            return server.Error(http.StatusForbidden, "credit limit not approved")
//	        },
//	    ))
func AbortIf(
	condition func(state *State) bool,
	respFn func(state *State) *server.Response,
) StepFn {
	return func(_ context.Context, _ *server.Request, state *State) error {
		if condition(state) {
			state.Abort(respFn(state))
		}
		return nil
	}
}
