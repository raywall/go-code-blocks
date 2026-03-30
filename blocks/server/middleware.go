// blocks/server/middleware.go
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// ── Logging ───────────────────────────────────────────────────────────────────

// Logging returns a Middleware that logs each request using slog.
// It records method, path, status code, and latency at Info level.
// Errors returned by the handler are logged at Error level.
func Logging() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			start := time.Now()
			resp, err := next(ctx, req)

			status := 0
			if resp != nil {
				status = resp.StatusCode
			}

			attrs := []any{
				"method", req.Method,
				"path", req.Path,
				"status", status,
				"latency", time.Since(start).Round(time.Millisecond),
				"source", string(req.Source),
			}
			if req.RequestID != "" {
				attrs = append(attrs, "request_id", req.RequestID)
			}

			if err != nil {
				slog.ErrorContext(ctx, "request error", append(attrs, "err", err)...)
			} else {
				slog.InfoContext(ctx, "request", attrs...)
			}

			return resp, err
		}
	}
}

// ── Recovery ──────────────────────────────────────────────────────────────────

// Recovery returns a Middleware that catches panics and converts them into
// HTTP 500 responses, preventing the entire process from crashing.
func Recovery() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *Request) (resp *Response, err error) {
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					slog.ErrorContext(ctx, "panic recovered",
						"panic", fmt.Sprintf("%v", r),
						"path", req.Path,
						"stack", string(stack),
					)
					resp = Error(http.StatusInternalServerError, "internal server error")
					err = nil
				}
			}()
			return next(ctx, req)
		}
	}
}

// ── CORS ──────────────────────────────────────────────────────────────────────

// CORSConfig configures the CORS middleware.
type CORSConfig struct {
	// AllowOrigins is the list of allowed origins.
	// Use ["*"] to allow all origins.
	AllowOrigins []string
	// AllowMethods lists the HTTP methods allowed for cross-origin requests.
	// Defaults to GET, POST, PUT, PATCH, DELETE, OPTIONS.
	AllowMethods []string
	// AllowHeaders lists additional headers the browser is allowed to send.
	AllowHeaders []string
	// ExposeHeaders lists headers the browser is allowed to read from the response.
	ExposeHeaders []string
	// AllowCredentials enables cookies and auth headers in cross-origin requests.
	AllowCredentials bool
	// MaxAge sets the preflight cache duration in seconds.
	MaxAge int
}

// CORS returns a Middleware that adds Cross-Origin Resource Sharing headers.
// Pass an empty CORSConfig{} to use permissive defaults suitable for development.
func CORS(cfg CORSConfig) Middleware {
	if len(cfg.AllowOrigins) == 0 {
		cfg.AllowOrigins = []string{"*"}
	}
	if len(cfg.AllowMethods) == 0 {
		cfg.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	}
	if len(cfg.AllowHeaders) == 0 {
		cfg.AllowHeaders = []string{"Content-Type", "Authorization", "X-Request-Id"}
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = 86400
	}

	origins := strings.Join(cfg.AllowOrigins, ", ")
	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")
	exposed := strings.Join(cfg.ExposeHeaders, ", ")
	maxAge := fmt.Sprintf("%d", cfg.MaxAge)

	return func(next Handler) Handler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			// Handle preflight
			if req.Method == http.MethodOptions {
				resp := &Response{
					StatusCode: http.StatusNoContent,
					Headers:    make(map[string]string),
				}
				applyCORSHeaders(resp.Headers, origins, methods, headers, exposed, maxAge, cfg.AllowCredentials)
				return resp, nil
			}

			resp, err := next(ctx, req)
			if resp == nil {
				resp = &Response{StatusCode: http.StatusOK}
			}
			if resp.Headers == nil {
				resp.Headers = make(map[string]string)
			}
			applyCORSHeaders(resp.Headers, origins, methods, headers, exposed, maxAge, cfg.AllowCredentials)
			return resp, err
		}
	}
}

func applyCORSHeaders(h map[string]string, origins, methods, headers, exposed, maxAge string, creds bool) {
	h["Access-Control-Allow-Origin"] = origins
	h["Access-Control-Allow-Methods"] = methods
	h["Access-Control-Allow-Headers"] = headers
	h["Access-Control-Max-Age"] = maxAge
	if exposed != "" {
		h["Access-Control-Expose-Headers"] = exposed
	}
	if creds {
		h["Access-Control-Allow-Credentials"] = "true"
	}
}

// ── RequestID ─────────────────────────────────────────────────────────────────

// RequestID returns a Middleware that ensures every request has a request ID.
// It reads X-Request-Id from the incoming headers; if absent, it uses the
// transport-supplied ID (API Gateway / ALB request ID). The ID is propagated
// as X-Request-Id on the response.
func RequestID() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, req *Request) (*Response, error) {
			id := req.Header("X-Request-Id")
			if id == "" {
				id = req.RequestID
			}
			if id != "" {
				req.RequestID = id
			}

			resp, err := next(ctx, req)
			if resp != nil && id != "" {
				if resp.Headers == nil {
					resp.Headers = make(map[string]string)
				}
				resp.Headers["X-Request-Id"] = id
			}
			return resp, err
		}
	}
}
