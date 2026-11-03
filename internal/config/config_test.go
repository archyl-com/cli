package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDir(
	t *testing.T,
) {
	custom := t.TempDir()
	t.Setenv("ARCHYL_CONFIG_DIR", custom)
	dir, err := Dir()
	require.NoError(t, err)
	assert.Equal(t, custom, dir)

	t.Setenv("ARCHYL_CONFIG_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	dir, err = Dir()
	require.NoError(t, err)
	base, err := os.UserConfigDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(base, "archyl"), dir)
	assert.Contains(t, dir, home, "follows the (isolated) user config dir")
}

func TestDirWithoutHome(
	t *testing.T,
) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("UserConfigDir reads other variables there")
	}
	t.Setenv("ARCHYL_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	_, err := Dir()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "locate configuration directory")
}

func TestLoadMissing(
	t *testing.T,
) {
	f, err := Load(filepath.Join(t.TempDir(), "absent"))
	require.NoError(t, err)
	assert.Empty(t, f.Current)
	assert.NotNil(t, f.Contexts, "the zero configuration is writable")
	_, _, ok := f.Resolve("")
	assert.False(t, ok)
}

func TestSaveLoad(
	t *testing.T,
) {
	dir := filepath.Join(t.TempDir(), "archyl")
	f, err := Load(dir)
	require.NoError(t, err)

	f.Contexts[DefaultContext] = Context{Host: DefaultHost, OrganizationID: "org-1", User: "ada@example.com"}
	f.Contexts["staging"] = Context{Host: "https://staging.example.com", AppURL: "https://app.staging.example.com"}
	f.Current = DefaultContext
	require.NoError(t, f.Save())

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "config.yaml"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	loaded, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, f.Current, loaded.Current)
	assert.Equal(t, f.Contexts, loaded.Contexts)
	assert.Equal(t, []string{"default", "staging"}, loaded.Names())

	// Switching the active context persists.
	loaded.Current = "staging"
	require.NoError(t, loaded.Save())
	again, err := Load(dir)
	require.NoError(t, err)
	name, ctx, ok := again.Resolve("")
	assert.True(t, ok)
	assert.Equal(t, "staging", name)
	assert.Equal(t, "https://staging.example.com", ctx.Host)
}

func TestLoadParse(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		content      string
		wantErr      string
		wantCurrent  string
		wantContexts int
	}{
		{
			name:         "valid",
			content:      "current: prod\ncontexts:\n  prod:\n    host: https://api.archyl.com\n",
			wantCurrent:  "prod",
			wantContexts: 1,
		},
		{
			name:         "empty file",
			content:      "",
			wantContexts: 0,
		},
		{
			name:         "no contexts key",
			content:      "current: prod\n",
			wantCurrent:  "prod",
			wantContexts: 0,
		},
		{
			name:    "corrupt",
			content: "contexts: [not, a, map\n",
			wantErr: "parse",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(testCase.content), 0o600))

			f, err := Load(dir)
			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantCurrent, f.Current)
			assert.Len(t, f.Contexts, testCase.wantContexts)
			assert.NotNil(t, f.Contexts)
		})
	}
}

func TestLoadUnreadable(
	t *testing.T,
) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "config.yaml"), 0o700))
	_, err := Load(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read")
}

func TestSaveUncreatableDir(
	t *testing.T,
) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	f := &File{path: filepath.Join(blocker, "archyl", "config.yaml")}
	err := f.Save()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create configuration directory")
}

func TestResolve(
	t *testing.T,
) {
	f := &File{
		Current: "prod",
		Contexts: map[string]Context{
			"prod":    {Host: "https://api.archyl.com"},
			"staging": {Host: "https://staging.example.com"},
		},
	}
	testCases := []struct {
		name     string
		file     *File
		input    string
		wantName string
		wantHost string
		wantOK   bool
	}{
		{name: "current", file: f, input: "", wantName: "prod", wantHost: "https://api.archyl.com", wantOK: true},
		{name: "explicit wins over current", file: f, input: "staging", wantName: "staging", wantHost: "https://staging.example.com", wantOK: true},
		{name: "unknown explicit", file: f, input: "missing", wantName: "missing", wantOK: false},
		{name: "current points at a removed context", file: &File{Current: "gone", Contexts: f.Contexts}, input: "", wantName: "gone", wantOK: false},
		{name: "nothing configured", file: &File{}, input: "", wantName: "", wantOK: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			name, ctx, ok := testCase.file.Resolve(testCase.input)
			assert.Equal(t, testCase.wantName, name)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.wantHost, ctx.Host)
		})
	}
}

func TestNames(
	t *testing.T,
) {
	assert.Empty(t, (&File{}).Names())
	f := &File{Contexts: map[string]Context{"zeta": {}, "alpha": {}, "mid": {}}}
	assert.Equal(t, []string{"alpha", "mid", "zeta"}, f.Names())
}
