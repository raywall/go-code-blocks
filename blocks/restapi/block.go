// Package restapi provides an HTTP REST integration block for go-code-blocks.
//
// It supports GET, POST, PUT, PATCH, DELETE and custom verbs against any HTTP
// API, with pluggable authentication strategies: static Bearer token, OAuth2
// client_credentials (with automatic token caching and refresh), HTTP Basic,
// and API Key (header or query param).
//
// # Token chaining
//
// A Block configured with WithOAuth2ClientCredentials implements the
// TokenProvider interface, so it can authorise another block:
//
//	auth := restapi.New("auth",
//	    restapi.WithBaseURL("https://auth.example.com"),
//	    restapi.WithOAuth2ClientCredentials(
//	        "https://auth.example.com/oauth/token",
//	        os.Getenv("CLIENT_ID"),
//	        os.Getenv("CLIENT_SECRET"),
//	    ),
//	)
//
//	api := restapi.New("api",
//	    restapi.WithBaseURL("https://api.example.com"),
//	    restapi.WithTokenProvider(auth), // 'auth' fetches & refreshes the token
//	)
//
//	app.MustRegister(auth)
//	app.MustRegister(api)
//	app.InitAll(ctx)
package restapi

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultTimeout      = 30 * time.Second
	defaultMaxIdleConns = 20
)

// New creates a new REST API Block.
//
//	block := restapi.New("payments-api",
//	    restapi.WithBaseURL("https://payments.example.com/v1"),
//	    restapi.WithTimeout(10*time.Second),
//	    restapi.WithOAuth2ClientCredentials(tokenURL, clientID, clientSecret),
//	)
func New(name string, opts ...Option) *Block {
	cfg := blockConfig{
		timeout:      defaultTimeout,
		maxIdleConns: defaultMaxIdleConns,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return &Block{name: name, cfg: cfg}
}

// Name implements core.Block.
func (b *Block) Name() string { return b.name }

// Init implements core.Block. It builds the http.Client with the configured
// transport, TLS settings, and connection pool.
func (b *Block) Init(_ context.Context) error {
	transport := b.cfg.transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxIdleConns = b.cfg.maxIdleConns
		t.MaxIdleConnsPerHost = b.cfg.maxIdleConns
		if b.cfg.tlsConfig != nil {
			t.TLSClientConfig = b.cfg.tlsConfig
		} else {
			t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport = t
	}

	b.httpClient = &http.Client{
		Transport: transport,
		Timeout:   b.cfg.timeout,
	}

	// Propagate the http.Client to any oauth2ClientCredentials provider so
	// token fetches use the same transport settings as regular API calls.
	if b.cfg.authApplier != nil {
		if ba, ok := b.cfg.authApplier.(*bearerApplier); ok {
			if o2, ok := ba.provider.(*oauth2ClientCredentials); ok {
				o2.httpClient = b.httpClient
			}
		}
	}

	b.auth = b.cfg.authApplier
	return nil
}

// Shutdown implements core.Block.
// Closes idle connections in the transport pool.
func (b *Block) Shutdown(_ context.Context) error {
	if b.httpClient != nil {
		b.httpClient.CloseIdleConnections()
	}
	return nil
}

// Token implements TokenProvider.
// Allows this block to be passed as WithTokenProvider to another block,
// chaining authentication flows without coupling blocks.
//
// Returns an error when the block has no auth strategy configured or is not
// yet initialised.
func (b *Block) Token(ctx context.Context) (string, error) {
	if b.httpClient == nil {
		return "", fmt.Errorf("restapi %q: Token called before Init", b.name)
	}
	if b.auth == nil {
		return "", fmt.Errorf("restapi %q: no auth strategy configured", b.name)
	}

	// We need the raw token string, not just the header. For bearerApplier
	// we can call the underlying provider directly.
	if ba, ok := b.auth.(*bearerApplier); ok {
		return ba.provider.Token(ctx)
	}
	return "", fmt.Errorf("restapi %q: auth strategy does not implement TokenProvider", b.name)
}
