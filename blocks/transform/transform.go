// blocks/transform/transform.go
//
// Package transform provides declarative data mapping utilities for use inside
// flow steps. It eliminates the boilerplate of manual Bind + map[string]any
// construction when combining data from multiple enrichment sources, request
// fields, and computed values into a single payload.
//
// All functions are pure — no I/O, no lifecycle, no core.Block registration.
// They are consumed directly inside flow.Transform and flow.Respond steps.
//
//	// Inside a flow.Transform step:
//	flow.NewStep("build-payload",
//	    flow.Transform(func(ctx context.Context, req *server.Request, s *flow.State) error {
//	        payload, err := transform.New(s, req).
//	            AllFrom("load-customer").               // spread all fields from state["load-customer"]
//	            Pick("load-credit", "available").       // pick one field from state["load-credit"]
//	            PathParam("order_id", "id").             // from URL :id
//	            Compute("status", func(s *flow.State, _ *server.Request) any {
//	                return "pending"
//	            }).
//	            Build()
//	        if err != nil { return err }
//	        s.Set("payload", payload)
//	        return nil
//	    }))
package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/raywall/go-code-blocks/blocks/flow"
	"github.com/raywall/go-code-blocks/blocks/server"
)

// ── Standalone functions ──────────────────────────────────────────────────────

// Merge combines the values of multiple state keys into a single flat map.
// Keys from later sources overwrite keys from earlier ones on collision.
//
//	merged, err := transform.Merge(state, "load-customer", "load-credit")
func Merge(state *flow.State, keys ...string) (map[string]any, error) {
	out := make(map[string]any)
	for _, key := range keys {
		m, err := toMap(state.Get(key), key)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			out[k] = v
		}
	}
	return out, nil
}

// Pick extracts only the specified fields from the value stored in state under key.
//
//	picked, err := transform.Pick(state, "load-customer", "id", "name", "tax_id")
func Pick(state *flow.State, key string, fields ...string) (map[string]any, error) {
	m, err := toMap(state.Get(key), key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := m[f]; ok {
			out[f] = v
		}
	}
	return out, nil
}

// Omit returns all fields of the value stored under key, excluding the specified ones.
//
//	public, err := transform.Omit(state, "load-customer", "password", "internal_id")
func Omit(state *flow.State, key string, fields ...string) (map[string]any, error) {
	m, err := toMap(state.Get(key), key)
	if err != nil {
		return nil, err
	}
	exclude := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		exclude[f] = struct{}{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, skip := exclude[k]; !skip {
			out[k] = v
		}
	}
	return out, nil
}

// Rename returns all fields of the value stored under key, renaming fields
// according to the mapping (oldName → newName). Unmapped fields keep their names.
//
//	renamed, err := transform.Rename(state, "load-customer",
//	    map[string]string{"customer_type": "type", "tax_id": "cnpj"})
func Rename(state *flow.State, key string, mapping map[string]string) (map[string]any, error) {
	m, err := toMap(state.Get(key), key)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if newKey, ok := mapping[k]; ok {
			out[newKey] = v
		} else {
			out[k] = v
		}
	}
	return out, nil
}

// ── Builder ───────────────────────────────────────────────────────────────────

// Builder provides a fluent API for composing a map from multiple sources:
// state values, request parameters and computed values.
//
// Instantiate with New, chain methods, and call Build or MustBuild at the end.
type Builder struct {
	state  *flow.State
	req    *server.Request
	data   map[string]any
	errors []error
}

// New creates a Builder bound to the given state and request.
// Both are optional — pass nil for either if not needed.
func New(state *flow.State, req *server.Request) *Builder {
	return &Builder{
		state: state,
		req:   req,
		data:  make(map[string]any),
	}
}

// AllFrom spreads all fields of state[key] into the output map.
// Existing keys are overwritten on collision.
//
//	.AllFrom("load-customer")   // adds id, name, email, type, ...
func (b *Builder) AllFrom(stateKey string) *Builder {
	if b.state == nil {
		return b
	}
	m, err := toMap(b.state.Get(stateKey), stateKey)
	if err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	for k, v := range m {
		b.data[k] = v
	}
	return b
}

// Pick copies only the specified fields from state[stateKey] into the output.
//
//	.Pick("load-customer", "id", "name")
func (b *Builder) Pick(stateKey string, fields ...string) *Builder {
	if b.state == nil {
		return b
	}
	m, err := toMap(b.state.Get(stateKey), stateKey)
	if err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	for _, f := range fields {
		if v, ok := m[f]; ok {
			b.data[f] = v
		}
	}
	return b
}

