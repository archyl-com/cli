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

func TestFilesKeepsNamesIntact(
	t *testing.T,
) {
	dir := t.TempDir()
	init := exec.Command("git", "init", "-q")
	init.Dir = dir
	init.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	require.NoError(t, init.Run())

	names := []string{"café/x.go", "my folder/a b.go", " lead.go", "plain.go"}
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("package x\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.go\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ignored.go"), nil, 0o600))

	g := &Git{Root: dir}
	files, err := g.Files(context.Background())
	require.NoError(t, err)
	assert.ElementsMatch(t, append(names, ".gitignore"), files, "untracked files arrive unquoted, ignored ones are left out")

	under, err := g.Files(context.Background(), "my folder")
	require.NoError(t, err)
	assert.Equal(t, []string{"my folder/a b.go"}, under)
}
