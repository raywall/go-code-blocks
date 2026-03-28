package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/raywall/go-code-blocks/core"
)

// ── Typed helpers ─────────────────────────────────────────────────────────────

// Get performs a GET request to the given path.
//
//	resp, err := api.Get(ctx, "/users/123", map[string]string{"expand": "roles"})
func (b *Block) Get(ctx context.Context, path string, query map[string]string) (*Response, error) {
	return b.Do(ctx, Request{Method: http.MethodGet, Path: path, Query: query})
}

// Post performs a POST request, JSON-encoding body automatically.
//
//	resp, err := api.Post(ctx, "/users", map[string]any{"name": "Alice"})
func (b *Block) Post(ctx context.Context, path string, body any) (*Response, error) {
	return b.Do(ctx, Request{Method: http.MethodPost, Path: path, Body: body})
}

// Put performs a PUT request, JSON-encoding body automatically.
func (b *Block) Put(ctx context.Context, path string, body any) (*Response, error) {
	return b.Do(ctx, Request{Method: http.MethodPut, Path: path, Body: body})
}

// Patch performs a PATCH request, JSON-encoding body automatically.
func (b *Block) Patch(ctx context.Context, path string, body any) (*Response, error) {
	return b.Do(ctx, Request{Method: http.MethodPatch, Path: path, Body: body})
}

// Delete performs a DELETE request.
//
//	resp, err := api.Delete(ctx, "/users/123")
func (b *Block) Delete(ctx context.Context, path string) (*Response, error) {
	return b.Do(ctx, Request{Method: http.MethodDelete, Path: path})
}

// Head performs a HEAD request.
func (b *Block) Head(ctx context.Context, path string) (*Response, error) {
	return b.Do(ctx, Request{Method: http.MethodHead, Path: path})
}

// ── JSON convenience wrappers ─────────────────────────────────────────────────

// GetJSON performs a GET and unmarshals the response body into out.
//
//	var user User
//	err := api.GetJSON(ctx, "/users/123", nil, &user)
func (b *Block) GetJSON(ctx context.Context, path string, query map[string]string, out any) error {
	resp, err := b.Get(ctx, path, query)
	if err != nil {
		return err
	}
	return b.unmarshal(resp, out)
}

// PostJSON performs a POST and unmarshals the response body into out.
//
//	var created User
//	err := api.PostJSON(ctx, "/users", payload, &created)
func (b *Block) PostJSON(ctx context.Context, path string, body, out any) error {
	resp, err := b.Post(ctx, path, body)
	if err != nil {
		return err
	}
	return b.unmarshal(resp, out)
}

// PutJSON performs a PUT and unmarshals the response body into out.
func (b *Block) PutJSON(ctx context.Context, path string, body, out any) error {
	resp, err := b.Put(ctx, path, body)
	if err != nil {
		return err
	}
	return b.unmarshal(resp, out)
}

// PatchJSON performs a PATCH and unmarshals the response body into out.
func (b *Block) PatchJSON(ctx context.Context, path string, body, out any) error {
	resp, err := b.Patch(ctx, path, body)
	if err != nil {
		return err
	}
	return b.unmarshal(resp, out)
}

// ── Core dispatcher ───────────────────────────────────────────────────────────

// Do executes a fully described HTTP Request and returns the raw Response.
// It handles URL construction, body encoding, default + per-request headers,
// authentication, and response body reading.
//
// Body encoding rules:
//   - nil        → no body
//   - string     → sent as-is
//   - []byte     → sent as-is
//   - io.Reader  → streamed as-is
//   - anything else → JSON-marshalled; Content-Type set to application/json
func (b *Block) Do(ctx context.Context, r Request) (*Response, error) {
	if err := b.checkInit(); err != nil {
		return nil, err
	}

	// ── Build URL ─────────────────────────────────────────────────────────────
	rawURL := joinURL(b.cfg.baseURL, r.Path)
	if len(r.Query) > 0 {
		rawURL += "?" + encodeQuery(r.Query)
	}

	// ── Build body ────────────────────────────────────────────────────────────
	bodyReader, contentType, err := encodeBody(r.Body, r.ContentType)
	if err != nil {
		return nil, fmt.Errorf("restapi %q: encode body: %w", b.name, err)
	}

	method := r.Method
	if method == "" {
		method = http.MethodGet
	}

	// ── Build http.Request ────────────────────────────────────────────────────
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("restapi %q: build request: %w", b.name, err)
	}

	// Apply default headers first, then per-request headers (override).
	for k, v := range b.cfg.defaultHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if contentType != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	// Apply auth strategy (sets Authorization header or query param).
	if b.auth != nil {
		if err := b.auth.apply(ctx, req); err != nil {
			return nil, fmt.Errorf("restapi %q: auth: %w", b.name, err)
		}
	}

	// ── Execute ───────────────────────────────────────────────────────────────
	start := time.Now()
	httpResp, err := b.httpClient.Do(req)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("restapi %q %s %s: %w", b.name, method, rawURL, err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("restapi %q: read response body: %w", b.name, err)
	}

	resp := &Response{
		StatusCode: httpResp.StatusCode,
		Headers:    httpResp.Header,
		Body:       respBody,
		latency:    latency,
	}

	// Surface HTTP-level errors with response body context for easier debugging.
	if !resp.OK() {
		return resp, fmt.Errorf("restapi %q %s %s: HTTP %d: %s",
			b.name, method, r.Path, resp.StatusCode, truncate(string(respBody), 256))
	}

	return resp, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (b *Block) checkInit() error {
	if b.httpClient == nil {
		return fmt.Errorf("restapi %q: %w", b.name, core.ErrNotInitialized)
	}
	return nil
}

func (b *Block) unmarshal(resp *Response, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(resp.Body, out); err != nil {
		return fmt.Errorf("restapi %q: unmarshal response (status=%d): %w",
			b.name, resp.StatusCode, err)
	}
	return nil
}

func joinURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func encodeQuery(q map[string]string) string {
	var parts []string
	for k, v := range q {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "&")
}

func encodeBody(body any, overrideContentType string) (io.Reader, string, error) {
	if body == nil {
		return nil, "", nil
	}
	ct := overrideContentType
	switch v := body.(type) {
	case string:
		if ct == "" {
			ct = "text/plain; charset=utf-8"
		}
		return strings.NewReader(v), ct, nil
	case []byte:
		return bytes.NewReader(v), ct, nil
	case io.Reader:
		return v, ct, nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, "", err
		}
		if ct == "" {
			ct = "application/json"
		}
		return bytes.NewReader(data), ct, nil
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
