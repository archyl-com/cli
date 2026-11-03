// Package credentials keeps the CLI's secrets in the OS keychain, falling back
// to a 0600 file where no keychain is available (CI runners, headless Linux).
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// ErrNotFound is returned when a context has no stored credential.
var ErrNotFound = errors.New("no credential stored")

const keyringService = "archyl-cli"

// Credential is either an API key or an OAuth token pair.
type Credential struct {
	APIKey       string    `json:"apiKey,omitempty"`
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	ClientID     string    `json:"clientId,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitzero"`
}

// Expired reports whether an OAuth access token is past (or within a minute
// of) its expiry. API keys never report expired here; the server decides.
func (c *Credential) Expired(now time.Time) bool {
	if c.APIKey != "" || c.ExpiresAt.IsZero() {
		return false
	}
	return now.Add(time.Minute).After(c.ExpiresAt)
}

// Store persists credentials per context name.
type Store interface {
	Get(name string) (*Credential, error)
	Set(name string, cred *Credential) error
	Delete(name string) error
}

// NewStore returns the keychain-backed store, or a file store when the
// keychain is disabled with ARCHYL_NO_KEYRING=1.
func NewStore(dir string) Store {
	file := &FileStore{Path: filepath.Join(dir, "credentials.json")}
	if os.Getenv("ARCHYL_NO_KEYRING") != "" {
		return file
	}
	return &keyringStore{fallback: file}
}

type keyringStore struct {
	fallback *FileStore
}

func (s *keyringStore) Get(name string) (*Credential, error) {
	raw, err := keyring.Get(keyringService, name)
	if err != nil {
		return s.fallback.Get(name)
	}
	var cred Credential
	if err := json.Unmarshal([]byte(raw), &cred); err != nil {
		return nil, fmt.Errorf("decode stored credential: %w", err)
	}
	return &cred, nil
}

func (s *keyringStore) Set(name string, cred *Credential) error {
	raw, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, name, string(raw)); err != nil {
		return s.fallback.Set(name, cred)
	}
	// A credential left in the file would shadow nothing but would outlive a
	// logout from the keychain; keep a single copy.
	_ = s.fallback.Delete(name)
	return nil
}

func (s *keyringStore) Delete(name string) error {
	err := keyring.Delete(keyringService, name)
	fileErr := s.fallback.Delete(name)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return err
	}
	return fileErr
}

// FileStore keeps credentials in a JSON file readable only by the user.
type FileStore struct {
	Path string
	mu   sync.Mutex
}

func (s *FileStore) read() (map[string]*Credential, error) {
	all := map[string]*Credential{}
	raw, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("decode %s: %w", s.Path, err)
	}
	return all, nil
}

func (s *FileStore) write(all map[string]*Credential) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	// Write then rename, so another CLI process reading concurrently sees the
	// old file or the new one, never a truncated one. CreateTemp makes it 0600.
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), filepath.Base(s.Path)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

func (s *FileStore) Get(name string) (*Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.read()
	if err != nil {
		return nil, err
	}
	cred, ok := all[name]
	if !ok {
		return nil, ErrNotFound
	}
	return cred, nil
}

func (s *FileStore) Set(name string, cred *Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.read()
	if err != nil {
		return err
	}
	all[name] = cred
	return s.write(all)
}

func (s *FileStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := all[name]; !ok {
		return nil
	}
	delete(all, name)
	return s.write(all)
}
