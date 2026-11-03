// Package repo reads the local git repository and the file that links it to
// an Archyl project.
package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ErrNotARepository is returned outside a git work tree.
var ErrNotARepository = errors.New("not inside a git repository")

// Git runs git commands in one work tree.
type Git struct {
	Root string
}

// FindRoot finds the work tree containing dir without running git: the
// nearest directory holding a .git entry. It suits hot paths such as the
// shell prompt, which only read a file at the root. It fails when GIT_DIR or
// GIT_WORK_TREE move the repository, which only git resolves.
func FindRoot(dir string) (string, error) {
	if os.Getenv("GIT_DIR") != "" || os.Getenv("GIT_WORK_TREE") != "" {
		return "", ErrNotARepository
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotARepository
		}
		dir = parent
	}
}

// Open finds the work tree containing dir.
func Open(ctx context.Context, dir string) (*Git, error) {
	out, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, ErrNotARepository
	}
	return &Git{Root: strings.TrimSpace(out)}, nil
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (g *Git) run(ctx context.Context, args ...string) (string, error) {
	return run(ctx, g.Root, args...)
}

// Remote returns the origin URL, or "" when there is none.
func (g *Git) Remote(ctx context.Context) string {
	out, err := g.run(ctx, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Head returns the current commit, or "" in an empty repository.
func (g *Git) Head(ctx context.Context) string {
	out, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Branch returns the current branch name.
func (g *Git) Branch(ctx context.Context) string {
	out, err := g.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// DefaultBase guesses the branch a feature branch is compared with: origin's
// HEAD, then main, then master.
func (g *Git) DefaultBase(ctx context.Context) string {
	if out, err := g.run(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimSpace(out)
	}
	for _, candidate := range []string{"origin/main", "main", "origin/master", "master"} {
		if _, err := g.run(ctx, "rev-parse", "--verify", "--quiet", candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// Change is a changed file, path relative to the work tree root.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// Changes lists every file that differs from base: committed on the branch,
// staged, unstaged and untracked. An empty base compares with HEAD only.
func (g *Git) Changes(ctx context.Context, base string) ([]Change, error) {
	byPath := map[string]string{}

	if base != "" {
		out, err := g.run(ctx, "diff", "--name-status", "--no-renames", "-z", "--end-of-options", base+"...HEAD")
		if err != nil {
			return nil, err
		}
		parseNameStatus(out, byPath)
	}
	if g.Head(ctx) != "" {
		out, err := g.run(ctx, "diff", "--name-status", "--no-renames", "-z", "HEAD")
		if err != nil {
			return nil, err
		}
		parseNameStatus(out, byPath)
	}
	out, err := g.run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			byPath[path] = "added"
		}
	}

	changes := make([]Change, 0, len(byPath))
	for path, status := range byPath {
		changes = append(changes, Change{Path: path, Status: status})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// parseNameStatus reads git diff --name-status -z: status and path alternate,
// NUL-separated, and paths arrive unquoted whatever they contain.
func parseNameStatus(out string, into map[string]string) {
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "" {
			continue
		}
		status := "modified"
		switch fields[i][0] {
		case 'A':
			status = "added"
		case 'D':
			status = "deleted"
		}
		into[fields[i+1]] = status
	}
}

// Files lists the tracked and untracked files under the given paths that
// exist in the working tree, relative to the root. Names come NUL-separated
// so non-ASCII names and leading spaces arrive unquoted and intact; files
// deleted but not yet committed are left out.
func (g *Git) Files(ctx context.Context, paths ...string) ([]string, error) {
	listed, err := g.lsFiles(ctx, append([]string{"--cached", "--others", "--exclude-standard"}, paths...)...)
	if err != nil {
		return nil, err
	}
	deleted, err := g.lsFiles(ctx, append([]string{"--deleted"}, paths...)...)
	if err != nil {
		return nil, err
	}
	gone := make(map[string]bool, len(deleted))
	for _, f := range deleted {
		gone[f] = true
	}
	files := listed[:0]
	seen := map[string]bool{}
	for _, f := range listed {
		if !gone[f] && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	return files, nil
}

// lsFiles runs git ls-files -z with flags, then "--" and any paths among
// them after the first non-flag argument.
func (g *Git) lsFiles(ctx context.Context, args ...string) ([]string, error) {
	cmd := []string{"-c", "core.quotePath=false", "ls-files", "-z"}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "--") {
		i++
	}
	cmd = append(cmd, args[:i]...)
	cmd = append(cmd, "--")
	cmd = append(cmd, args[i:]...)
	out, err := g.run(ctx, cmd...)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// Rel turns a path (absolute, or relative to cwd) into one relative to the
// work tree root, with forward slashes.
func (g *Git) Rel(cwd, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	root, err := filepath.EvalSymlinks(g.Root)
	if err != nil {
		root = g.Root
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the repository", path)
	}
	return filepath.ToSlash(rel), nil
}

// Resolves reports whether ref names a commit in this repository.
func (g *Git) Resolves(ctx context.Context, ref string) bool {
	_, err := g.run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// TagAtHead returns the tag pointing at HEAD, or "".
func (g *Git) TagAtHead(ctx context.Context) string {
	out, err := g.run(ctx, "describe", "--tags", "--exact-match", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// HooksDir returns the directory git runs hooks from, honouring core.hooksPath.
func (g *Git) HooksDir(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(out)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(g.Root, dir)
	}
	return dir, nil
}

// Churn counts how many of the last n commits touched each file, a cheap
// measure of where the work happens.
func (g *Git) Churn(ctx context.Context, n int) (map[string]int, error) {
	out, err := g.run(ctx, "log", "-n", strconv.Itoa(n), "--no-merges", "--name-only", "--format=")
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			counts[line]++
		}
	}
	return counts, nil
}
