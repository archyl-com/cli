package dslfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	return dir
}

func TestLoad(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		tree      map[string]string
		file      string
		wantPath  string
		wantFiles []string
		wantErr   string
	}{
		{
			name:     "a file alone",
			tree:     map[string]string{"archyl.yaml": "version: \"1.0\"\n"},
			file:     "archyl.yaml",
			wantPath: "archyl.yaml",
		},
		{
			name: "nested includes resolve against their own file",
			tree: map[string]string{
				"archyl.yaml":                   "version: \"1.0\"\ninclude: [services/archyl.yaml]\n",
				"services/archyl.yaml":          "include: [api/archyl.yaml, ../web.yaml, api/archyl.yaml]\n",
				"services/api/archyl.yaml":      "include: [../../archyl.yaml]\n",
				"web.yaml":                      "containers: []\n",
				"services/unrelated/archyl.yml": "x: 1\n",
			},
			file:      "archyl.yaml",
			wantPath:  "archyl.yaml",
			wantFiles: []string{"services/archyl.yaml", "services/api/archyl.yaml", "web.yaml"},
		},
		{
			name: "a root below the base keeps its path",
			tree: map[string]string{
				"docs/archyl.yaml": "include: [../svc/a.yaml]\n",
				"svc/a.yaml":       "containers: []\n",
			},
			file:      "docs/archyl.yaml",
			wantPath:  "docs/archyl.yaml",
			wantFiles: []string{"svc/a.yaml"},
		},
		{
			name:    "a missing include",
			tree:    map[string]string{"archyl.yaml": "include: [api.yaml]\n"},
			file:    "archyl.yaml",
			wantErr: "archyl.yaml includes api.yaml, which does not exist",
		},
		{
			name:    "an include outside the repository",
			tree:    map[string]string{"archyl.yaml": "include: [../x.yaml]\n"},
			file:    "archyl.yaml",
			wantErr: "outside",
		},
		{
			name: "a doc and a contract body come from the files they name",
			tree: map[string]string{
				"services/archyl.yaml": "version: \"1.0\"\ninclude: [api.yaml]\ndocs:\n  records:\n    - title: Guide\n      file: ./docs/guide.md\n    - title: Inline\n      file: docs/unread.md\n      content: inline wins\n",
				"services/api.yaml":    "api_contracts:\n  - name: Orders\n    file: api/orders.yaml\n",
				"docs/guide.md":        "# Guide\n",
				"api/orders.yaml":      "openapi: 3.1.0\n",
			},
			file:      "services/archyl.yaml",
			wantPath:  "services/archyl.yaml",
			wantFiles: []string{"services/api.yaml", "docs/guide.md", "api/orders.yaml"},
		},
		{
			name:    "a doc naming a missing file",
			tree:    map[string]string{"archyl.yaml": "docs:\n  records:\n    - title: Guide\n      file: docs/guide.md\n"},
			file:    "archyl.yaml",
			wantErr: "archyl.yaml names the file docs/guide.md, which does not exist",
		},
		{
			name:    "a doc naming a file outside the repository",
			tree:    map[string]string{"archyl.yaml": "docs:\n  records:\n    - title: Guide\n      file: ../../etc/passwd\n"},
			file:    "archyl.yaml",
			wantErr: "outside",
		},
		{
			name:    "an empty file",
			tree:    map[string]string{"archyl.yaml": "  \n"},
			file:    "archyl.yaml",
			wantErr: "is empty",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeTree(t, testCase.tree)
			src, err := Load(dir, filepath.Join(dir, testCase.file))
			if testCase.wantErr != "" {
				assert.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantPath, src.Path)
			assert.Equal(t, testCase.tree[testCase.file], src.Content)
			var got []string
			for name, content := range src.Files {
				got = append(got, name)
				assert.Equal(t, testCase.tree[name], content)
			}
			assert.ElementsMatch(t, testCase.wantFiles, got)
		})
	}
}

func TestLoadOutsideBase(
	t *testing.T,
) {
	dir := writeTree(t, map[string]string{"elsewhere/archyl.yaml": "include: [a.yaml]\n", "elsewhere/a.yaml": "x: 1\n"})
	src, err := Load(filepath.Join(dir, "repo"), filepath.Join(dir, "elsewhere", "archyl.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "archyl.yaml", src.Path, "a file outside the base is its own base")
	assert.Contains(t, src.Files, "a.yaml")
}

func TestLoadRefusesSymlinksAndHugeFiles(
	t *testing.T,
) {
	outside := writeTree(t, map[string]string{"secret": "id_rsa"})
	testCases := []struct {
		name    string
		setup   func(t *testing.T, dir string)
		wantErr string
	}{
		{
			name: "a symbolic link to a file outside the repository",
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "a.yaml")))
			},
			wantErr: "not a regular file",
		},
		{
			name: "a file over the size cap",
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "a.yaml"), make([]byte, MaxFileBytes+1), 0o600))
			},
			wantErr: "larger than 5 MiB",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{"archyl.yaml": "include: [a.yaml]\n"})
			testCase.setup(t, dir)
			_, err := Load(dir, filepath.Join(dir, "archyl.yaml"))
			assert.ErrorContains(t, err, testCase.wantErr)
		})
	}
}
