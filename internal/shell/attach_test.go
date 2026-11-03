package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttach(
	t *testing.T,
) {
	root := t.TempDir()
	outside := t.TempDir()
	write := func(dir, name, content string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	write(root, "small.go", "a\nb\n")
	write(root, "long.go", strings.Repeat("line\n", 1000))
	write(root, "wide.txt", strings.Repeat(strings.Repeat("x", 99)+"\n", 300))
	write(root, "logo.png", "\x89PNG\r\n\x1a\n\x00\x00")
	write(root, "empty.go", "")
	write(root, "pkg/inner.go", "package pkg\n")
	write(outside, "secret.env", "TOKEN=1\n")
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.env"), filepath.Join(root, "link.env")))

	testCases := []struct {
		path     string
		wantNote string
		attached bool
	}{
		{path: "small.go", wantNote: "attached small.go (2 lines)", attached: true},
		{path: "long.go", wantNote: "attached long.go (first 400 of 1000 lines)", attached: true},
		{path: "wide.txt", wantNote: "attached wide.txt (first 245 of 300 lines)", attached: true},
		{path: "pkg/inner.go", wantNote: "attached pkg/inner.go (1 line)", attached: true},
		{path: "logo.png", wantNote: "skipped logo.png (binary)"},
		{path: "empty.go", wantNote: "skipped empty.go (empty)"},
		{path: "pkg", wantNote: "skipped pkg (a directory)"},
		{path: "missing.go", wantNote: "skipped missing.go (not found)"},
		{path: "../" + filepath.Base(outside) + "/secret.env", wantNote: "outside the repository"},
		{path: filepath.Join(outside, "secret.env"), wantNote: "outside the repository"},
		{path: "link.env", wantNote: "skipped link.env (outside the repository)"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			atts := Attach(root, []string{testCase.path}, maxAttachTotal)
			require.Len(t, atts, 1)
			assert.Contains(t, atts[0].Note(), testCase.wantNote)
			assert.Equal(t, testCase.attached, atts[0].Content != "")
			assert.LessOrEqual(t, len(atts[0].Content), maxAttachBytes)
		})
	}
}

func TestAttachSharesABudget(
	t *testing.T,
) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(strings.Repeat(strings.Repeat("x", 79)+"\n", 1500)), 0o600))
	}
	atts := Attach(root, []string{"a.txt", "b.txt", "c.txt", "a.txt"}, maxAttachTotal)
	require.Len(t, atts, 3, "a file mentioned twice is attached once")

	total := 0
	for _, a := range atts {
		total += len(a.Content)
	}
	assert.LessOrEqual(t, total, maxAttachTotal)
	assert.Equal(t, "skipped c.txt (attachment limit reached)", atts[2].Note())

	block := attachmentsBlock(atts)
	assert.Contains(t, block, `<file path="a.txt" lines="lines 1-307 of 1500">`, "the first file takes its share")
	assert.Contains(t, block, `<file path="b.txt" lines="lines 1-51 of 1500">`, "the next one what is left")
	assert.False(t, strings.Contains(block, "c.txt"))
}
