// blocks/server/tcp.go
package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/raywall/go-code-blocks/core"
)

// ── ConnHandler ───────────────────────────────────────────────────────────────

// ConnHandler is the function signature for handling a raw TCP connection.
// It is called in its own goroutine for each accepted connection and must
// return when the connection is done (either by the client disconnecting or
// when the context is cancelled).
//
//	handler := func(ctx context.Context, conn *server.Conn) {
//	    for {
//	        msg, err := conn.ReadMessage()
//	        if err != nil { return }
//	        fmt.Printf("[%s] %x\n", conn.RemoteAddr(), msg)
//	    }
//	}
type ConnHandler func(ctx context.Context, conn *Conn)

// ── Conn ──────────────────────────────────────────────────────────────────────

// Conn wraps a raw net.Conn and provides convenience methods for
// reading frames and writing responses.
type Conn struct {
	raw          net.Conn
	bufSize      int
	readTimeout  time.Duration
	writeTimeout time.Duration
}

// RemoteAddr returns the remote address string of the connection.
func (c *Conn) RemoteAddr() string { return c.raw.RemoteAddr().String() }

// LocalAddr returns the local address string of the server-side endpoint.
func (c *Conn) LocalAddr() string { return c.raw.LocalAddr().String() }

// Read reads up to len(p) bytes into p from the connection.
// It is a direct pass-through to the underlying net.Conn.
func (c *Conn) Read(p []byte) (int, error) {
	if c.readTimeout > 0 {
		c.raw.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	return c.raw.Read(p)
}

// Write sends p to the remote device.
func (c *Conn) Write(p []byte) (int, error) {
	if c.writeTimeout > 0 {
		c.raw.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	}
	return c.raw.Write(p)
}

// ReadMessage reads a single chunk of data up to the configured buffer size.
// Returns io.EOF when the client disconnects gracefully.
func (c *Conn) ReadMessage() ([]byte, error) {
	buf := make([]byte, c.bufSize)
	if c.readTimeout > 0 {
		c.raw.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	n, err := c.raw.Read(buf)
	if n > 0 {
		return buf[:n], err
	}
	return nil, err
}

// ReadFull reads exactly len(p) bytes, blocking until all are received.
func (c *Conn) ReadFull(p []byte) error {
	if c.readTimeout > 0 {
		c.raw.SetReadDeadline(time.Now().Add(c.readTimeout))
	}
	_, err := io.ReadFull(c.raw, p)
	return err
}

// Close closes the connection immediately.
func (c *Conn) Close() error { return c.raw.Close() }

// Raw returns the underlying net.Conn for advanced use cases.
func (c *Conn) Raw() net.Conn { return c.raw }

// ── TCPBlock ──────────────────────────────────────────────────────────────────

// TCPBlock is a raw TCP server block. It listens on a configurable port and
// dispatches each accepted connection to the registered ConnHandler in its own
// goroutine.
//
//	handler := func(ctx context.Context, conn *server.Conn) {
//	    defer conn.Close()
//	    for {
//	        msg, err := conn.ReadMessage()
//	        if err != nil { return }
//	        // process msg...
//	    }
//	}
//
//	tcp := server.NewTCP("tracker",
//	    server.WithTCPPort(5001),
//	    server.WithConnHandler(handler),
//	    server.WithBufSize(1024),
//	)
//
//	app.MustRegister(tcp)
//	app.InitAll(ctx)
//	tcp.Wait()
type TCPBlock struct {
	name  string
	cfg   tcpConfig
	ln    net.Listener
	wg    sync.WaitGroup
	done  chan struct{}
	errCh chan error
}

type tcpConfig struct {
	port            int
	handler         ConnHandler
	bufSize         int
	readTimeout     time.Duration
	writeTimeout    time.Duration
	shutdownTimeout time.Duration
}

// ── TCPOptions ────────────────────────────────────────────────────────────────

// TCPOption configures a TCPBlock.
type TCPOption func(*tcpConfig)

// WithTCPPort sets the TCP port to listen on. Defaults to 5001.
func WithTCPPort(port int) TCPOption {
	return func(c *tcpConfig) { c.port = port }
}

// WithConnHandler sets the function called for each accepted connection.
// Required — Init returns an error if not provided.
func WithConnHandler(h ConnHandler) TCPOption {
	return func(c *tcpConfig) { c.handler = h }
}

// WithBufSize sets the default buffer size for ReadMessage. Defaults to 1024 bytes.
func WithBufSize(n int) TCPOption {
	return func(c *tcpConfig) { c.bufSize = n }
}

// WithConnReadTimeout sets a per-read deadline on each connection.
// 0 means no deadline (default).
func WithConnReadTimeout(d time.Duration) TCPOption {
	return func(c *tcpConfig) { c.readTimeout = d }
}

// WithConnWriteTimeout sets a per-write deadline on each connection.
func WithConnWriteTimeout(d time.Duration) TCPOption {
	return func(c *tcpConfig) { c.writeTimeout = d }
}

// WithTCPShutdownTimeout sets the maximum time to wait for active connections
// to finish before forcing close. Defaults to 10 s.
func WithTCPShutdownTimeout(d time.Duration) TCPOption {
	return func(c *tcpConfig) { c.shutdownTimeout = d }
}

// ── Constructor ───────────────────────────────────────────────────────────────

// NewTCP creates a new TCP server block.
//
//	tcp := server.NewTCP("obd-tracker",
//	    server.WithTCPPort(5001),
//	    server.WithConnHandler(myHandler),
//	    server.WithBufSize(2048),
//	    server.WithConnReadTimeout(5*time.Minute),
//	)
func NewTCP(name string, opts ...TCPOption) *TCPBlock {
	cfg := tcpConfig{
		port:            5001,
		bufSize:         1024,
		shutdownTimeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &TCPBlock{
		name:  name,
		cfg:   cfg,
		done:  make(chan struct{}),
		errCh: make(chan error, 1),
	}
}

// Name implements core.Block.
func (b *TCPBlock) Name() string { return b.name }

// Init implements core.Block. It opens the TCP listener and starts the
// accept loop in a background goroutine. Returns immediately.
func (b *TCPBlock) Init(_ context.Context) error {
	if b.cfg.handler == nil {
		return fmt.Errorf("server/tcp %q: no handler configured; use WithConnHandler", b.name)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", b.cfg.port))
	if err != nil {
		return fmt.Errorf("server/tcp %q: listen :%d: %w", b.name, b.cfg.port, err)
	}
	b.ln = ln

	go b.acceptLoop()
	return nil
}

// Shutdown implements core.Block. Closes the listener (which unblocks Accept),
// then waits up to the configured shutdown timeout for active connections to
// finish.
func (b *TCPBlock) Shutdown(_ context.Context) error {
	// Signal the accept loop to stop.
	close(b.done)

	// Close the listener so Accept() returns immediately.
	if b.ln != nil {
		b.ln.Close()
	}

	// Wait for all active connection goroutines with timeout.
	finished := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(finished)
	}()

	select {
	case <-finished:
		return nil
	case <-time.After(b.cfg.shutdownTimeout):
		return fmt.Errorf("server/tcp %q: shutdown timeout after %s with active connections",
			b.name, b.cfg.shutdownTimeout)
	}
}

// Wait blocks until the TCP server stops — either via Shutdown or a fatal
// accept error. Returns nil on clean shutdown.
func (b *TCPBlock) Wait() error { return <-b.errCh }

// Port returns the configured listen port.
func (b *TCPBlock) Port() int { return b.cfg.port }

// acceptLoop runs in a background goroutine, accepting connections until
// the listener is closed (Shutdown called).
func (b *TCPBlock) acceptLoop() {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			select {
			case <-b.done:
				// Clean shutdown — not an error.
				b.errCh <- nil
			default:
				b.errCh <- fmt.Errorf("server/tcp %q: accept: %w", b.name, err)
			}
			return
		}

		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Cancel the handler context when shutdown is triggered.
			go func() {
				select {
				case <-b.done:
					conn.Close()
				case <-ctx.Done():
				}
			}()

			wrapped := &Conn{
				raw:          conn,
				bufSize:      b.cfg.bufSize,
				readTimeout:  b.cfg.readTimeout,
				writeTimeout: b.cfg.writeTimeout,
			}
			b.cfg.handler(ctx, wrapped)
		}()
	}
}

// Ensure TCPBlock implements core.Block.
var _ core.Block = (*TCPBlock)(nil)
