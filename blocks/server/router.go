// blocks/server/router.go
package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// ── Router ────────────────────────────────────────────────────────────────────

// Router dispatches incoming requests to registered handlers based on HTTP
// method and path pattern. Path patterns support named segments (:param) and
// a catch-all wildcard (*).
//
//	r := server.NewRouter()
//	r.GET("/users",       listUsersHandler)
//	r.POST("/users",      createUserHandler)
//	r.GET("/users/:id",   getUserHandler)
//	r.PUT("/users/:id",   updateUserHandler)
//	r.DELETE("/users/:id", deleteUserHandler)
//
// Middleware can be applied per-route or globally via Use:
//
//	r.Use(loggingMiddleware, authMiddleware)
//	r.GET("/admin", adminHandler, adminOnlyMiddleware)
type Router struct {
	routes     []*route
	middleware []Middleware
	notFound   Handler
}

type route struct {
	method   string   // uppercase HTTP verb, or "" for any
	segments []string // pattern split on "/", e.g. ["users", ":id"]
	handler  Handler
}

// NewRouter creates an empty Router.
// The default not-found handler returns 404 with a JSON error body.
func NewRouter() *Router {
	return &Router{
		notFound: func(_ context.Context, req *Request) (*Response, error) {
			return Error(http.StatusNotFound,
				fmt.Sprintf("%s %s not found", req.Method, req.Path)), nil
		},
	}
}

// Use registers middleware applied to every route in this router.
// Middleware is applied in declaration order (outermost first).
func (r *Router) Use(middleware ...Middleware) {
	r.middleware = append(r.middleware, middleware...)
}

// NotFound sets a custom handler for unmatched routes.
func (r *Router) NotFound(h Handler) { r.notFound = h }

// Handle registers a handler for the given method and path pattern.
// method is the uppercase HTTP verb; pass "" to match any method.
// Per-route middleware is applied inside the global middleware chain.
func (r *Router) Handle(method, pattern string, h Handler, middleware ...Middleware) {
	if len(middleware) > 0 {
		h = chain(h, middleware...)
	}
	r.routes = append(r.routes, &route{
		method:   strings.ToUpper(method),
		segments: splitPath(pattern),
		handler:  h,
	})
}

// GET registers a handler for GET requests.
func (r *Router) GET(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodGet, pattern, h, middleware...)
}

// POST registers a handler for POST requests.
func (r *Router) POST(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodPost, pattern, h, middleware...)
}

// PUT registers a handler for PUT requests.
func (r *Router) PUT(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodPut, pattern, h, middleware...)
}

// PATCH registers a handler for PATCH requests.
func (r *Router) PATCH(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodPatch, pattern, h, middleware...)
}

// DELETE registers a handler for DELETE requests.
func (r *Router) DELETE(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodDelete, pattern, h, middleware...)
}

// HEAD registers a handler for HEAD requests.
func (r *Router) HEAD(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodHead, pattern, h, middleware...)
}

// OPTIONS registers a handler for OPTIONS requests.
func (r *Router) OPTIONS(pattern string, h Handler, middleware ...Middleware) {
	r.Handle(http.MethodOptions, pattern, h, middleware...)
}

// dispatch finds the matching route for the request and invokes the handler.
// Global middleware is applied here, wrapping all matched handlers.
func (r *Router) dispatch(ctx context.Context, req *Request) (*Response, error) {
	segs := splitPath(req.Path)
	method := strings.ToUpper(req.Method)

	for _, rt := range r.routes {
		if rt.method != "" && rt.method != method {
			continue
		}
		params, ok := matchSegments(rt.segments, segs)
		if !ok {
			continue
		}
		if req.PathParams == nil {
			req.PathParams = make(map[string]string)
		}
		for k, v := range params {
			req.PathParams[k] = v
		}
		h := chain(rt.handler, r.middleware...)
		return h(ctx, req)
	}

	h := chain(r.notFound, r.middleware...)
	return h(ctx, req)
}

// ServeHTTP adapts the Router to net/http.Handler, bridging the HTTP server
// block with the universal Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, httpReq *http.Request) {
	req := requestFromHTTP(httpReq)
	resp, err := r.dispatch(httpReq.Context(), req)
	if err != nil {
		resp = Error(http.StatusInternalServerError, err.Error())
	}
	if resp == nil {
		resp = Error(http.StatusInternalServerError, "handler returned nil response")
	}
	writeHTTPResponse(w, resp)
}

// ── path matching ─────────────────────────────────────────────────────────────

// matchSegments checks whether reqSegs matches the pattern segs.
// Named segments (:param) are captured into the returned map.
// A trailing "*" segment matches any remaining path.
func matchSegments(pattern, req []string) (map[string]string, bool) {
	params := map[string]string{}

	for i, seg := range pattern {
		if seg == "*" {
			return params, true // wildcard matches remainder
		}
		if i >= len(req) {
			return nil, false
		}
		if strings.HasPrefix(seg, ":") {
			params[seg[1:]] = req[i]
		} else if seg != req[i] {
			return nil, false
		}
	}
	if len(pattern) != len(req) {
		return nil, false
	}
	return params, true
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return []string{}
	}
	return strings.Split(p, "/")
}
