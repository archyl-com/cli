package commands

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/session"
	"github.com/archyl-com/cli/internal/ui"
)

// modelAPI serves a project's model named after the system; status, when set,
// fails every request, and slow holds them until the client gives up.
func modelAPI(t *testing.T, system string, status int, slow bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow {
			<-r.Context().Done()
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"nope"}`)
			return
		}
		var body any
		switch {
		case strings.HasSuffix(r.URL.Path, "/c4"):
			body = api.Model{Systems: []api.System{{ID: "s", Name: system}}}
		case strings.HasSuffix(r.URL.Path, "/adrs"):
			body = map[string]any{"data": []api.ADR{{ID: "a", Number: 1, Title: "Use Go"}}}
		default:
			body = api.Project{ID: "p1", Name: "Demo"}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestLoadModel(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		cached      bool
		cachedAge   time.Duration
		noCache     bool
		status      int
		slow        bool
		want        string
		wantErr     bool
		wantInCache string
	}{
		{name: "cold fetch fills the cache", want: "Fresh", wantInCache: "Fresh"},
		{name: "recent snapshot skips the API", cached: true, cachedAge: time.Second, slow: true, want: "Cached", wantInCache: "Cached"},
		{name: "older snapshot revalidates", cached: true, cachedAge: time.Minute, want: "Fresh", wantInCache: "Fresh"},
		{name: "slow API falls back to the cache", cached: true, cachedAge: time.Minute, slow: true, want: "Cached", wantInCache: "Cached"},
		{name: "server error falls back to the cache", cached: true, cachedAge: time.Minute, status: http.StatusBadGateway, want: "Cached", wantInCache: "Cached"},
		{name: "lost access drops the cache", cached: true, cachedAge: time.Minute, status: http.StatusForbidden, wantErr: true},
		{name: "--no-cache neither reads nor writes", cached: true, cachedAge: time.Second, noCache: true, want: "Fresh", wantInCache: "Cached"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
			t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
			t.Setenv("ARCHYL_NO_CACHE", "")
			t.Setenv("ARCHYL_HOST", modelAPI(t, "Fresh", testCase.status, testCase.slow))
			t.Setenv("ARCHYL_API_KEY", "test-key")

			app := &App{Printer: &ui.Printer{Out: io.Discard, Err: io.Discard}}
			scope, ok := app.cacheScope()
			require.True(t, ok)
			// Keep the fallback from spawning the test binary as a refresher.
			require.True(t, cache.ClaimRefresh(scope, "p1", time.Hour))
			if testCase.cached {
				require.NoError(t, cache.SaveModel(scope, "p1", cache.ModelSnapshot{
					Project: api.Project{ID: "p1", Name: "Demo"},
					Model:   api.Model{Systems: []api.System{{ID: "s", Name: "Cached"}}},
				}))
				backdateSnapshots(t, testCase.cachedAge)
			}
			app.noCache = testCase.noCache

			snap, err := loadModel(context.Background(), app, "p1")
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, snap.Model.Systems[0].Name)
				assert.Equal(t, "Demo", snap.Project.Name)
			}

			entry, err := cache.LoadModel(scope, "p1")
			require.NoError(t, err)
			if testCase.wantInCache == "" {
				assert.Nil(t, entry)
				return
			}
			require.NotNil(t, entry)
			assert.Equal(t, testCase.wantInCache, entry.Value.Model.Systems[0].Name)
		})
	}
}

// backdateSnapshots ages every snapshot in the test's cache directory.
func backdateSnapshots(t *testing.T, age time.Duration) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(os.Getenv("ARCHYL_CACHE_DIR"), "models", "*", "*", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var entry map[string]any
		require.NoError(t, json.Unmarshal(raw, &entry))
		entry["savedAt"] = time.Now().Add(-age)
		raw, err = json.Marshal(entry)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(f, raw, 0o600))
	}
}

func TestPromptKeyIsScoped(
	t *testing.T,
) {
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_HOST", "http://localhost:1")

	key := func(apiKey string) string {
		t.Setenv("ARCHYL_API_KEY", apiKey)
		app := &App{Printer: &ui.Printer{Out: io.Discard, Err: io.Discard}}
		k, ok := promptKey(app, "p1")
		require.True(t, ok)
		return k
	}
	a, b := key("key-a"), key("key-b")
	assert.NotEqual(t, a, b, "another identity gets another key")
	assert.NotContains(t, a, "p1", "names are hashed")

	require.NoError(t, cache.Save(a, promptState{Project: "Demo"}))
	entry, err := cache.Load[promptState](a)
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "Demo", entry.Value.Project)
	miss, err := cache.Load[promptState](b)
	require.NoError(t, err)
	assert.Nil(t, miss)
}

func TestCacheScopeHidesAPIKey(
	t *testing.T,
) {
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_API_KEY", "secret-key")
	app := &App{Printer: &ui.Printer{Out: io.Discard, Err: io.Discard}}

	scope, ok := app.cacheScope()
	require.True(t, ok)
	assert.NotContains(t, scope.Identity, "secret-key")
	assert.NotEmpty(t, scope.Identity)
}

func TestTransient(
	t *testing.T,
) {
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "timeout", err: context.DeadlineExceeded, want: true},
		{name: "network", err: errors.New("reach http://x: connection refused"), want: true},
		{name: "server error", err: &api.Error{Status: http.StatusServiceUnavailable}, want: true},
		{name: "rate limited", err: &api.Error{Status: http.StatusTooManyRequests}, want: true},
		{name: "forbidden", err: &api.Error{Status: http.StatusForbidden}},
		{name: "not found", err: &api.Error{Status: http.StatusNotFound}},
		{name: "signed out", err: session.ErrNotLoggedIn},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, transient(testCase.err))
		})
	}
}
