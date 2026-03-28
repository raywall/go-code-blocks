package restapi

import (
	"net/http"
	"time"
)

// Block is a REST API integration block.
// It holds a configured *http.Client, base URL, default headers, and an
// optional auth strategy. All HTTP operations are performed through Do or the
// typed helpers (Get, Post, Put, Patch, Delete).
type Block struct {
	name       string
	cfg        blockConfig
	httpClient *http.Client
	auth       authApplier // nil when no auth is configured
}

// ── Request / Response ────────────────────────────────────────────────────────

// Request describes a single outgoing HTTP call.
// Fields left as zero values use the block's defaults.
type Request struct {
	// Method is the HTTP verb (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS).
	// Defaults to GET when empty.
	Method string
	// Path is appended to the block's BaseURL.
	// It may include path parameters: "/users/{id}".
	Path string
	// Query is serialised into the URL query string.
	Query map[string]string
	// Headers are merged over the block's default headers.
	// Per-request headers take precedence.
	Headers map[string]string
	// Body is the request body. Pass nil for GET/DELETE/HEAD.
	// Strings, []byte, and io.Reader are accepted; anything else is
	// JSON-marshalled automatically.
	Body any
	// ContentType overrides "Content-Type" for this request.
	// Defaults to "application/json" when Body is non-nil.
	ContentType string
}

// Response is the parsed result of an HTTP call.
type Response struct {
	// StatusCode is the HTTP status code (200, 201, 404, …).
	StatusCode int
	// Headers contains the response headers.
	Headers http.Header
	// Body holds the raw response bytes.
	Body []byte
	// latency is recorded internally for observability.
	latency time.Duration
}

// OK reports whether the response status is in the 2xx range.
func (r *Response) OK() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}

// Latency returns the round-trip duration of the request.
func (r *Response) Latency() time.Duration { return r.latency }
