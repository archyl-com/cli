package repo

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// Bounds on what Diff reads of untracked files: a new file's first lines say
// what it is, the rest only costs prompt space, and a directory nobody
// ignored (node_modules) must not be read whole.
const (
	maxUntrackedBytes = 32 * 1024
	untrackedBudget   = 96 * 1024
	maxUntrackedFiles = 40
)

// diffArgs make git's diff output the same whatever the user's config:
// paths unquoted, a/ and b/ prefixes, no color, no external diff tool.
var diffArgs = []string{
	"-c", "core.quotePath=false",
	"diff", "--src-prefix=a/", "--dst-prefix=b/", "--no-color", "--no-ext-diff", "--no-renames",
}

// FileDiff is one file's unified diff.
type FileDiff struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// Diff returns the unified diff of every file that differs from base, the way
// Changes lists them: commits on the branch since it forked from base, staged,
// unstaged and untracked changes. An empty base compares with HEAD only.
//
// Untracked files are read only when they are regular files that do not look
// like secrets, within a small budget: what Diff returns is sent to an AI.
func (g *Git) Diff(ctx context.Context, base string) ([]FileDiff, error) {
	var diffs []FileDiff
	if from := g.forkPoint(ctx, base); from != "" {
		out, err := g.run(ctx, append(append([]string(nil), diffArgs...), from)...)
		if err != nil {
			return nil, err
		}
		diffs = splitDiff(out)
	}
	out, err := g.run(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	budget, files := untrackedBudget, 0
	for _, p := range strings.Split(out, "\x00") {
		if budget <= 0 || files == maxUntrackedFiles {
			break
		}
		if p == "" || !readableUntracked(p) {
			continue
		}
		if d, ok := g.untrackedDiff(p, min(budget, maxUntrackedBytes)); ok {
			diffs = append(diffs, d)
			budget -= len(d.Text)
			files++
		}
	}
	return diffs, nil
}

// readableUntracked reports whether an untracked file may be read for the
// diff: not a likely secret, not inside a dependency or build directory.
func readableUntracked(p string) bool {
	for _, dir := range strings.Split(path.Dir(p), "/") {
		switch dir {
		case "node_modules", "vendor", "dist", "build", "target", ".venv", "venv":
			return false
		}
	}
	return !secretLike(path.Base(p))
}

// secretLike reports whether a file name looks like it holds credentials.
func secretLike(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case ".env", ".envrc", ".netrc", ".npmrc", ".pypirc", ".pgpass", ".htpasswd", ".git-credentials", ".dockercfg",
		"credentials", "credentials.json", "kubeconfig",
		"secrets.yaml", "secrets.yml", "secrets.json",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519":
		return true
	}
	if strings.HasPrefix(lower, ".env.") {
		return true
	}
	if strings.HasSuffix(lower, ".json") && (strings.HasPrefix(lower, "service-account") || strings.HasPrefix(lower, "service_account")) {
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".ppk", ".kdbx", ".tfvars", ".tfstate"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// forkPoint is the commit the working tree is compared with: where the branch
// left base, or HEAD without a base. It is empty in a repository with no
// commit yet.
func (g *Git) forkPoint(ctx context.Context, base string) string {
	head := g.Head(ctx)
	if head == "" || base == "" {
		return head
	}
	out, err := g.run(ctx, "merge-base", base, "HEAD")
	if err != nil {
		return head
	}
	return strings.TrimSpace(out)
}

// untrackedDiff writes a new file as a diff that adds it, reading at most
// limit bytes. Only regular files are read, so a symlink never leads outside
// the work tree; binaries are skipped.
func (g *Git) untrackedDiff(p string, limit int) (FileDiff, bool) {
	full := filepath.Join(g.Root, filepath.FromSlash(p))
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return FileDiff{}, false
	}
	f, err := os.Open(full)
	if err != nil {
		return FileDiff{}, false
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil || bytes.IndexByte(content, 0) >= 0 {
		return FileDiff{}, false
	}
	return FileDiff{Path: p, Text: newFileDiff(p, string(content))}, true
}

// newFileDiff is the diff git would print when the file is added.
func newFileDiff(path, content string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\nnew file\n--- /dev/null\n+++ b/" + path + "\n")
	if content == "" {
		return b.String()
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	b.WriteString("@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
	for _, line := range lines {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

// splitDiff cuts git's output into one diff per file.
func splitDiff(out string) []FileDiff {
	const marker = "diff --git "
	var diffs []FileDiff
	var block strings.Builder
	flush := func() {
		if text := block.String(); strings.TrimSpace(text) != "" {
			diffs = append(diffs, FileDiff{Path: diffPath(text), Text: text})
		}
		block.Reset()
	}
	for _, line := range strings.SplitAfter(out, "\n") {
		if strings.HasPrefix(line, marker) {
			flush()
		}
		block.WriteString(line)
	}
	flush()
	return diffs
}

// diffPath reads the file's path from a diff: the new side, or the old one
// when the file was deleted. git ends a path holding a space with a tab.
func diffPath(text string) string {
	var oldPath string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			return strings.TrimRight(strings.TrimPrefix(line, "+++ b/"), "\t")
		case strings.HasPrefix(line, "--- a/"):
			oldPath = strings.TrimRight(strings.TrimPrefix(line, "--- a/"), "\t")
		case strings.HasPrefix(line, "@@"):
			if oldPath != "" {
				return oldPath
			}
		}
	}
	if oldPath != "" {
		return oldPath
	}
	// A binary or mode-only change has no ---/+++ lines: read the header,
	// "diff --git a/<path> b/<path>".
	header, _, _ := strings.Cut(text, "\n")
	if _, b, ok := strings.Cut(header, " b/"); ok {
		return b
	}
	return ""
}
