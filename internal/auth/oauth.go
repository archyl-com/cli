// Package auth signs the CLI in through Archyl's OAuth 2.1 provider: dynamic
// client registration, PKCE (S256) and a loopback redirect (RFC 8252), then
// keeps the session alive with rotating refresh tokens.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/credentials"
	"github.com/archyl-com/cli/internal/safehttp"
)

// Scope asks for read and write access; sessions, memories and change
// requests are writes.
const Scope = "mcp:write"

// ErrRefreshRejected means the refresh token is no longer valid and the user
// must sign in again.
var ErrRefreshRejected = errors.New("session expired")

// Flow runs the browser sign-in against one Archyl host.
type Flow struct {
	Host string
	HTTP *http.Client
	// OpenBrowser opens the authorization URL. Login still works when it
	// fails: the URL is also handed to Prompt.
	OpenBrowser func(url string) error
	// Prompt is told the URL the user must visit.
	Prompt func(url string)
	// Timeout bounds the wait for the browser to come back.
	Timeout time.Duration
}

// Login completes the authorization-code flow and returns the token pair.
func (f *Flow) Login(ctx context.Context) (*credentials.Credential, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open loopback listener: %w", err)
	}
	defer func() { _ = listener.Close() }()
	redirectURI := fmt.Sprintf("http://%s/callback", listener.Addr().String())

	clientID, err := f.register(ctx, redirectURI)
	if err != nil {
		return nil, err
	}

	verifier := randomString(32)
	state := randomString(16)
	authorizeURL := f.endpoint("/oauth/authorize") + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"scope":                 {Scope},
	}.Encode()

	codes := make(chan callbackResult, 1)
	server := &http.Server{
		Handler:           callbackHandler(state, codes),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	if f.Prompt != nil {
		f.Prompt(authorizeURL)
	}
	if f.OpenBrowser != nil {
		_ = f.OpenBrowser(authorizeURL)
	}

	timeout := f.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var result callbackResult
	select {
	case result = <-codes:
	case <-waitCtx.Done():
		return nil, fmt.Errorf("timed out waiting for the browser sign-in")
	}
	if result.err != nil {
		return nil, result.err
	}

	return f.exchange(ctx, clientID, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {result.code},
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	})
}

// Refresh trades a refresh token for a new pair. The old refresh token is
// consumed by the server whether or not this succeeds.
func (f *Flow) Refresh(ctx context.Context, cred *credentials.Credential) (*credentials.Credential, error) {
	if cred.RefreshToken == "" || cred.ClientID == "" {
		return nil, ErrRefreshRejected
	}
	return f.exchange(ctx, cred.ClientID, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
		"client_id":     {cred.ClientID},
	})
}

func (f *Flow) endpoint(path string) string {
	return strings.TrimSuffix(f.Host, "/") + path
}

func (f *Flow) client() *http.Client {
	if f.HTTP != nil {
		return f.HTTP
	}
	return safehttp.Client(30 * time.Second)
}

func (f *Flow) register(ctx context.Context, redirectURI string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"redirect_uris": []string{redirectURI},
		"client_name":   "Archyl CLI",
		"client_uri":    "https://github.com/archyl-com/cli",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint("/register"), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := f.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("reach %s: %w", f.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		ClientID         string `json:"client_id"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ClientID == "" {
		if out.ErrorDescription != "" {
			return "", fmt.Errorf("register the CLI with %s: %s", f.Host, out.ErrorDescription)
		}
		return "", fmt.Errorf("register the CLI with %s: unexpected response (HTTP %d)", f.Host, resp.StatusCode)
	}
	return out.ClientID, nil
}

func (f *Flow) exchange(ctx context.Context, clientID string, form url.Values) (*credentials.Credential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint("/oauth/token"), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", buildinfo.UserAgent())

	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", f.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode token response (HTTP %d): %w", resp.StatusCode, err)
	}
	if out.AccessToken == "" {
		if out.Error == "invalid_grant" {
			return nil, ErrRefreshRejected
		}
		return nil, fmt.Errorf("token exchange failed: %s", firstNonEmpty(out.ErrorDescription, out.Error, resp.Status))
	}

	return &credentials.Credential{
		AccessToken:  out.AccessToken,
		RefreshToken: out.RefreshToken,
		ClientID:     clientID,
		ExpiresAt:    time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
	}, nil
}

type callbackResult struct {
	code string
	err  error
}

func callbackHandler(state string, results chan<- callbackResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		var result callbackResult
		switch {
		case query.Get("state") != state:
			result.err = errors.New("sign-in response did not match this request (state mismatch)")
		case query.Get("error") != "":
			result.err = fmt.Errorf("sign-in was refused: %s", firstNonEmpty(query.Get("error_description"), query.Get("error")))
		case query.Get("code") == "":
			result.err = errors.New("sign-in response carried no authorization code")
		default:
			result.code = query.Get("code")
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// A wrong state did not come from this sign-in: another process or a
		// web page probing loopback ports must not end it.
		if query.Get("state") != state {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, callbackPage("Sign-in failed", "Go back to your terminal for details."))
			return
		}
		if result.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, callbackPage("Sign-in failed", "Go back to your terminal for details."))
		} else {
			_, _ = fmt.Fprint(w, callbackPage("You're signed in", "You can close this tab and return to your terminal."))
		}

		select {
		case results <- result:
		default:
		}
	})
	return mux
}

func callbackPage(title, body string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>Archyl CLI</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#09090b;color:#fafafa;font:16px/1.5 ui-sans-serif,system-ui,sans-serif}
main{text-align:center}h1{font-size:20px;margin:0 0 8px}p{color:#a1a1aa;margin:0}b{color:#818cf8}</style></head>
<body><main><p><b>◆ archyl</b></p><h1>` + title + `</h1><p>` + body + `</p></main></body></html>`
}

func randomString(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
