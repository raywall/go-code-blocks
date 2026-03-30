// blocks/server/http.go
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/raywall/go-code-blocks/core"
)

// ── HTTPBlock ─────────────────────────────────────────────────────────────────

// HTTPBlock is a standalone HTTP server block. It listens on a configurable
// port and dispatches requests to the configured Router or Handler.
//
//	router := server.NewRouter()
//	router.GET("/health", func(ctx context.Context, req *server.Request) (*server.Response, error) {
//	    return server.JSON(200, map[string]string{"status": "ok"}), nil
//	})
//
//	httpBlock := server.NewHTTP("api",
//	    server.WithPort(8080),
//	    server.WithRouter(router),
//	    server.WithMiddleware(server.Logging(), server.Recovery()),
//	)
//
//	app.MustRegister(httpBlock)
//	app.InitAll(ctx)  // starts listening
//	httpBlock.Wait()  // blocks until Shutdown is called
type HTTPBlock struct {
	name   string
	cfg    blockConfig
	server *http.Server
	errCh  chan error
}

// NewHTTP creates a new HTTP server block.
func NewHTTP(name string, opts ...Option) *HTTPBlock {
	cfg := buildConfig(opts)
	return &HTTPBlock{
		name:  name,
		cfg:   cfg,
		errCh: make(chan error, 1),
	}
}

// Name implements core.Block.
func (b *HTTPBlock) Name() string { return b.name }

// Init implements core.Block. It builds the http.Server and starts listening
// in a background goroutine. Returns immediately — call Wait to block until
// the server stops.
func (b *HTTPBlock) Init(_ context.Context) error {
	h, err := b.cfg.effectiveHandler()
	if err != nil {
		return fmt.Errorf("server/http %q: %w", b.name, err)
	}

	// Wrap the universal handler in a net/http adapter.
	mux := http.NewServeMux()
	mux.Handle("/", &httpAdapter{handler: h})

	b.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", b.cfg.port),
		Handler:      mux,
		ReadTimeout:  b.cfg.readTimeout,
		WriteTimeout: b.cfg.writeTimeout,
		IdleTimeout:  b.cfg.idleTimeout,
	}

	if b.cfg.tlsConfig != nil {
		b.server.TLSConfig = b.cfg.tlsConfig
	}

	ln, err := net.Listen("tcp", b.server.Addr)
	if err != nil {
		return fmt.Errorf("server/http %q: listen :%d: %w", b.name, b.cfg.port, err)
	}

	go func() {
		var serveErr error
		if b.cfg.certFile != "" {
			serveErr = b.server.ServeTLS(ln, b.cfg.certFile, b.cfg.keyFile)
		} else {
			serveErr = b.server.Serve(ln)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			b.errCh <- serveErr
		} else {
			b.errCh <- nil
		}
	}()

	return nil
}

// Shutdown implements core.Block. It triggers a graceful shutdown of the HTTP
// server, waiting up to the configured ShutdownTimeout for in-flight requests
// to complete.
func (b *HTTPBlock) Shutdown(ctx context.Context) error {
	if b.server == nil {
		return nil
	}
	shutCtx, cancel := context.WithTimeout(ctx, b.cfg.shutdownTimeout)
	defer cancel()
	if err := b.server.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("server/http %q: shutdown: %w", b.name, err)
	}
	return nil
}

// Wait blocks until the server stops — either via Shutdown or due to a fatal
// error. Returns nil on clean shutdown, or the error that caused the stop.
// Typical usage:
//
//	app.InitAll(ctx)
//	defer app.ShutdownAll(ctx)
//	httpBlock.Wait()
func (b *HTTPBlock) Wait() error {
	return <-b.errCh
}

// Port returns the configured listen port.
func (b *HTTPBlock) Port() int { return b.cfg.port }

// ── httpAdapter ───────────────────────────────────────────────────────────────

// httpAdapter bridges net/http.Handler with the universal Handler interface.
type httpAdapter struct{ handler Handler }

func (a *httpAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := requestFromHTTP(r)
	resp, err := a.handler(r.Context(), req)
	if err != nil {
		resp = Error(http.StatusInternalServerError, err.Error())
	}
	if resp == nil {
		resp = Error(http.StatusInternalServerError, "handler returned nil response")
	}
	writeHTTPResponse(w, resp)
}

// ── HTTP ↔ universal conversion ───────────────────────────────────────────────

// requestFromHTTP builds a transport-agnostic *Request from a *http.Request.
func requestFromHTTP(r *http.Request) *Request {
	body, _ := io.ReadAll(r.Body)

	headers := make(map[string]string, len(r.Header))
	for k, vv := range r.Header {
		headers[http.CanonicalHeaderKey(k)] = strings.Join(vv, ", ")
	}

	query := make(map[string][]string)
	for k, v := range r.URL.Query() {
		query[k] = v
	}

	return &Request{
		Method:    r.Method,
		Path:      r.URL.Path,
		Query:     query,
		Headers:   headers,
		Body:      body,
		SourceIP:  r.RemoteAddr,
		RequestID: r.Header.Get("X-Request-Id"),
		Source:    SourceHTTP,
		Raw:       r,
	}
}

// writeHTTPResponse serializes a *Response into a net/http.ResponseWriter.
func writeHTTPResponse(w http.ResponseWriter, resp *Response) {
	// Apply response headers
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}

	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	body, contentType, err := encodeResponseBody(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"response encoding failed"}`))
		return
	}

	if w.Header().Get("Content-Type") == "" && contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	w.WriteHeader(statusCode)
	if body != nil {
		w.Write(body)
	}
}

// encodeResponseBody converts resp.Body to []byte and infers Content-Type.
func encodeResponseBody(resp *Response) ([]byte, string, error) {
	if resp.Body == nil {
		return nil, "", nil
	}
	switch v := resp.Body.(type) {
	case string:
		return []byte(v), "text/plain; charset=utf-8", nil
	case []byte:
		return v, "application/octet-stream", nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, "", err
		}
		return b, "application/json", nil
	}
}

// Ensure HTTPBlock implements core.Block.
var _ core.Block = (*HTTPBlock)(nil)
