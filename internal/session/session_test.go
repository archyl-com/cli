package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/credentials"
)

// tokenServer accepts each refresh token once, like the real server, and
// rotates it on every successful refresh.
type tokenServer struct {
	mu        sync.Mutex
	valid     map[string]bool
	issued    int
	onRequest func()
}

func newTokenServer(t *testing.T, valid ...string) (*tokenServer, *httptest.Server) {
	t.Helper()
	ts := &tokenServer{valid: map[string]bool{}}
	for _, token := range valid {
		ts.valid[token] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(ts.serve))
	t.Cleanup(srv.Close)
	return ts, srv
}

func (ts *tokenServer) serve(w http.ResponseWriter, r *http.Request) {
	if ts.onRequest != nil {
		ts.onRequest()
	}
	// Widen the window in which an unserialized second refresh would race.
	time.Sleep(20 * time.Millisecond)

	ts.mu.Lock()
	defer ts.mu.Unlock()
	token := r.PostFormValue("refresh_token")
	w.Header().Set("Content-Type", "application/json")
	if !ts.valid[token] {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	delete(ts.valid, token)
	ts.issued++
	next := fmt.Sprintf("refresh-%d", ts.issued)
	ts.valid[next] = true
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  fmt.Sprintf("access-%d", ts.issued),
		"refresh_token": next,
		"expires_in":    3600,
	})
}

func expiredCredential(refreshToken string) *credentials.Credential {
	return &credentials.Credential{
		AccessToken:  "access-old",
		RefreshToken: refreshToken,
		ClientID:     "cli",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}
}

// newTestSession mimics one CLI process: its own store handle (so no
// in-process mutex is shared) over the same config dir.
func newTestSession(dir, host string) *Session {
	return &Session{
		Name:       "default",
		Host:       host,
		storedHost: host,
		store:      &credentials.FileStore{Path: filepath.Join(dir, "credentials.json")},
		flow:       &auth.Flow{Host: host},
		lockPath:   filepath.Join(dir, "refresh.lock"),
	}
}

func bearer(t *testing.T, s *Session) (string, error) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := s.Apply(context.Background(), req); err != nil {
		return "", err
	}
	return req.Header.Get("Authorization"), nil
}

func TestConcurrentRefresh(t *testing.T) {
	testCases := []struct {
		name      string
		processes int
	}{
		{name: "two processes", processes: 2},
		{name: "many parallel hooks", processes: 8},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			ts, srv := newTokenServer(t, "refresh-old")
			seed := &credentials.FileStore{Path: filepath.Join(dir, "credentials.json")}
			require.NoError(t, seed.Set("default", expiredCredential("refresh-old")))

			headers := make([]string, testCase.processes)
			errs := make([]error, testCase.processes)
			var wg sync.WaitGroup
			for i := range testCase.processes {
				wg.Go(func() {
					headers[i], errs[i] = bearer(t, newTestSession(dir, srv.URL))
				})
			}
			wg.Wait()

			for i := range testCase.processes {
				require.NoError(t, errs[i])
				assert.Equal(t, "Bearer access-1", headers[i])
			}
			assert.Equal(t, 1, ts.issued, "the refresh token is spent once")

			stored, err := seed.Get("default")
			require.NoError(t, err)
			assert.Equal(t, "refresh-1", stored.RefreshToken)
			assert.False(t, stored.Expired(time.Now()))
		})
	}
}

func TestRefreshRejected(t *testing.T) {
	newer := &credentials.Credential{
		AccessToken:  "access-login",
		RefreshToken: "refresh-login",
		ClientID:     "cli",
		ExpiresAt:    time.Now().Add(time.Hour),
	}

	testCases := []struct {
		name string
		// duringRefresh runs while the token request is in flight.
		duringRefresh func(store credentials.Store)
		wantHeader    string
		wantErr       error
		wantStored    *credentials.Credential
	}{
		{
			name:    "unchanged stored token signs out",
			wantErr: ErrNotLoggedIn,
		},
		{
			name: "credential replaced meanwhile is kept",
			duringRefresh: func(store credentials.Store) {
				_ = store.Set("default", newer)
			},
			wantHeader: "Bearer access-login",
			wantStored: newer,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			ts, srv := newTokenServer(t)
			s := newTestSession(dir, srv.URL)
			require.NoError(t, s.store.Set("default", expiredCredential("refresh-revoked")))
			if testCase.duringRefresh != nil {
				other := &credentials.FileStore{Path: filepath.Join(dir, "credentials.json")}
				ts.onRequest = func() { testCase.duringRefresh(other) }
			}

			header, err := bearer(t, s)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.wantHeader, header)
			}
			stored, getErr := s.store.Get("default")
			if testCase.wantStored == nil {
				require.ErrorIs(t, getErr, credentials.ErrNotFound)
				return
			}
			require.NoError(t, getErr)
			assert.Equal(t, testCase.wantStored.RefreshToken, stored.RefreshToken)
		})
	}
}

func TestAcquireLock(t *testing.T) {
	testCases := []struct {
		name     string
		existing time.Duration // age of a lock already on disk; 0 = none
		wantErr  error
	}{
		{name: "free", wantErr: nil},
		{name: "stale lock is taken over", existing: 2 * lockStaleAfter, wantErr: nil},
		{name: "held lock waits", existing: time.Second, wantErr: context.DeadlineExceeded},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "refresh.lock")
			if testCase.existing > 0 {
				require.NoError(t, os.WriteFile(path, []byte("other"), 0o600))
				mtime := time.Now().Add(-testCase.existing)
				require.NoError(t, os.Chtimes(path, mtime, mtime))
			}

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			unlock, err := acquireLock(ctx, path)

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				assert.FileExists(t, path, "another process's lock is left alone")
				return
			}
			require.NoError(t, err)
			unlock()
			assert.NoFileExists(t, path)
		})
	}
}

func TestReleaseLockKeepsTakenOverLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refresh.lock")
	unlock, err := acquireLock(context.Background(), path)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("other"), 0o600))
	unlock()

	assert.FileExists(t, path)
}
