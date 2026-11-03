package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// The machine's git config (commit signing, hooks, templates) must not
	// leak into the fixture.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// TestChanges covers every way a file can differ from the base: committed on
// the branch, staged, unstaged, deleted and untracked.
func TestChanges(
	t *testing.T,
) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "keep.go"), "a")
	write(t, filepath.Join(dir, "gone.go"), "a")
	write(t, filepath.Join(dir, "edit.go"), "a")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "base")

	gitIn(t, dir, "checkout", "-q", "-b", "feature")
	write(t, filepath.Join(dir, "committed.go"), "b")
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-q", "-m", "feature")
	write(t, filepath.Join(dir, "edit.go"), "changed")
	require.NoError(t, os.Remove(filepath.Join(dir, "gone.go")))
	write(t, filepath.Join(dir, "pkg", "new.go"), "c")

	g, err := Open(context.Background(), dir)
	require.NoError(t, err)

	testCases := []struct {
		name string
		base string
		want []Change
	}{
		{
			name: "against main",
			base: "main",
			want: []Change{
				{Path: "committed.go", Status: "added"},
				{Path: "edit.go", Status: "modified"},
				{Path: "gone.go", Status: "deleted"},
				{Path: "pkg/new.go", Status: "added"},
			},
		},
		{
			name: "working tree only",
			base: "",
			want: []Change{
				{Path: "edit.go", Status: "modified"},
				{Path: "gone.go", Status: "deleted"},
				{Path: "pkg/new.go", Status: "added"},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			changes, err := g.Changes(context.Background(), testCase.base)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, changes)
		})
	}

	assert.Equal(t, "main", g.DefaultBase(context.Background()))
}

func TestLinkRoundTrip(
	t *testing.T,
) {
	dir := t.TempDir()
	_, err := ReadLink(dir)
	assert.ErrorIs(t, err, ErrNotLinked)

	link := &Link{ProjectID: "p1", ProjectName: "Payments"}
	require.NoError(t, WriteLink(dir, link))
	got, err := ReadLink(dir)
	require.NoError(t, err)
	assert.Equal(t, link, got)
}

func TestWriteLinkKeepsUnknownKeys(
	t *testing.T,
) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, LinkFile), `{"projectId":"old","apiKey":"secret"}`)

	require.NoError(t, WriteLink(dir, &Link{ProjectID: "new"}))

	raw, err := os.ReadFile(filepath.Join(dir, LinkFile))
	require.NoError(t, err)
	assert.JSONEq(t, `{"projectId":"new","apiKey":"secret"}`, string(raw))
}

func TestFindRoot(
	t *testing.T,
) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	worktree := t.TempDir()
	write(t, filepath.Join(worktree, ".git"), "gitdir: /elsewhere\n")
	write(t, filepath.Join(root, "a", "b", "file.go"), "package b\n")
	outside := t.TempDir()

	testCases := []struct {
		name    string
		dir     string
		env     map[string]string
		want    string
		wantErr error
	}{
		{
			name: "at the root",
			dir:  root,
			want: root,
		},
		{
			name: "in a subdirectory",
			dir:  filepath.Join(root, "a", "b"),
			want: root,
		},
		{
			name: "linked worktree with a .git file",
			dir:  worktree,
			want: worktree,
		},
		{
			name:    "outside a repository",
			dir:     outside,
			wantErr: ErrNotARepository,
		},
		{
			name:    "GIT_DIR is left to git",
			dir:     root,
			env:     map[string]string{"GIT_DIR": filepath.Join(root, ".git")},
			wantErr: ErrNotARepository,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("GIT_DIR", "")
			t.Setenv("GIT_WORK_TREE", "")
			for k, v := range testCase.env {
				t.Setenv(k, v)
			}
			got, err := FindRoot(testCase.dir)
			if testCase.wantErr != nil {
				assert.ErrorIs(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}
