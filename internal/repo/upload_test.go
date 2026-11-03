package repo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadForUpload(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "id")
	require.NoError(t, os.WriteFile(secret, []byte("key"), 0o600))

	write := func(name, content string) {
		full := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	write("main.go", "package main")
	write(".env.production", "A=1")
	write("secrets.yaml", "a: 1")
	write("blob.bin", "a\x00b")
	write("big.txt", "12345678901")
	require.NoError(t, os.Symlink(secret, filepath.Join(root, "notes.md")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))

	testCases := []struct {
		rel  string
		want SkipReason
	}{
		{rel: "main.go"},
		{rel: ".env.production", want: SkipSecret},
		{rel: "secrets.yaml", want: SkipSecret},
		{rel: "blob.bin", want: SkipBinary},
		{rel: "big.txt", want: SkipTooLarge},
		{rel: "notes.md", want: SkipLink},
		{rel: "linked/id", want: SkipLink},
		{rel: "missing.go", want: SkipUnreadable},
	}

	for _, testCase := range testCases {
		t.Run(testCase.rel, func(t *testing.T) {
			raw, reason := ReadForUpload(root, testCase.rel, 10+2)
			if testCase.rel == "big.txt" {
				raw, reason = ReadForUpload(root, testCase.rel, 10)
			}
			assert.Equal(t, testCase.want, reason)
			assert.Equal(t, testCase.want == "", raw != nil)
		})
	}
}

func TestParseNameStatusKeepsUnusualPaths(t *testing.T) {
	into := map[string]string{}
	parseNameStatus("M\x00héllo wörld.go\x00A\x00a\"b.go\x00D\x00gone.go\x00", into)
	assert.Equal(t, map[string]string{"héllo wörld.go": "modified", "a\"b.go": "added", "gone.go": "deleted"}, into)
}
