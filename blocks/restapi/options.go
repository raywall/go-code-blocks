package restapi

import (
	"crypto/tls"
	"net/http"
	"time"
)

// Option configures a REST API Block.
type Option func(*blockConfig)

type blockConfig struct {
	baseURL        string
	defaultHeaders map[string]string
	timeout        time.Duration
	maxIdleConns   int
	tlsConfig      *tls.Config
	transport      http.RoundTripper // custom transport (e.g. for mocking)
	authApplier    authApplier       // resolved auth strategy
}

// ── Connection ────────────────────────────────────────────────────────────────

// WithBaseURL sets the base URL prepended to every request path.
// Trailing slashes are normalised internally.
//
//	restapi.WithBaseURL("https://api.example.com/v2")
func WithBaseURL(u string) Option {
	return func(c *blockConfig) { c.baseURL = u }
}

// WithTimeout sets the per-request timeout.
// Defaults to 30 s when not provided.
func WithTimeout(d time.Duration) Option {
	return func(c *blockConfig) { c.timeout = d }
}

// WithMaxIdleConns controls connection pool size for keep-alive reuse.
func WithMaxIdleConns(n int) Option {
	return func(c *blockConfig) { c.maxIdleConns = n }
}

// WithTLS configures a custom TLS configuration (e.g. mTLS, custom CA).
func WithTLS(cfg *tls.Config) Option {
	return func(c *blockConfig) { c.tlsConfig = cfg }
}

// WithTransport injects a fully custom http.RoundTripper.
// Useful for testing (e.g. httptest transports) or adding middleware like
// retry logic, tracing, or circuit breakers.
func WithTransport(rt http.RoundTripper) Option {
	return func(c *blockConfig) { c.transport = rt }
}

// ── Default headers ───────────────────────────────────────────────────────────

// WithHeader adds a default header sent with every request.
// Call multiple times to add several headers.
//
//	restapi.WithHeader("X-API-Version", "2024-01")
//	restapi.WithHeader("Accept-Language", "pt-BR")
func WithHeader(key, value string) Option {
	return func(c *blockConfig) {
		if c.defaultHeaders == nil {
			c.defaultHeaders = make(map[string]string)
		}
		c.defaultHeaders[key] = value
	}
}

// ── Authentication ────────────────────────────────────────────────────────────

// WithBearerToken configures a static Bearer token sent on every request.
// Use WithTokenProvider for dynamic tokens that require fetching or refreshing.
//
//	restapi.WithBearerToken("eyJhbGciOiJSUzI1NiJ9...")
func WithBearerToken(token string) Option {
	return func(c *blockConfig) {
		c.authApplier = &bearerApplier{provider: &staticToken{token: token}}
	}
}

// WithTokenProvider configures a dynamic Bearer token source.
// The provider is called before each request; it is responsible for caching
// and refreshing the token internally.
//
// Any Block configured with WithOAuth2ClientCredentials satisfies TokenProvider,
// enabling one block to authorise another:
//
//	authBlock := restapi.New("auth", restapi.WithOAuth2ClientCredentials(...))
//	apiBlock  := restapi.New("api",  restapi.WithTokenProvider(authBlock))
func WithTokenProvider(p TokenProvider) Option {
	return func(c *blockConfig) {
		c.authApplier = &bearerApplier{provider: p}
	}
}

// WithOAuth2ClientCredentials configures the block to fetch and cache a Bearer
// token via the OAuth2 client_credentials grant before each request.
//
// The token is refreshed automatically 30 s before expiry.
//
//	restapi.WithOAuth2ClientCredentials(
//	    "https://auth.example.com/oauth/token",
//	    "my-client-id",
//	    "my-client-secret",
//	    "read:data", "write:data",   // optional scopes
//	)
func WithOAuth2ClientCredentials(tokenURL, clientID, clientSecret string, scopes ...string) Option {
	return func(c *blockConfig) {
		c.authApplier = &bearerApplier{
			provider: &oauth2ClientCredentials{
				tokenURL:     tokenURL,
				clientID:     clientID,
				clientSecret: clientSecret,
				scopes:       scopes,
			},
		}
	}
}

// WithBasicAuth configures HTTP Basic Authentication.
//
//	restapi.WithBasicAuth("admin", "s3cr3t")
func WithBasicAuth(username, password string) Option {
	return func(c *blockConfig) {
		c.authApplier = &basicApplier{user: username, password: password}
	}
}

// WithAPIKeyHeader sends the API key as a request header.
//
//	restapi.WithAPIKeyHeader("X-API-Key", "abc123")
func WithAPIKeyHeader(headerName, key string) Option {
	return func(c *blockConfig) {
		c.authApplier = &apiKeyApplier{key: headerName, value: key, inHeader: true}
	}
}

// WithAPIKeyQuery appends the API key as a query parameter.
//
//	restapi.WithAPIKeyQuery("api_key", "abc123")
func WithAPIKeyQuery(paramName, key string) Option {
	return func(c *blockConfig) {
		c.authApplier = &apiKeyApplier{key: paramName, value: key, inHeader: false}
	}
}
