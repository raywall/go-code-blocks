// blocks/output/output.go
//
// Package output provides declarative constructors for flow step responses and
// outbound REST API payloads. It bridges the flow pipeline with the server and
// restapi blocks without requiring the user to write manual JSON marshalling or
// request construction in every step.
//
// All functions are pure utilities — no core.Block registration required.
//
// # Response constructors
//
// Used as the fn argument to flow.NewStep when building the final HTTP response:
//
//	flow.NewStep("respond", output.JSON(http.StatusCreated, "payload"))
//	flow.NewStep("respond", output.JSONFrom(http.StatusOK, func(s *flow.State, _ *server.Request) any {
//	    var u User
//	    s.Bind("load-user", &u)
//	    return u
//	}))
//
// # REST payload builder
//
// Used inside flow.EnrichStep to call a downstream REST API using data from
// the current state and request:
//
//	flow.EnrichStep("call-downstream",
//	    output.Call(downstreamAPI,
//	        output.REST("POST", "/orders").
//	            BodyFromState("payload").
//	            HeaderFromRequest("X-Trace-Id"),
//	    ))
package output

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/raywall/go-code-blocks/blocks/flow"
	"github.com/raywall/go-code-blocks/blocks/restapi"
	"github.com/raywall/go-code-blocks/blocks/server"
)

// ── Response constructors ─────────────────────────────────────────────────────

// JSON returns a StepFn that reads the value at stateKey and responds with it
// JSON-encoded at the given status code.
//
//	flow.NewStep("respond", output.JSON(http.StatusOK, "payload"))
func JSON(statusCode int, stateKey string) flow.StepFn {
	return func(_ context.Context, _ *server.Request, state *flow.State) error {
		state.Respond(server.JSON(statusCode, state.Get(stateKey)))
		return nil
	}
}

// JSONFrom returns a StepFn that calls fn to build the response body.
// Use this when the response body is derived from multiple state keys or
// requires light transformation before serialisation.
//
//	flow.NewStep("respond",
//	    output.JSONFrom(http.StatusOK, func(s *flow.State, req *server.Request) any {
//	        var customer Customer
//	        s.Bind("load-customer", &customer)
//	        return map[string]any{
//	            "id":    customer.ID,
//	            "name":  customer.Name,
//	            "route": req.PathParam("id"),
//	        }
//	    }))
func JSONFrom(statusCode int, fn func(*flow.State, *server.Request) any) flow.StepFn {
	return func(_ context.Context, req *server.Request, state *flow.State) error {
		state.Respond(server.JSON(statusCode, fn(state, req)))
		return nil
	}
}

// Created is a shorthand for JSON(201, stateKey).
//
//	flow.NewStep("respond", output.Created("order"))
func Created(stateKey string) flow.StepFn {
	return JSON(http.StatusCreated, stateKey)
}

// OK is a shorthand for JSON(200, stateKey).
func OK(stateKey string) flow.StepFn {
	return JSON(http.StatusOK, stateKey)
}

// Text returns a StepFn that responds with the string value at stateKey.
func Text(statusCode int, stateKey string) flow.StepFn {
	return func(_ context.Context, _ *server.Request, state *flow.State) error {
		v := state.Get(stateKey)
		s, _ := v.(string)
		state.Respond(server.Text(statusCode, s))
		return nil
	}
}

// NoContent returns a StepFn that responds with HTTP 204 No Content.
//
//	flow.NewStep("respond", output.NoContent())
func NoContent() flow.StepFn {
	return func(_ context.Context, _ *server.Request, state *flow.State) error {
		state.Respond(server.NoContent())
		return nil
	}
}

// Redirect returns a StepFn that responds with an HTTP redirect.
// urlFn receives the current state and request to build the target URL dynamically.
//
//	flow.NewStep("redirect",
//	    output.Redirect(http.StatusFound, func(s *flow.State, req *server.Request) string {
//	        return "/orders/" + req.PathParam("id")
//	    }))
func Redirect(code int, urlFn func(*flow.State, *server.Request) string) flow.StepFn {
	return func(_ context.Context, req *server.Request, state *flow.State) error {
		state.Respond(server.Redirect(code, urlFn(state, req)))
		return nil
	}
}

// ── REST payload builder ──────────────────────────────────────────────────────

// Payload is a fluent builder for restapi.Request objects. It reads from
// the current flow State and server.Request to assemble the outbound call.
type Payload struct {
	method   string
	path     string
	pathVars map[string]payloadSource // placeholder → source
	body     payloadSource
	headers  []headerSource
	queries  []querySource
}

type payloadSource struct {
	kind     string // "state", "static", "fn"
	stateKey string
	static   any
	fn       func(*flow.State, *server.Request) any
}

