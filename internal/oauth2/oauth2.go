// Package oauth2 manages OAuth2 tokens for neomd accounts.
// It runs the authorization code flow on first use (opening the user's browser),
// persists the token to a JSON file, and refreshes it automatically on expiry.
//
// Endpoints can be discovered automatically from an OIDC issuer URL
// (e.g. "https://accounts.google.com") or provided manually via AuthURL/TokenURL.
package oauth2

import (
	"cmp"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/emersion/go-sasl"
	"golang.org/x/oauth2"
)

//go:embed static/oauth2_success.html
var successHTML string

// Config holds OAuth2 settings for a single account.
// Either IssuerURL (OIDC discovery) or both AuthURL+TokenURL must be set.
// If all three are set, AuthURL and TokenURL take precedence.
type Config struct {
	ClientID     string
	ClientSecret string
	IssuerURL    string // OIDC issuer; discovers AuthURL+TokenURL automatically
	AuthURL      string // manual override (skips discovery)
	TokenURL     string // manual override (skips discovery)
	Scopes       []string
	RedirectPort int    // local callback port; defaults to 8085
	TokenFile    string // path to persist the token JSON

	DiscoveryTimeout time.Duration // Timeout for the discovery OIDC HTTP request. Defaults to 10s
	AuthFlowTimeout  time.Duration // Timeout for the AuthFlow to be completed. Defaults to 5m
}

func (c *Config) redirectPort() int {
	if c.RedirectPort == 0 {
		return 8085
	}
	return c.RedirectPort
}

func (c *Config) redirectURL() string {
	return fmt.Sprintf("http://localhost:%d/callback", c.redirectPort())
}

// Default OIDC discovery timeout: 10 seconds
func (c *Config) discoveryTimeout() time.Duration {
	return cmp.Or(c.DiscoveryTimeout, 10*time.Second)
}

// Default Authflow timeout: 5 minutes
func (c *Config) authFlowTimeout() time.Duration {
	return cmp.Or(c.AuthFlowTimeout, 5*time.Minute)
}

// resolve returns the final AuthURL and TokenURL, discovering them from the
// OIDC issuer document if manual URLs are not provided.
func (c *Config) resolve(ctx context.Context, timeout time.Duration) (string, string, error) {
	if c.AuthURL != "" && c.TokenURL != "" {
		return c.AuthURL, c.TokenURL, nil
	}
	if c.IssuerURL == "" {
		return "", "", fmt.Errorf("oauth2: set oauth2_issuer_url or both oauth2_auth_url and oauth2_token_url")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	authURL, tokenURL, err := discoverEndpoints(ctx, c.IssuerURL)
	if err != nil {
		return "", "", err
	}
	return cmp.Or(c.AuthURL, authURL), cmp.Or(c.TokenURL, tokenURL), nil
}

// discoverEndpoints fetches {issuer}/.well-known/openid-configuration and
// returns the authorization_endpoint and token_endpoint values.
func discoverEndpoints(ctx context.Context, issuerURL string) (string, string, error) {
	discoveryURL := issuerURL + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build discovery request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("fetch OIDC discovery document from %s: %w", discoveryURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("OIDC discovery %s: HTTP %d", discoveryURL, resp.StatusCode)
	}

	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	const maxBody = 1 << 20 // 1 MiB
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&doc); err != nil {
		return "", "", fmt.Errorf("parse OIDC discovery document: %w", err)
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return "", "", fmt.Errorf("OIDC discovery document missing authorization_endpoint or token_endpoint")
	}
	return doc.AuthorizationEndpoint, doc.TokenEndpoint, nil
}

// TokenSource returns a function that always provides a valid access token.
// On the first call it loads the token from TokenFile; if none exists it runs
// the full browser-based authorization code flow. Subsequent calls refresh the
// token automatically when it is expired.
func TokenSource(ctx context.Context, cfg Config) (func() (string, error), error) {
	authURL, tokenURL, err := cfg.resolve(ctx, cfg.discoveryTimeout())
	if err != nil {
		return nil, err
	}
	oc := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   authURL,
			TokenURL:  tokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: cfg.redirectURL(),
		Scopes:      cfg.Scopes,
	}

	tok, err := loadToken(cfg.TokenFile)
	if err != nil {
		flowCtx, flowCancel := context.WithTimeout(ctx, cfg.authFlowTimeout())
		defer flowCancel()

		tok, err = runAuthFlow(flowCtx, cfg, oc)
		if err != nil {
			return nil, fmt.Errorf("oauth2 auth flow: %w", err)
		}
		if err := saveToken(cfg.TokenFile, tok); err != nil {
			return nil, fmt.Errorf("save oauth2 token: %w", err)
		}
	}

	ts := oc.TokenSource(ctx, tok)

	return func() (string, error) {
		t, err := ts.Token()
		if err != nil {
			return "", err
		}
		_ = saveToken(cfg.TokenFile, t)
		return t.AccessToken, nil
	}, nil
}

// runAuthFlow starts a local callback server, opens the browser at the
// authorization URL, and waits for the provider to redirect back with a code.
func runAuthFlow(ctx context.Context, cfg Config, oc *oauth2.Config) (*oauth2.Token, error) {
	state, err := randomState()
	if err != nil {
		return nil, fmt.Errorf("generate oauth2 state: %w", err)
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	ln, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", cfg.redirectPort()))
	if err != nil {
		return nil, fmt.Errorf("listen on redirect port %d: %w", cfg.redirectPort(), err)
	}

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth2 state mismatch")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth2 callback: missing code parameter")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintln(w, successHTML)
		codeCh <- code
	})

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	defer srv.Close()

	authURL := oc.AuthCodeURL(state, oauth2.AccessTypeOffline)
	fmt.Printf("\nOpening browser for OAuth2 authorization...\n%s\n\nIf the browser did not open, paste the URL above manually.\nWaiting for authorization...\n\n", authURL)
	openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("oauth2 auth flow: %w", ctx.Err())
	}

	tok, err := oc.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	return tok, nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Opens a browser to initiate the AuthFlow
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}

func saveToken(path string, tok *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(tok)
}

// XOAuth2Client returns a sasl.Client that implements the XOAUTH2 mechanism.
// Both Google and Microsoft Exchange Online support XOAUTH2; it is more
// broadly compatible than the RFC 7628 OAUTHBEARER mechanism.
func XOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

type xoauth2Client struct {
	username string
	token    string
}

func (c *xoauth2Client) Start() (string, []byte, error) {
	ir := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.username, c.token)
	return "XOAUTH2", []byte(ir), nil
}

// Next is called when the server sends a challenge after a failed auth.
// We return an empty response to cleanly abort the exchange.
func (c *xoauth2Client) Next(_ []byte) ([]byte, error) {
	return []byte{}, nil
}

func loadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parse token file: %w", err)
	}
	return &tok, nil
}
