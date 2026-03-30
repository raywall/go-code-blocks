// blocks/server/types.go
// Package server provides inbound integration blocks that receive requests
// from AWS API Gateway (v1/v2), Application Load Balancer, or as a standalone
// HTTP server. All three transports normalize incoming events into the same
// *Request type and serialize *Response back to the appropriate wire format.
//
// A single Handler function works unchanged across all three transports:
//
//	handler := func(ctx context.Context, req *server.Request) (*server.Response, error) {
//	    return server.JSON(200, map[string]string{"hello": req.PathParam("id")}), nil
//	}
//
//	// Run as HTTP server
//	http := server.NewHTTP("api", server.WithPort(8080), server.WithRouter(router))
//
//	// Run as Lambda (API Gateway v2 / ALB)
//	fn := server.NewLambda("api", server.WithSource(server.SourceAPIGatewayV2), server.WithRouter(router))
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ── Source ────────────────────────────────────────────────────────────────────

// Source identifies the transport that delivered a request.
type Source string

const (
	// SourceHTTP identifies a request received directly via the HTTP server block.
	SourceHTTP Source = "http"
	// SourceAPIGatewayV1 identifies an AWS API Gateway REST API (v1) event.
	SourceAPIGatewayV1 Source = "apigateway_v1"
	// SourceAPIGatewayV2 identifies an AWS API Gateway HTTP API (v2) event.
	SourceAPIGatewayV2 Source = "apigateway_v2"
	// SourceALB identifies an AWS Application Load Balancer target group event.
	SourceALB Source = "alb"
)

// ── Request ───────────────────────────────────────────────────────────────────

// Request is the unified, transport-agnostic representation of an incoming
// HTTP request. It is populated identically regardless of whether the request
// arrived via API Gateway, ALB, or a direct HTTP connection.
type Request struct {
	// Method is the HTTP verb (GET, POST, PUT, PATCH, DELETE, …).
	Method string
	// Path is the raw URL path (e.g. "/users/123").
	Path string
	// PathParams holds named path parameters extracted by the Router.
	// For the path pattern "/users/:id", PathParams["id"] == "123".
	PathParams map[string]string
	// Query holds the parsed query string parameters, potentially multi-valued.
	Query map[string][]string
	// Headers holds the request headers, normalised to lowercase keys.
	Headers map[string]string
	// Body is the raw request body bytes.
	Body []byte
	// SourceIP is the originating client IP address.
	SourceIP string
	// RequestID is a unique identifier for this request (from X-Request-Id,
	// API Gateway requestId, or ALB traceId).
	RequestID string
	// Stage is the API Gateway deployment stage (e.g. "prod"). Empty for HTTP.
	Stage string
	// Source identifies the transport that delivered this request.
	Source Source
	// Raw holds the original transport-specific event for advanced use cases.
	// For HTTP it is *http.Request; for Lambda it is the raw events struct.
	Raw any
}

// PathParam returns the value of the named path parameter, or empty string.
func (r *Request) PathParam(name string) string {
	if r.PathParams == nil {
		return ""
	}
	return r.PathParams[name]
}

// QueryParam returns the first value of the named query parameter, or empty string.
func (r *Request) QueryParam(name string) string {
	if r.Query == nil {
		return ""
	}
	vals := r.Query[name]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// Header returns the value of the named header (case-insensitive), or empty string.
func (r *Request) Header(name string) string {
	if r.Headers == nil {
		return ""
	}
	return r.Headers[http.CanonicalHeaderKey(name)]
}

// BindJSON unmarshals the request body into v.
func (r *Request) BindJSON(v any) error {
	if len(r.Body) == 0 {
		return fmt.Errorf("server: request body is empty")
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("server: bind json: %w", err)
	}
	return nil
}

// ── Response ──────────────────────────────────────────────────────────────────

// Response is the unified outgoing response. The server block serializes it
// to the appropriate wire format for the active transport.
type Response struct {
	// StatusCode is the HTTP status code.
	StatusCode int
	// Headers are added to the response alongside any transport defaults.
	Headers map[string]string
	// Body is the response payload.
	// string and []byte are sent as-is; anything else is JSON-marshalled.
	Body any
	// IsBase64 signals that Body is a base64-encoded binary payload.
	// Only relevant for Lambda transports.
	IsBase64 bool
}

// ── Response constructors ─────────────────────────────────────────────────────

// JSON returns a Response with the given status code and a JSON-marshalled body.
// Content-Type is set to application/json automatically.
func JSON(statusCode int, body any) *Response {
	return &Response{
		StatusCode: statusCode,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
}

// Text returns a Response with the given status code and a plain-text body.
func Text(statusCode int, body string) *Response {
	return &Response{
		StatusCode: statusCode,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "text/plain; charset=utf-8"},
	}
}

// Error returns a JSON error response with a standard {"error": message} body.
func Error(statusCode int, message string) *Response {
	return JSON(statusCode, map[string]string{"error": message})
}

// NoContent returns a 204 No Content response with no body.
func NoContent() *Response {
	return &Response{StatusCode: http.StatusNoContent}
}

// Redirect returns a 301 or 302 redirect response to the target URL.
func Redirect(statusCode int, url string) *Response {
	return &Response{
		StatusCode: statusCode,
		Headers:    map[string]string{"Location": url},
	}
}

// ── Handler & Middleware ──────────────────────────────────────────────────────

// Handler is the universal request handler. Its signature is identical
// regardless of transport — API Gateway, ALB, or direct HTTP.
type Handler func(ctx context.Context, req *Request) (*Response, error)

// Middleware wraps a Handler to form a processing chain.
// The pattern is identical to standard http.Handler middleware:
//
//	logging := func(next server.Handler) server.Handler {
//	    return func(ctx context.Context, req *server.Request) (*server.Response, error) {
//	        start := time.Now()
//	        resp, err := next(ctx, req)
//	        slog.Info("request", "method", req.Method, "path", req.Path,
//	            "status", resp.StatusCode, "latency", time.Since(start))
//	        return resp, err
//	    }
//	}
type Middleware func(next Handler) Handler

// chain applies middleware in declaration order, so the first middleware
// declared is the outermost (first to run, last to return).
func chain(h Handler, middleware ...Middleware) Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
