package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/credentials"
)

func TestChallenge(
	t *testing.T,
) {
	// RFC 7636, appendix B.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	assert.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", challenge(verifier))

	sum := sha256.Sum256([]byte("other"))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), challenge("other"))
}

func TestRandomString(
	t *testing.T,
) {
	a, b := randomString(32), randomString(32)
	assert.NotEqual(t, a, b)
	assert.Len(t, a, 43, "32 bytes, base64url without padding")
	assert.Regexp(t, `^[A-Za-z0-9_-]+$`, a)
}

func TestCallbackHandler(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		query       url.Values
		wantCode    string
		wantErr     string
		wantHTTP    int
		wantIgnored bool
	}{
		{
			name:     "code with matching state",
			query:    url.Values{"state": {"s1"}, "code": {"abc"}},
			wantCode: "abc",
			wantHTTP: http.StatusOK,
		},
		{
			name:        "state mismatch",
			query:       url.Values{"state": {"other"}, "code": {"abc"}},
			wantIgnored: true,
			wantHTTP:    http.StatusBadRequest,
		},
		{
			name:        "missing state",
			query:       url.Values{"code": {"abc"}},
			wantIgnored: true,
			wantHTTP:    http.StatusBadRequest,
		},
		{
			name:        "state checked before error",
			query:       url.Values{"state": {"other"}, "error": {"access_denied"}},
			wantIgnored: true,
			wantHTTP:    http.StatusBadRequest,
		},
		{
			name:     "error with description",
			query:    url.Values{"state": {"s1"}, "error": {"access_denied"}, "error_description": {"user said no"}},
			wantErr:  "sign-in was refused: user said no",
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "error without description",
			query:    url.Values{"state": {"s1"}, "error": {"access_denied"}},
			wantErr:  "sign-in was refused: access_denied",
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "no code",
			query:    url.Values{"state": {"s1"}},
			wantErr:  "no authorization code",
			wantHTTP: http.StatusBadRequest,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			results := make(chan callbackResult, 1)
			handler := callbackHandler("s1", results)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?"+testCase.query.Encode(), nil))

			assert.Equal(t, testCase.wantHTTP, rec.Code)
			assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
			if testCase.wantIgnored {
				assert.Empty(t, results, "a stray callback must not end the sign-in")
				return
			}
			result := <-results
			if testCase.wantErr != "" {
				require.Error(t, result.err)
				assert.Contains(t, result.err.Error(), testCase.wantErr)
				assert.Contains(t, rec.Body.String(), "Sign-in failed")
				return
			}
			require.NoError(t, result.err)
			assert.Equal(t, testCase.wantCode, result.code)
			assert.Contains(t, rec.Body.String(), "signed in")
		})
	}
}

func TestCallbackHandlerDeliversOnce(
	t *testing.T,
) {
	results := make(chan callbackResult, 1)
	handler := callbackHandler("s1", results)

	for range 2 {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?state=s1&code=c", nil))
		assert.Equal(t, http.StatusOK, rec.Code, "a second hit must not block")
	}
	assert.Len(t, results, 1)
}

// fakeServer is an OAuth provider recording what the CLI sent.
type fakeServer struct {
	*httptest.Server

	mu            sync.Mutex
	registerBody  map[string]any
	tokenForms    []url.Values
	registerReply func(w http.ResponseWriter)
	tokenReply    func(w http.ResponseWriter, form url.Values)
}

func newFakeServer(
	t *testing.T,
) *fakeServer {
	t.Helper()
	fs := &fakeServer{
		registerReply: func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "client-1"})
		},
		tokenReply: func(w http.ResponseWriter, form url.Values) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"expires_in":    3600,
			})
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fs.mu.Lock()
		fs.registerBody = body
		fs.mu.Unlock()
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NotEmpty(t, r.Header.Get("User-Agent"))
		fs.registerReply(w)
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		fs.mu.Lock()
		fs.tokenForms = append(fs.tokenForms, r.PostForm)
		fs.mu.Unlock()
		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		fs.tokenReply(w, r.PostForm)
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

