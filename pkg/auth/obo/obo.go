package obo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// expirySkew is subtracted from the token lifetime so we refresh slightly
// before the downstream token actually expires.
const expirySkew = 30 * time.Second

// Exchanger performs the OAuth2 On-Behalf-Of flow: it exchanges an incoming
// user access token (the assertion) for a downstream access token scoped to a
// specific resource.
//
// https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-on-behalf-of-flow
type Exchanger struct {
	client *http.Client

	clientID     string
	clientSecret string
	scope        string

	tokenURL string

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	token   string
	expires time.Time
}

func New(issuer, clientID, clientSecret, scope string) (*Exchanger, error) {
	if issuer == "" {
		return nil, errors.New("obo: issuer is required")
	}

	if clientID == "" {
		return nil, errors.New("obo: client_id is required")
	}

	if clientSecret == "" {
		return nil, errors.New("obo: client_secret is required")
	}

	if scope == "" {
		return nil, errors.New("obo: scope is required")
	}

	provider, err := oidc.NewProvider(context.Background(), issuer)

	if err != nil {
		return nil, err
	}

	return &Exchanger{
		client: http.DefaultClient,

		clientID:     clientID,
		clientSecret: clientSecret,
		scope:        scope,

		tokenURL: provider.Endpoint().TokenURL,

		cache: make(map[string]entry),
	}, nil
}

// Token exchanges the given user access token (assertion) for a downstream
// access token. Results are cached per assertion until shortly before they expire.
func (e *Exchanger) Token(ctx context.Context, assertion string) (string, error) {
	if assertion == "" {
		return "", errors.New("obo: missing assertion")
	}

	key := hash(assertion)

	if token, ok := e.lookup(key); ok {
		return token, nil
	}

	token, expires, err := e.exchange(ctx, assertion)

	if err != nil {
		return "", err
	}

	e.store(key, token, expires)

	return token, nil
}

func (e *Exchanger) lookup(key string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.cache[key]

	if !ok || !time.Now().Before(c.expires) {
		return "", false
	}

	return c.token, true
}

func (e *Exchanger) store(key, token string, expires time.Time) {
	now := time.Now()

	if !now.Before(expires) {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	for k, c := range e.cache {
		if !now.Before(c.expires) {
			delete(e.cache, k)
		}
	}

	e.cache[key] = entry{
		token:   token,
		expires: expires,
	}
}

func (e *Exchanger) exchange(ctx context.Context, assertion string) (string, time.Time, error) {
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	data.Set("client_id", e.clientID)
	data.Set("client_secret", e.clientSecret)
	data.Set("assertion", assertion)
	data.Set("scope", e.scope)
	data.Set("requested_token_use", "on_behalf_of")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.tokenURL, strings.NewReader(data.Encode()))

	if err != nil {
		return "", time.Time{}, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := e.client.Do(req)

	if err != nil {
		return "", time.Time{}, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {
		return "", time.Time{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, errors.New("obo: token exchange failed (" + resp.Status + "): " + strings.TrimSpace(string(body)))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, err
	}

	if result.AccessToken == "" {
		return "", time.Time{}, errors.New("obo: token exchange returned no access_token")
	}

	expires := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)

	if result.ExpiresIn > 0 {
		expires = expires.Add(-expirySkew)
	}

	return result.AccessToken, expires, nil
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
