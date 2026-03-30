// blocks/server/options.go
package server

import (
	"crypto/tls"
	"time"
)

// ── Common options ────────────────────────────────────────────────────────────

// Option configures a server block.
type Option func(*blockConfig)

type blockConfig struct {
	// HTTP server
	port            int
	readTimeout     time.Duration
	writeTimeout    time.Duration
	idleTimeout     time.Duration
	shutdownTimeout time.Duration
	tlsConfig       *tls.Config
	certFile        string
	keyFile         string

	// Lambda
	source Source // SourceAPIGatewayV1 | SourceAPIGatewayV2 | SourceALB

	// Shared
	router     *Router
	handler    Handler // flat handler (alternative to router)
	middleware []Middleware
}

// WithRouter sets the Router used to dispatch incoming requests.
// Either WithRouter or WithHandler must be provided.
func WithRouter(r *Router) Option {
	return func(c *blockConfig) { c.router = r }
}

// WithHandler sets a single Handler that receives every request.
// Use WithRouter instead when you need method/path routing.
func WithHandler(h Handler) Option {
	return func(c *blockConfig) { c.handler = h }
}

// WithMiddleware registers global middleware applied to every request before
// it reaches the router or handler. Declared in outermost-first order.
func WithMiddleware(middleware ...Middleware) Option {
	return func(c *blockConfig) {
		c.middleware = append(c.middleware, middleware...)
	}
}

// ── HTTP server options ───────────────────────────────────────────────────────

// WithPort sets the TCP port the HTTP server listens on.
// Defaults to 8080.
func WithPort(port int) Option {
	return func(c *blockConfig) { c.port = port }
}

// WithReadTimeout sets the maximum duration for reading the full request.
// Defaults to 30 s.
func WithReadTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.readTimeout = d }
}

// WithWriteTimeout sets the maximum duration for writing the full response.
// Defaults to 30 s.
func WithWriteTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.writeTimeout = d }
}

// WithIdleTimeout sets the maximum time to wait for the next keep-alive request.
// Defaults to 60 s.
func WithIdleTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.idleTimeout = d }
}

// WithShutdownTimeout sets the maximum time allowed for graceful shutdown.
// Defaults to 10 s.
func WithShutdownTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.shutdownTimeout = d }
}

// WithTLS configures the server to use TLS with the given certificate and key files.
// Mutually exclusive with WithTLSConfig.
func WithTLS(certFile, keyFile string) Option {
	return func(c *blockConfig) {
		c.certFile = certFile
		c.keyFile = keyFile
	}
}

// WithTLSConfig injects a custom *tls.Config.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(c *blockConfig) { c.tlsConfig = cfg }
}

// ── Lambda options ────────────────────────────────────────────────────────────

// WithSource declares which Lambda event source the block expects.
// Required for Lambda blocks — must be one of SourceAPIGatewayV1,
// SourceAPIGatewayV2, or SourceALB.
func WithSource(s Source) Option {
	return func(c *blockConfig) { c.source = s }
}

// ── helpers ───────────────────────────────────────────────────────────────────

func buildConfig(opts []Option) blockConfig {
	cfg := blockConfig{
		port:            8080,
		readTimeout:     30 * time.Second,
		writeTimeout:    30 * time.Second,
		idleTimeout:     60 * time.Second,
		shutdownTimeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// effectiveHandler resolves the final top-level Handler.
// If a Router is configured, it wraps it with any global middleware.
// If a flat Handler is configured, it wraps that instead.
func (cfg *blockConfig) effectiveHandler() (Handler, error) {
	var h Handler
	switch {
	case cfg.router != nil:
		h = cfg.router.dispatch
	case cfg.handler != nil:
		h = cfg.handler
	default:
		return nil, errNoHandler
	}
	if len(cfg.middleware) > 0 {
		h = chain(h, cfg.middleware...)
	}
	return h, nil
}
