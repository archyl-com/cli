package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// lockStaleAfter outlives the 30s token request timeout, so only a lock
	// left behind by a crashed process is ever taken over.
	lockStaleAfter = 45 * time.Second
	lockPoll       = 50 * time.Millisecond
)

// acquireLock takes an exclusive lock shared by every CLI process using dir.
// It relies on O_CREATE|O_EXCL alone, which behaves the same on macOS, Linux
// and Windows. The returned function releases it.
func acquireLock(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	owner := lockOwner()

	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := f.WriteString(owner)
			closeErr := f.Close()
			if err := errors.Join(writeErr, closeErr); err != nil {
				_ = os.Remove(path)
				return nil, err
			}
			return func() { releaseLock(path, owner) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}

		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > lockStaleAfter {
			_ = os.Remove(path)
			continue
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// releaseLock removes the lock only while it is still ours: after a stale
// takeover it belongs to another process.
func releaseLock(path, owner string) {
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != owner {
		return
	}
	_ = os.Remove(path)
}

func lockOwner() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