// Omit spreads all fields of state[stateKey] except the excluded ones.
//
//	.Omit("load-customer", "internal_code", "raw_password")
func (b *Builder) Omit(stateKey string, excluded ...string) *Builder {
	if b.state == nil {
		return b
	}
	excl := make(map[string]struct{}, len(excluded))
	for _, e := range excluded {
		excl[e] = struct{}{}
	}
	m, err := toMap(b.state.Get(stateKey), stateKey)
	if err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	for k, v := range m {
		if _, skip := excl[k]; !skip {
			b.data[k] = v
		}
	}
	return b
}

// Field copies one field from state[stateKey] into the output under destKey.
// Useful for renaming or promoting nested values.
//
//	.Field("customer_name", "load-customer", "name")
//	.Field("credit",        "load-credit",   "available")
func (b *Builder) Field(destKey, stateKey, srcField string) *Builder {
	if b.state == nil {
		return b
	}
	m, err := toMap(b.state.Get(stateKey), stateKey)
	if err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	if v, ok := m[srcField]; ok {
		b.data[destKey] = v
	}
	return b
}

// Set adds a static key-value pair to the output.
//
//	.Set("status", "pending")
//	.Set("version", 2)
func (b *Builder) Set(key string, value any) *Builder {
	b.data[key] = value
	return b
}

// Compute derives a value from state and request via a function.
//
//	.Compute("total_with_tax", func(s *flow.State, _ *server.Request) any {
//	    var body OrderRequest
//	    s.Bind("__body__", &body)
//	    return body.Amount * 1.12
//	})
func (b *Builder) Compute(destKey string, fn func(*flow.State, *server.Request) any) *Builder {
	b.data[destKey] = fn(b.state, b.req)
	return b
}

// PathParam copies a URL path parameter (from :param) into the output.
//
//	.PathParam("order_id", "id")   // copies req.PathParam("id") → output["order_id"]
func (b *Builder) PathParam(destKey, paramName string) *Builder {
	if b.req != nil {
		b.data[destKey] = b.req.PathParam(paramName)
	}
	return b
}

// QueryParam copies a URL query string parameter into the output.
//
//	.QueryParam("page", "page")    // copies ?page=2 → output["page"]
func (b *Builder) QueryParam(destKey, paramName string) *Builder {
	if b.req != nil {
		b.data[destKey] = b.req.QueryParam(paramName)
	}
	return b
}

// Header copies a request header value into the output.
//
//	.Header("trace_id", "X-Request-Id")
func (b *Builder) Header(destKey, headerName string) *Builder {
	if b.req != nil {
		b.data[destKey] = b.req.Header(headerName)
	}
	return b
}

// Build returns the assembled map, or an aggregated error if any step failed.
func (b *Builder) Build() (map[string]any, error) {
	if len(b.errors) > 0 {
		msgs := make([]string, len(b.errors))
		for i, e := range b.errors {
			msgs[i] = e.Error()
		}
		return nil, fmt.Errorf("transform: %s", strings.Join(msgs, "; "))
	}
	return b.data, nil
}

// MustBuild returns the assembled map and panics on error.
// Use only in tests or when errors are structurally impossible.
func (b *Builder) MustBuild() map[string]any {
	m, err := b.Build()
	if err != nil {
		panic(err)
	}
	return m
}

// ── Step constructor ──────────────────────────────────────────────────────────

// Step returns a flow.StepFn that runs the builder function, stores the result
// in state under destKey, and passes any error back to the flow engine.
//
//	flow.NewStep("build-payload",
//	    transform.Step("payload", func(b *transform.Builder) *transform.Builder {
//	        return b.
//	            AllFrom("load-customer").
//	            Pick("load-credit", "available").
//	            PathParam("order_id", "id").
//	            Set("status", "pending")
//	    }))
func Step(destKey string, fn func(*Builder) *Builder) flow.StepFn {
	return func(ctx context.Context, req *server.Request, state *flow.State) error {
		b := fn(New(state, req))
		result, err := b.Build()
		if err != nil {
			return fmt.Errorf("transform step %q: %w", destKey, err)
		}
		state.Set(destKey, result)
		return nil
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// toMap converts any value to map[string]any via JSON roundtrip.
func toMap(v any, key string) (map[string]any, error) {
	if v == nil {
		return map[string]any{}, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("transform: marshal %q: %w", key, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("transform: unmarshal %q: %w", key, err)
	}
	return m, nil
}