type headerSource struct {
	key     string
	kind    string // "static", "request", "state", "fn"
	value   string // static or header name
	stateFn func(*flow.State, *server.Request) string
}

type querySource struct {
	key   string
	kind  string
	value string
	fn    func(*flow.State, *server.Request) string
}

// REST creates a new Payload builder for the given HTTP method and path.
// The path may contain {placeholder} segments resolved via PathParam.
//
//	output.REST("POST", "/orders")
//	output.REST("GET",  "/customers/{customer_id}/credit")
func REST(method, path string) *Payload {
	return &Payload{
		method:   strings.ToUpper(method),
		path:     path,
		pathVars: make(map[string]payloadSource),
	}
}

// PathParamFromState resolves a {placeholder} in the path using a field from
// the value stored at stateKey.
//
//	output.REST("GET", "/customers/{tax_id}/credit").
//	    PathParamFromState("tax_id", "load-customer", "tax_id")
func (p *Payload) PathParamFromState(placeholder, stateKey, field string) *Payload {
	p.pathVars[placeholder] = payloadSource{kind: "state-field", stateKey: stateKey, static: field}
	return p
}

// PathParamFromRequest resolves a {placeholder} in the path from a URL path param.
//
//	output.REST("GET", "/customers/{id}").
//	    PathParamFromRequest("id", "customer_id")
func (p *Payload) PathParamFromRequest(placeholder, requestParam string) *Payload {
	p.pathVars[placeholder] = payloadSource{kind: "request", static: requestParam}
	return p
}

// BodyFromState sets the request body to the value stored at stateKey.
//
//	output.REST("POST", "/orders").BodyFromState("payload")
func (p *Payload) BodyFromState(stateKey string) *Payload {
	p.body = payloadSource{kind: "state", stateKey: stateKey}
	return p
}

// Body sets a static request body.
func (p *Payload) Body(value any) *Payload {
	p.body = payloadSource{kind: "static", static: value}
	return p
}

// BodyFrom derives the request body at call time from state and request.
//
//	output.REST("POST", "/orders").
//	    BodyFrom(func(s *flow.State, req *server.Request) any {
//	        var customer Customer
//	        s.Bind("load-customer", &customer)
//	        return map[string]any{"cnpj": customer.TaxID, "amount": req.QueryParam("amount")}
//	    })
func (p *Payload) BodyFrom(fn func(*flow.State, *server.Request) any) *Payload {
	p.body = payloadSource{kind: "fn", fn: fn}
	return p
}

// Header adds a static header to the outbound request.
func (p *Payload) Header(key, value string) *Payload {
	p.headers = append(p.headers, headerSource{key: key, kind: "static", value: value})
	return p
}

// HeaderFromRequest copies a header from the inbound request to the outbound one.
//
//	.HeaderFromRequest("X-Trace-Id")   // forwards X-Trace-Id as-is
func (p *Payload) HeaderFromRequest(headerName string) *Payload {
	p.headers = append(p.headers, headerSource{key: headerName, kind: "request", value: headerName})
	return p
}

// HeaderFromRequestAs copies a header from the inbound request under a new name.
//
//	.HeaderFromRequestAs("X-Correlation-Id", "X-Trace-Id")
func (p *Payload) HeaderFromRequestAs(destKey, srcHeader string) *Payload {
	p.headers = append(p.headers, headerSource{key: destKey, kind: "request", value: srcHeader})
	return p
}

// HeaderFrom computes a header value from state and request.
func (p *Payload) HeaderFrom(key string, fn func(*flow.State, *server.Request) string) *Payload {
	p.headers = append(p.headers, headerSource{key: key, kind: "fn", stateFn: fn})
	return p
}

// Query adds a static query string parameter.
func (p *Payload) Query(key, value string) *Payload {
	p.queries = append(p.queries, querySource{key: key, kind: "static", value: value})
	return p
}

// QueryFrom computes a query parameter from state and request.
func (p *Payload) QueryFrom(key string, fn func(*flow.State, *server.Request) string) *Payload {
	p.queries = append(p.queries, querySource{key: key, kind: "fn", fn: fn})
	return p
}

