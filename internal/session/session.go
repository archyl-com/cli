// Package session resolves who the CLI is talking to and as whom: the active
// context, its stored credential, and the environment overrides CI relies on.
package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/config"
	"github.com/archyl-com/cli/internal/credentials"
)

// ErrNotLoggedIn is returned when no credential is available.
var ErrNotLoggedIn = errors.New("not logged in")

// HostMismatchError is returned when the environment points at a host other
// than the one the stored credential was issued for.
type HostMismatchError struct {
	Stored string
	Asked  string
}

func (e *HostMismatchError) Error() string {
	return fmt.Sprintf("the stored sign-in is for %s, so it was not sent to %s: run archyl login --host %s, or set ARCHYL_API_KEY",
		e.Stored, e.Asked, e.Asked)
}

// Session is a resolved context with its credential.
type Session struct {
	Name           string
	Host           string
	AppURL         string
	OrganizationID string

	store    credentials.Store
	flow     *auth.Flow
	lockPath string
	// storedHost is the host of the context, the only one its stored
	// credential may be sent to. ARCHYL_HOST can come from a committed agent
	// configuration or a leftover export, so it never gets to spend it.
	storedHost string

	mu   sync.Mutex
	cred *credentials.Credential
}

// Options overrides parts of the resolution.
type Options struct {
	// Context names the context to use; empty means the current one.
	Context string
}

// Resolve builds the session. Environment variables win over the stored
// configuration, so CI can run with ARCHYL_API_KEY alone:
//
//	ARCHYL_API_KEY      an API key (skips stored credentials)
//	ARCHYL_HOST         API base URL (ARCHYL_API_URL is accepted too); a host
//	                    other than the context's needs ARCHYL_API_KEY as well
//	ARCHYL_ORG_ID       organization ID
//	ARCHYL_APP_URL      web app URL, when it cannot be derived from the host
//	ARCHYL_PROFILE      profile name
func Resolve(dir string, opts Options) (*Session, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	name := opts.Context
	if name == "" {
		name = os.Getenv("ARCHYL_PROFILE")
	}
	name, ctx, _ := cfg.Resolve(name)
	if name == "" {
		name = config.DefaultContext
	}

	storedHost := firstNonEmpty(ctx.Host, config.DefaultHost)
	s := &Session{
		Name:           name,
		Host:           firstNonEmpty(os.Getenv("ARCHYL_HOST"), os.Getenv("ARCHYL_API_URL"), storedHost),
		AppURL:         firstNonEmpty(os.Getenv("ARCHYL_APP_URL"), ctx.AppURL),
		OrganizationID: firstNonEmpty(os.Getenv("ARCHYL_ORG_ID"), ctx.OrganizationID),
		store:          credentials.NewStore(dir),
		lockPath:       filepath.Join(dir, "refresh.lock"),
		storedHost:     storedHost,
	}
	s.flow = &auth.Flow{Host: s.Host}

	if key := os.Getenv("ARCHYL_API_KEY"); key != "" {
		s.cred = &credentials.Credential{APIKey: key}
	}
	return s, nil
}

// Credential returns the credential in use, loading it on first call.
func (s *Session) Credential() (*credentials.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Session) load() (*credentials.Credential, error) {
	if s.cred != nil {
		return s.cred, nil
	}
	if !sameHost(s.Host, s.storedHost) {
		return nil, &HostMismatchError{Stored: s.storedHost, Asked: s.Host}
	}
	cred, err := s.store.Get(s.Name)
	if errors.Is(err, credentials.ErrNotFound) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, fmt.Errorf("read stored credential: %w", err)
	}
	s.cred = cred
	return cred, nil
}

// Client returns an API client authenticated as this session.
func (s *Session) Client() *api.Client {
	return api.NewClient(s.Host, s.OrganizationID, s)
}

// Apply implements api.Credentials. An expired OAuth token is refreshed
// before the request rather than after a 401.
func (s *Session) Apply(ctx context.Context, req *http.Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, err := s.load()
	if err != nil {
		return err
	}
	if cred.APIKey != "" {
		req.Header.Set("X-API-Key", cred.APIKey)
		return nil
	}
	if cred.Expired(time.Now()) {
		if cred, err = s.refresh(ctx); err != nil {
			return err
		}
	}
	req.Header.Set("Authorization", "Bearer "+cred.AccessToken)
	return nil
}

// Invalidate implements api.Credentials: after a 401, an OAuth session gets
// one refresh; an API key has nothing to retry with.
func (s *Session) Invalidate(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cred, err := s.load()
	if err != nil || cred.APIKey != "" {
		return false, nil
	}
	if _, err := s.refresh(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// refresh replaces the session's OAuth token. Refresh tokens are single-use,
// so concurrent CLI processes (parallel guard hooks, wrap plus a hook) take
// turns under a lock, and whoever comes second adopts the token the first one
// stored instead of spending the already-rotated refresh token.
func (s *Session) refresh(ctx context.Context) (*credentials.Credential, error) {
	held := s.cred
	unlock, err := acquireLock(ctx, s.lockPath)
	if err != nil {
		return nil, fmt.Errorf("wait for token refresh lock: %w", err)
	}
	defer unlock()

	stored, err := s.store.Get(s.Name)
	if errors.Is(err, credentials.ErrNotFound) {
		s.cred = nil
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, fmt.Errorf("read stored credential: %w", err)
	}
	if stored.AccessToken != held.AccessToken && !stored.Expired(time.Now()) {
		s.cred = stored
		return stored, nil
	}

	fresh, err := s.flow.Refresh(ctx, stored)
	if errors.Is(err, auth.ErrRefreshRejected) {
		return s.rejected(stored)
	}
	if err != nil {
		return nil, err
	}
	if err := s.store.Set(s.Name, fresh); err != nil {
		return nil, fmt.Errorf("store refreshed credential: %w", err)
	}
	s.cred = fresh
	return fresh, nil
}

// rejected signs the context out, unless the stored credential changed while
// the refresh was in flight (a login, or a process that ignored the lock):
// that newer credential is not the one the server refused.
func (s *Session) rejected(spent *credentials.Credential) (*credentials.Credential, error) {
	s.cred = nil
	stored, err := s.store.Get(s.Name)
	if err == nil && stored.RefreshToken != spent.RefreshToken {
		if stored.Expired(time.Now()) {
			return nil, ErrNotLoggedIn
		}
		s.cred = stored
		return stored, nil
	}
	_ = s.store.Delete(s.Name)
	return nil, ErrNotLoggedIn
}

func sameHost(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
