package restapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ── TokenProvider ─────────────────────────────────────────────────────────────

// TokenProvider is the interface implemented by any entity that can supply a
// Bearer token. A Block satisfies this interface when configured with
// WithOAuth2ClientCredentials, enabling one block to authorise another.
//
//	authBlock := restapi.New("auth", restapi.WithOAuth2ClientCredentials(...))
//	apiBlock  := restapi.New("api",  restapi.WithTokenProvider(authBlock))
type TokenProvider interface {
	// Token returns a valid Bearer token string, fetching or refreshing it
	// when necessary.
	Token(ctx context.Context) (string, error)
}

// ── staticToken ───────────────────────────────────────────────────────────────

// staticToken always returns the same pre-configured token.
// Used by WithBearerToken.
type staticToken struct{ token string }

func (s *staticToken) Token(_ context.Context) (string, error) { return s.token, nil }

// ── oauth2ClientCredentials ───────────────────────────────────────────────────

// tokenResponse is the minimal shape of an OAuth2 token endpoint response.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"` // seconds
}

// oauth2ClientCredentials fetches and caches a token via the OAuth2
// client_credentials grant. It auto-refreshes before expiry.
type oauth2ClientCredentials struct {
	tokenURL     string
	clientID     string
	clientSecret string
	scopes       []string
	httpClient   *http.Client

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

// Token returns a cached token, or fetches a new one when expired or absent.
func (o *oauth2ClientCredentials) Token(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Refresh 30 s before actual expiry to account for clock skew.
	if o.cached != "" && time.Now().Before(o.expiresAt.Add(-30*time.Second)) {
		return o.cached, nil
	}

	token, expiresIn, err := o.fetch(ctx)
	if err != nil {
		return "", err
	}

	o.cached = token
	if expiresIn > 0 {
		o.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	} else {
		// Fallback when the server omits expires_in: assume 1 hour.
		o.expiresAt = time.Now().Add(time.Hour)
	}
	return o.cached, nil
}

func (o *oauth2ClientCredentials) fetch(ctx context.Context) (string, int, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {o.clientID},
		"client_secret": {o.clientSecret},
	}
	if len(o.scopes) > 0 {
		form.Set("scope", strings.Join(o.scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("oauth2: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := o.httpClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("oauth2: fetch token: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("oauth2: token endpoint returned %d: %s",
			resp.StatusCode, string(body))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("oauth2: parse token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("oauth2: token endpoint returned empty access_token")
	}
	return tr.AccessToken, tr.ExpiresIn, nil
}

// ── authApplier ───────────────────────────────────────────────────────────────

// authApplier knows how to attach credentials to an outgoing *http.Request.
type authApplier interface {
	apply(ctx context.Context, req *http.Request) error
}

// bearerApplier injects "Authorization: Bearer <token>" using a TokenProvider.
type bearerApplier struct{ provider TokenProvider }

func (a *bearerApplier) apply(ctx context.Context, req *http.Request) error {
	token, err := a.provider.Token(ctx)
	if err != nil {
		return fmt.Errorf("bearer auth: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// basicApplier injects "Authorization: Basic <base64(user:pass)>".
type basicApplier struct{ user, password string }

func (a *basicApplier) apply(_ context.Context, req *http.Request) error {
	encoded := base64.StdEncoding.EncodeToString(
		[]byte(a.user + ":" + a.password))
	req.Header.Set("Authorization", "Basic "+encoded)
	return nil
}

// apiKeyApplier injects an API key as a header or query parameter.
type apiKeyApplier struct {
	key      string
	value    string
	inHeader bool // true = header, false = query param
}

func (a *apiKeyApplier) apply(_ context.Context, req *http.Request) error {
	if a.inHeader {
		req.Header.Set(a.key, a.value)
	} else {
		q := req.URL.Query()
		q.Set(a.key, a.value)
		req.URL.RawQuery = q.Encode()
	}
	return nil
}