// browser follows the authorization URL like a user who approves: it calls the
// loopback redirect with a code and the state it was given, or tampers with it.
func browser(
	t *testing.T,
	opened *url.URL,
	tamper func(q url.Values),
) func(string) error {
	return func(raw string) error {
		u, err := url.Parse(raw)
		require.NoError(t, err)
		*opened = *u

		q := u.Query()
		reply := url.Values{"code": {"auth-code"}, "state": {q.Get("state")}}
		if tamper != nil {
			tamper(reply)
		}
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + reply.Encode())
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLogin(
	t *testing.T,
) {
	server := newFakeServer(t)
	var opened url.URL
	var prompted string
	flow := &Flow{
		Host:        server.URL + "/",
		HTTP:        server.Client(),
		OpenBrowser: browser(t, &opened, nil),
		Prompt:      func(u string) { prompted = u },
		Timeout:     5 * time.Second,
	}

	before := time.Now()
	cred, err := flow.Login(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "access-1", cred.AccessToken)
	assert.Equal(t, "refresh-1", cred.RefreshToken)
	assert.Equal(t, "client-1", cred.ClientID)
	assert.WithinDuration(t, before.Add(time.Hour), cred.ExpiresAt, 5*time.Second)
	assert.Equal(t, opened.String(), prompted, "the URL is shown even if the browser opens")

	assert.Equal(t, "/oauth/authorize", opened.Path)
	q := opened.Query()
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "client-1", q.Get("client_id"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Equal(t, Scope, q.Get("scope"))
	assert.NotEmpty(t, q.Get("state"))
	redirect, err := url.Parse(q.Get("redirect_uri"))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", redirect.Hostname(), "loopback redirect, RFC 8252")
	assert.Equal(t, "/callback", redirect.Path)

	server.mu.Lock()
	defer server.mu.Unlock()
	assert.Equal(t, []any{q.Get("redirect_uri")}, server.registerBody["redirect_uris"])
	require.Len(t, server.tokenForms, 1)
	form := server.tokenForms[0]
	assert.Equal(t, "authorization_code", form.Get("grant_type"))
	assert.Equal(t, "auth-code", form.Get("code"))
	assert.Equal(t, "client-1", form.Get("client_id"))
	assert.Equal(t, q.Get("redirect_uri"), form.Get("redirect_uri"))
	assert.Equal(t, q.Get("code_challenge"), challenge(form.Get("code_verifier")), "the verifier matches the challenge sent")
}

func TestLoginFailures(
	t *testing.T,
) {
	testCases := []struct {
		name          string
		tamper        func(q url.Values)
		noBrowser     bool
		registerReply func(w http.ResponseWriter)
		tokenReply    func(w http.ResponseWriter, form url.Values)
		wantErr       string
		wantIs        error
	}{
		{
			name:    "a stray callback does not end the sign-in",
			tamper:  func(q url.Values) { q.Set("state", "forged") },
			wantErr: "timed out",
		},
		{
			name: "user refuses",
			tamper: func(q url.Values) {
				q.Del("code")
				q.Set("error", "access_denied")
			},
			wantErr: "sign-in was refused: access_denied",
		},
		{
			name:      "browser never comes back",
			noBrowser: true,
			wantErr:   "timed out",
		},
		{
			name: "registration refused with description",
			registerReply: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_redirect_uri", "error_description": "bad redirect"})
			},
			wantErr: "register the CLI with",
		},
		{
			name: "registration returns garbage",
			registerReply: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html>"))
			},
			wantErr: "unexpected response (HTTP 502)",
		},
		{
			name: "token exchange invalid_grant",
			tokenReply: func(w http.ResponseWriter, form url.Values) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			},
			wantIs: ErrRefreshRejected,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newFakeServer(t)
			if testCase.registerReply != nil {
				server.registerReply = testCase.registerReply
			}
			if testCase.tokenReply != nil {
				server.tokenReply = testCase.tokenReply
			}
			var opened url.URL
			flow := &Flow{Host: server.URL, HTTP: server.Client(), Timeout: 2 * time.Second}
			if testCase.noBrowser {
				flow.Timeout = 50 * time.Millisecond
			} else {
				flow.OpenBrowser = browser(t, &opened, testCase.tamper)
			}

			cred, err := flow.Login(context.Background())
			require.Error(t, err)
			assert.Nil(t, cred)
			if testCase.wantIs != nil {
				assert.ErrorIs(t, err, testCase.wantIs)
			}
			if testCase.wantErr != "" {
				assert.Contains(t, err.Error(), testCase.wantErr)
			}
			if testCase.tamper != nil {
				server.mu.Lock()
				assert.Empty(t, server.tokenForms, "no code is exchanged after a bad callback")
				server.mu.Unlock()
			}
		})
	}
}

func TestLoginStillWorksWhenBrowserFails(
	t *testing.T,
) {
	server := newFakeServer(t)
	var opened url.URL
	approve := browser(t, &opened, nil)
	flow := &Flow{
		Host:        server.URL,
		HTTP:        server.Client(),
		OpenBrowser: func(string) error { return errors.New("no display") },
		// The user copies the printed URL into a browser by hand.
		Prompt:  func(u string) { _ = approve(u) },
		Timeout: 5 * time.Second,
	}

	cred, err := flow.Login(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "access-1", cred.AccessToken)
}

