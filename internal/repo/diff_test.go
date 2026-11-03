package repo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitDiff(
	t *testing.T,
) {
	modified := "diff --git a/api/orders.go b/api/orders.go\nindex 1..2 100644\n--- a/api/orders.go\n+++ b/api/orders.go\n@@ -1 +1 @@\n-a\n+b\n"
	deleted := "diff --git a/old.go b/old.go\ndeleted file mode 100644\n--- a/old.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n"
	binary := "diff --git a/logo.png b/logo.png\nindex 1..2 100644\nBinary files a/logo.png and b/logo.png differ\n"
	// Captured from git 2.x: a path with a space ends with a tab on the ---
	// and +++ lines; with core.quotePath=false a non-ASCII path is written
	// as is.
	spaced := "diff --git a/docs/my notes.md b/docs/my notes.md\nindex 1..2 100644\n--- a/docs/my notes.md\t\n+++ b/docs/my notes.md\t\n@@ -1 +1 @@\n-a\n+b\n"
	accented := "diff --git a/café/menü.go b/café/menü.go\nindex 1..2 100644\n--- a/café/menü.go\n+++ b/café/menü.go\n@@ -1 +1 @@\n-a\n+b\n"
	deletedSpaced := "diff --git a/old file.go b/old file.go\ndeleted file mode 100644\n--- a/old file.go\t\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n"
	// A line that merely mentions the marker must not split the diff.
	quoted := "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n+run diff --git a/x b/x\n"

	testCases := []struct {
		name string
		out  string
		want []FileDiff
	}{
		{name: "empty", out: "", want: nil},
		{
			name: "one file per block",
			out:  modified + deleted + binary,
			want: []FileDiff{
				{Path: "api/orders.go", Text: modified},
				{Path: "old.go", Text: deleted},
				{Path: "logo.png", Text: binary},
			},
		},
		{
			name: "spaces and non-ASCII in paths",
			out:  spaced + accented + deletedSpaced,
			want: []FileDiff{
				{Path: "docs/my notes.md", Text: spaced},
				{Path: "café/menü.go", Text: accented},
				{Path: "old file.go", Text: deletedSpaced},
			},
		},
		{
			name: "the marker inside a hunk",
			out:  quoted,
			want: []FileDiff{{Path: "README.md", Text: quoted}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, splitDiff(testCase.out))
		})
	}
}

func TestNewFileDiff(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "lines are added",
			content: "package a\n\nfunc A() {}\n",
			want:    "diff --git a/a.go b/a.go\nnew file\n--- /dev/null\n+++ b/a.go\n@@ -0,0 +1,3 @@\n+package a\n+\n+func A() {}\n",
		},
		{
			name:    "no trailing newline",
			content: "x",
			want:    "diff --git a/a.go b/a.go\nnew file\n--- /dev/null\n+++ b/a.go\n@@ -0,0 +1,1 @@\n+x\n",
		},
		{
			name:    "empty file",
			content: "",
			want:    "diff --git a/a.go b/a.go\nnew file\n--- /dev/null\n+++ b/a.go\n",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, newFileDiff("a.go", testCase.content))
		})
	}
}

func TestDiffArgs(
	t *testing.T,
) {
	for _, flag := range []string{"core.quotePath=false", "--src-prefix=a/", "--dst-prefix=b/", "--no-color", "--no-ext-diff"} {
		assert.Contains(t, diffArgs, flag, "the user's config must not change the output")
	}
	assert.Equal(t, []string{"-c", "core.quotePath=false", "diff"}, diffArgs[:3], "-c goes before the subcommand")
}

func TestSecretLike(
	t *testing.T,
) {
	testCases := []struct {
		name string
		want bool
	}{
		{name: ".env", want: true},
		{name: ".env.local", want: true},
		{name: "id_rsa", want: true},
		{name: "id_ed25519.pub", want: false},
		{name: "id_card.go", want: false},
		{name: "secrets.yaml", want: true},
		{name: "prod.tfvars", want: true},
		{name: "server.PEM", want: true},
		{name: "tls.key", want: true},
		{name: ".netrc", want: true},
		{name: "environment.go", want: false},
		{name: "keys.go", want: false},
		{name: "README.md", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, secretLike(testCase.name))
		})
	}
}

// newUntrackedRepo is a repository with no commit, so every file in it is
// untracked. Nothing is ever committed.
func newUntrackedRepo(t *testing.T) *Git {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	out, err := exec.Command("git", "init", "-q", root).CombinedOutput()
	require.NoError(t, err, string(out))
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return &Git{Root: resolved}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func TestDiffUntracked(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	g := newUntrackedRepo(t)
	outside := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(outside, []byte("PRIVATE KEY"), 0o600))

	writeFile(t, g.Root, "docs/my notes.md", "spaced\n")
	writeFile(t, g.Root, "café/menü.go", "package café\n")
	writeFile(t, g.Root, ".env", "TOKEN=secret\n")
	writeFile(t, g.Root, "certs/server.pem", "-----BEGIN-----\n")
	writeFile(t, g.Root, "node_modules/left-pad/index.js", "module.exports = 1\n")
	writeFile(t, g.Root, "logo.png", "\x89PNG\x00")
	require.NoError(t, os.Symlink(outside, filepath.Join(g.Root, "notes")))

	diffs, err := g.Diff(context.Background(), "")
	require.NoError(t, err)
	var paths []string
	for _, d := range diffs {
		paths = append(paths, d.Path)
		assert.NotContains(t, d.Text, "PRIVATE KEY")
		assert.NotContains(t, d.Text, "secret")
	}
	assert.ElementsMatch(t, []string{"docs/my notes.md", "café/menü.go"}, paths)
}

func TestDiffUntrackedBudget(
	t *testing.T,
) {
	g := newUntrackedRepo(t)
	big := strings.Repeat("a line of a new file\n", maxUntrackedBytes/10)
	for i := range maxUntrackedFiles + 10 {
		writeFile(t, g.Root, fmt.Sprintf("src/f%03d.go", i), big)
	}

	diffs, err := g.Diff(context.Background(), "")
	require.NoError(t, err)
	total := 0
	for _, d := range diffs {
		total += len(d.Text)
	}
	assert.LessOrEqual(t, len(diffs), maxUntrackedFiles)
	assert.Less(t, len(diffs), maxUntrackedFiles, "the byte budget stops the reading first")
	// Only the diff's own marks (+ per line, the header) go past the budget.
	assert.LessOrEqual(t, total, untrackedBudget*11/10, "each file is read within the remaining budget")
}
