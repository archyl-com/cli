// Package cache keeps small JSON snapshots on disk so fast paths — the shell
// prompt segment, the update notice — never wait on the network.
package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Dir is the cache directory: $XDG_CACHE_HOME/archyl or the OS equivalent.
func Dir() string {
	if dir := os.Getenv("ARCHYL_CACHE_DIR"); dir != "" {
		return dir
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "archyl")
	}
	return filepath.Join(base, "archyl")
}

// Entry wraps a value with the time it was written.
type Entry[T any] struct {
	SavedAt time.Time `json:"savedAt"`
	Value   T         `json:"value"`
}

// Fresh reports whether the entry is younger than ttl.
func (e *Entry[T]) Fresh(ttl time.Duration) bool {
	return e != nil && time.Since(e.SavedAt) < ttl
}

// Load reads a cached value; a missing or unreadable file is a miss.
func Load[T any](name string) (*Entry[T], error) {
	return loadFile[T](filepath.Join(Dir(), name+".json"))
}

// Save writes a value atomically.
func Save[T any](name string, value T) error {
	return saveFile(filepath.Join(Dir(), name+".json"), value)
}

func loadFile[T any](path string) (*Entry[T], error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e Entry[T]
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, nil
	}
	return &e, nil
}

// saveFile writes through a temporary file, so readers never see half a
// snapshot; os.CreateTemp makes it 0600.
func saveFile[T any](path string, value T) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(Entry[T]{SavedAt: time.Now(), Value: value})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	_ = tmp.Close()
	return os.Rename(tmp.Name(), path)
}