func TestLoginUnreachableHost(
	t *testing.T,
) {
	server := httptest.NewServer(http.NotFoundHandler())
	host := server.URL
	server.Close()

	flow := &Flow{Host: host, Timeout: time.Second}
	_, err := flow.Login(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reach "+host)
}

func TestRefresh(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		cred       credentials.Credential
		tokenReply func(w http.ResponseWriter, form url.Values)
		wantAccess string
		wantIs     error
		wantErr    string
		wantCalls  int
	}{
		{
			name:       "rotates the pair",
			cred:       credentials.Credential{RefreshToken: "old", ClientID: "client-1"},
			wantAccess: "access-1",
			wantCalls:  1,
		},
		{
			name: "invalid_grant means sign in again",
			cred: credentials.Credential{RefreshToken: "old", ClientID: "client-1"},
			tokenReply: func(w http.ResponseWriter, form url.Values) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "token reused"})
			},
			wantIs:    ErrRefreshRejected,
			wantCalls: 1,
		},
		{
			name: "other OAuth error keeps its description",
			cred: credentials.Credential{RefreshToken: "old", ClientID: "client-1"},
			tokenReply: func(w http.ResponseWriter, form url.Values) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "unknown client"})
			},
			wantErr:   "token exchange failed: unknown client",
			wantCalls: 1,
		},
		{
			name: "error without description falls back to the code",
			cred: credentials.Credential{RefreshToken: "old", ClientID: "client-1"},
			tokenReply: func(w http.ResponseWriter, form url.Values) {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "server_error"})
			},
			wantErr:   "token exchange failed: server_error",
			wantCalls: 1,
		},
		{
			name: "empty JSON falls back to the HTTP status",
			cred: credentials.Credential{RefreshToken: "old", ClientID: "client-1"},
			tokenReply: func(w http.ResponseWriter, form url.Values) {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{}"))
			},
			wantErr:   "503 Service Unavailable",
			wantCalls: 1,
		},
		{
			name: "non-JSON response",
			cred: credentials.Credential{RefreshToken: "old", ClientID: "client-1"},
			tokenReply: func(w http.ResponseWriter, form url.Values) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("<html>"))
			},
			wantErr:   "decode token response (HTTP 502)",
			wantCalls: 1,
		},
		{
			name:   "no refresh token is rejected locally",
			cred:   credentials.Credential{AccessToken: "a", ClientID: "client-1"},
			wantIs: ErrRefreshRejected,
		},
		{
			name:   "no client id is rejected locally",
			cred:   credentials.Credential{RefreshToken: "old"},
			wantIs: ErrRefreshRejected,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newFakeServer(t)
			if testCase.tokenReply != nil {
				server.tokenReply = testCase.tokenReply
			}
			flow := &Flow{Host: server.URL, HTTP: server.Client()}

			cred := testCase.cred
			got, err := flow.Refresh(context.Background(), &cred)

			server.mu.Lock()
			forms := server.tokenForms
			server.mu.Unlock()
			require.Len(t, forms, testCase.wantCalls)
			if testCase.wantCalls > 0 {
				assert.Equal(t, "refresh_token", forms[0].Get("grant_type"))
				assert.Equal(t, cred.RefreshToken, forms[0].Get("refresh_token"))
				assert.Equal(t, cred.ClientID, forms[0].Get("client_id"))
			}

			if testCase.wantIs != nil || testCase.wantErr != "" {
				require.Error(t, err)
				assert.Nil(t, got)
				if testCase.wantIs != nil {
					assert.ErrorIs(t, err, testCase.wantIs)
				}
				if testCase.wantErr != "" {
					assert.Contains(t, err.Error(), testCase.wantErr)
					assert.NotErrorIs(t, err, ErrRefreshRejected)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantAccess, got.AccessToken)
			assert.Equal(t, "refresh-1", got.RefreshToken)
			assert.Equal(t, cred.ClientID, got.ClientID)
		})
	}
}

func TestFlowDefaults(
	t *testing.T,
) {
	flow := &Flow{Host: "https://api.example.com/"}
	assert.Equal(t, "https://api.example.com/oauth/token", flow.endpoint("/oauth/token"))
	assert.Equal(t, 30*time.Second, flow.client().Timeout)

	custom := &http.Client{}
	flow.HTTP = custom
	assert.Same(t, custom, flow.client())
}

func TestFirstNonEmpty(
	t *testing.T,
) {
	assert.Equal(t, "b", firstNonEmpty("", "b", "c"))
	assert.Equal(t, "", firstNonEmpty("", ""))
	assert.Equal(t, "", firstNonEmpty())
}
