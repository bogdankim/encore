package lastfm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bogdankim/encore/internal/config"
)

// SecretAccount is the config.Secret* account holding Credentials as JSON.
const SecretAccount = "lastfm"

// Environment overrides for the application key and secret.
const (
	EnvKey    = "ENCORE_LASTFM_KEY"
	EnvSecret = "ENCORE_LASTFM_SECRET"
)

// Credentials are everything needed to scrobble.
type Credentials struct {
	APIKey     string `json:"apiKey"`
	Secret     string `json:"secret"`
	SessionKey string `json:"sessionKey"`
	Username   string `json:"username"`
}

// Ready reports whether c can scrobble.
func (c Credentials) Ready() bool { return c.APIKey != "" && c.Secret != "" && c.SessionKey != "" }

// LoadCredentials reads stored credentials and applies the env overrides.
// Nothing stored is not an error: it returns empty Credentials.
func LoadCredentials() (Credentials, error) {
	var c Credentials
	raw, err := config.SecretGet(SecretAccount)
	switch {
	case errors.Is(err, config.ErrNoSecret):
	case err != nil:
		return withEnv(c), err
	default:
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return withEnv(Credentials{}), fmt.Errorf("last.fm: stored credentials: %w", err)
		}
	}
	return withEnv(c), nil
}

func withEnv(c Credentials) Credentials {
	if k := strings.TrimSpace(os.Getenv(EnvKey)); k != "" {
		c.APIKey = k
	}
	if s := strings.TrimSpace(os.Getenv(EnvSecret)); s != "" {
		c.Secret = s
	}
	return c
}

// SaveCredentials stores c in the Keychain.
func SaveCredentials(c Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return config.SecretSet(SecretAccount, string(b))
}

// DeleteCredentials forgets the stored credentials (disconnect).
func DeleteCredentials() error { return config.SecretDelete(SecretAccount) }

// ErrAuthTimeout means the user didn't approve access in time.
var ErrAuthTimeout = errors.New("last.fm: authorization timed out")

// Auth runs the desktop auth flow for the setup overlay:
//
//	url, token, err := a.BeginAuth(ctx, key, secret) // validates key and secret
//	// the UI runs `open url`
//	creds, err := a.WaitSession(ctx, token)          // polls until approved
//	SaveCredentials(creds)
//
// It never opens anything itself.
type Auth struct {
	HTTP     *http.Client  // optional
	Root     string        // optional API root, for tests
	Interval time.Duration // poll interval, default 2.5 s
	Timeout  time.Duration // give up after, default 10 min

	key, secret string
}

// BeginAuth gets a request token and returns the page the user must open to
// approve it. A wrong key or secret fails here.
func (a *Auth) BeginAuth(ctx context.Context, key, secret string) (string, string, error) {
	key, secret = strings.TrimSpace(key), strings.TrimSpace(secret)
	if key == "" || secret == "" {
		return "", "", errors.New("last.fm: API key and secret are both required")
	}
	a.key, a.secret = key, secret
	c := a.client()
	token, err := c.GetToken(ctx)
	if err != nil {
		return "", "", err
	}
	return c.AuthorizeURL(token), token, nil
}

// WaitSession polls until the user approves token in the browser, then
// returns complete Credentials (not yet saved).
func (a *Auth) WaitSession(ctx context.Context, token string) (Credentials, error) {
	if a.key == "" {
		return Credentials{}, errors.New("last.fm: WaitSession before BeginAuth")
	}
	every, limit := a.Interval, a.Timeout
	if every <= 0 {
		every = 2500 * time.Millisecond
	}
	if limit <= 0 {
		limit = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	s, err := a.client().PollSession(ctx, token, every)
	if errors.Is(err, context.DeadlineExceeded) {
		return Credentials{}, ErrAuthTimeout
	}
	if err != nil {
		return Credentials{}, err
	}
	return Credentials{APIKey: a.key, Secret: a.secret, SessionKey: s.Key, Username: s.Name}, nil
}

func (a *Auth) client() *Client {
	return &Client{Key: a.key, Secret: a.secret, HTTP: a.HTTP, Root: a.Root}
}
