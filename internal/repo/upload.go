package repo

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// binaryProbe is how much of a file is searched for a NUL, as git does.
const binaryProbe = 8000

// SkipReason says why ReadForUpload left a file out.
type SkipReason string

// Reasons a file is not uploaded.
const (
	SkipUnreadable SkipReason = "unreadable"
	SkipLink       SkipReason = "symbolic link"
	SkipSecret     SkipReason = "looks like a secret"
	SkipTooLarge   SkipReason = "too large"
	SkipBinary     SkipReason = "binary"
)

// SecretPath reports whether a repository path looks like it holds credentials.
func SecretPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	return secretLike(path.Base(rel)) || strings.HasSuffix(rel, ".docker/config.json")
}

// ReadForUpload reads a repository file that is about to leave the machine. A
// repository can be someone else's, and in one a link named notes.md can point
// at ~/.ssh/id_ed25519 or at .git/config, where CI keeps its token: only regular
// files reached without leaving root are read, and secrets by name are refused.
// reason is empty when raw holds the file.
func ReadForUpload(root, rel string, limit int64) (raw []byte, reason SkipReason) {
	if SecretPath(rel) {
		return nil, SkipSecret
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if err != nil {
		return nil, SkipUnreadable
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, SkipLink
	}
	if !info.Mode().IsRegular() {
		return nil, SkipUnreadable
	}
	if !insideRoot(root, filepath.Dir(full)) {
		return nil, SkipLink
	}
	if info.Size() > limit {
		return nil, SkipTooLarge
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, SkipUnreadable
	}
	defer func() { _ = f.Close() }()
	raw, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, SkipUnreadable
	}
	if int64(len(raw)) > limit {
		return nil, SkipTooLarge
	}
	if bytes.IndexByte(raw[:min(len(raw), binaryProbe)], 0) >= 0 {
		return nil, SkipBinary
	}
	return raw, ""
}

// insideRoot reports whether dir is under root once links are resolved.
func insideRoot(root, dir string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RefuseLink stops a write that would go through a symbolic link: a repository
// can plant one where the CLI writes, pointing at a file of the user's.
func RefuseLink(path string) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link; it was not written", path)
	}
	return nil
}