// Build constructs the restapi.Request using the current state and request.
func (p *Payload) Build(req *server.Request, state *flow.State) (restapi.Request, error) {
	// ── Resolve path ──────────────────────────────────────────────────────────
	resolvedPath := p.path
	for placeholder, src := range p.pathVars {
		var val string
		switch src.kind {
		case "state-field":
			m, err := stateToMap(state, src.stateKey)
			if err != nil {
				return restapi.Request{}, fmt.Errorf("output: path param %q: %w", placeholder, err)
			}
			v, _ := m[src.static.(string)]
			val = fmt.Sprintf("%v", v)
		case "request":
			val = req.PathParam(src.static.(string))
		}
		resolvedPath = strings.ReplaceAll(resolvedPath, "{"+placeholder+"}", val)
	}

	// ── Resolve body ──────────────────────────────────────────────────────────
	var body any
	switch p.body.kind {
	case "state":
		body = state.Get(p.body.stateKey)
	case "static":
		body = p.body.static
	case "fn":
		body = p.body.fn(state, req)
	}

	// ── Resolve headers ───────────────────────────────────────────────────────
	headers := make(map[string]string, len(p.headers))
	for _, h := range p.headers {
		switch h.kind {
		case "static":
			headers[h.key] = h.value
		case "request":
			headers[h.key] = req.Header(h.value)
		case "fn":
			headers[h.key] = h.stateFn(state, req)
		}
	}

	// ── Resolve query params ──────────────────────────────────────────────────
	query := make(map[string]string, len(p.queries))
	for _, q := range p.queries {
		switch q.kind {
		case "static":
			query[q.key] = q.value
		case "fn":
			query[q.key] = q.fn(state, req)
		}
	}

	return restapi.Request{
		Method:  p.method,
		Path:    resolvedPath,
		Body:    body,
		Headers: headers,
		Query:   query,
	}, nil
}

// ── Call — EnrichStep that executes an outbound REST call ────────────────────

// Call returns a flow.EnrichStep function that builds the REST request using
// the Payload builder, calls the API, and stores the raw response bytes in
// state under the step name.
//
// The result is a *restapi.Response. To deserialise it, use state.Bind in the
// next step, or use CallJSON which deserialises directly.
//
//	flow.EnrichStep("call-credit",
//	    output.Call(creditAPI,
//	        output.REST("GET", "/credit/{tax_id}").
//	            PathParamFromState("tax_id", "load-customer", "tax_id").
//	            HeaderFromRequest("X-Trace-Id"),
//	    ))
func Call(api *restapi.Block, payload *Payload) func(context.Context, *server.Request, *flow.State) (any, error) {
	return func(ctx context.Context, req *server.Request, state *flow.State) (any, error) {
		r, err := payload.Build(req, state)
		if err != nil {
			return nil, fmt.Errorf("output.Call: build payload: %w", err)
		}
		resp, err := api.Do(ctx, r)
		if err != nil {
			return nil, fmt.Errorf("output.Call %s %s: %w", r.Method, r.Path, err)
		}
		if !resp.OK() {
			return nil, fmt.Errorf("output.Call %s %s: HTTP %d", r.Method, r.Path, resp.StatusCode)
		}
		return resp, nil
	}
}

// CallJSON is like Call but immediately deserialises the response body into
// dest (which should be a pointer to a struct or map). The deserialised value
// is what gets stored in state.
//
//	var credit CreditResponse
//	flow.EnrichStep("load-credit",
//	    output.CallJSON(creditAPI, &credit,
//	        output.REST("GET", "/credit/{tax_id}").
//	            PathParamFromState("tax_id", "load-customer", "tax_id"),
//	    ))
func CallJSON(api *restapi.Block, dest any, payload *Payload) func(context.Context, *server.Request, *flow.State) (any, error) {
	return func(ctx context.Context, req *server.Request, state *flow.State) (any, error) {
		r, err := payload.Build(req, state)
		if err != nil {
			return nil, fmt.Errorf("output.CallJSON: build payload: %w", err)
		}

		switch strings.ToUpper(r.Method) {
		case "GET":
			err = api.GetJSON(ctx, r.Path, r.Query, dest)
		case "POST":
			err = api.PostJSON(ctx, r.Path, r.Body, dest)
		case "PUT":
			err = api.PutJSON(ctx, r.Path, r.Body, dest)
		case "PATCH":
			err = api.PatchJSON(ctx, r.Path, r.Body, dest)
		default:
			resp, doErr := api.Do(ctx, r)
			if doErr != nil {
				return nil, fmt.Errorf("output.CallJSON %s %s: %w", r.Method, r.Path, doErr)
			}
			if !resp.OK() {
				return nil, fmt.Errorf("output.CallJSON %s %s: HTTP %d", r.Method, r.Path, resp.StatusCode)
			}
			return dest, nil
		}

		if err != nil {
			return nil, fmt.Errorf("output.CallJSON %s %s: %w", r.Method, r.Path, err)
		}
		return dest, nil
	}
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func stateToMap(state *flow.State, key string) (map[string]any, error) {
	v := state.Get(key)
	if v == nil {
		return map[string]any{}, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, fmt.Errorf("output: state[%q] is not a map", key)
}
