package completion

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func envWith(home string, vars map[string]string) Env {
	return Env{Getenv: func(k string) string { return vars[k] }, Home: home, GOOS: "linux"}
}

func TestDetect(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		vars    map[string]string
		goos    string
		want    Shell
		wantErr bool
	}{
		{name: "zsh", vars: map[string]string{"SHELL": "/bin/zsh"}, want: Zsh},
		{name: "bash from homebrew", vars: map[string]string{"SHELL": "/opt/homebrew/bin/bash"}, want: Bash},
		{name: "fish", vars: map[string]string{"SHELL": "/usr/bin/fish"}, want: Fish},
		{name: "pwsh", vars: map[string]string{"SHELL": "/usr/local/bin/pwsh"}, want: PowerShell},
		{name: "windows without SHELL", goos: "windows", want: PowerShell},
		{name: "unknown", vars: map[string]string{"SHELL": "/bin/tcsh"}, wantErr: true},
		{name: "unset", wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := envWith("/home/dev", testCase.vars)
			if testCase.goos != "" {
				env.GOOS = testCase.goos
			}
			got, err := Detect(env)
			if testCase.wantErr {
				assert.ErrorIs(t, err, ErrUnknownShell)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestTargetFor(
	t *testing.T,
) {
	home := t.TempDir()
	ownFpath := filepath.Join(home, ".zsh", "completions")
	require.NoError(t, os.MkdirAll(ownFpath, 0o755))

	testCases := []struct {
		name   string
		shell  Shell
		vars   map[string]string
		fpath  []string
		want   string
		rcFile string
	}{
		{
			name:  "bash",
			shell: Bash,
			want:  filepath.Join(home, ".local", "share", "bash-completion", "completions", "archyl"),
		},
		{
			name:  "bash under XDG_DATA_HOME",
			shell: Bash,
			vars:  map[string]string{"XDG_DATA_HOME": "/xdg/data"},
			want:  filepath.Join("/xdg/data", "bash-completion", "completions", "archyl"),
		},
		{
			name:  "fish",
			shell: Fish,
			want:  filepath.Join(home, ".config", "fish", "completions", "archyl.fish"),
		},
		{
			name:  "zsh with a writable fpath directory of the user's",
			shell: Zsh,
			fpath: []string{"/usr/share/zsh/site-functions", ownFpath},
			want:  filepath.Join(ownFpath, "_archyl"),
		},
		{
			name:   "zsh falls back to ~/.zfunc",
			shell:  Zsh,
			fpath:  []string{"/usr/share/zsh/site-functions"},
			want:   filepath.Join(home, ".zfunc", "_archyl"),
			rcFile: filepath.Join(home, ".zshrc"),
		},
		{
			name:   "powershell",
			shell:  PowerShell,
			want:   filepath.Join(home, ".config", "powershell", "archyl.ps1"),
			rcFile: filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := envWith(home, testCase.vars)
			if testCase.fpath != nil {
				env.Fpath = func() []string { return testCase.fpath }
			}
			target := TargetFor(testCase.shell, env)
			assert.Equal(t, testCase.want, target.Path)
			assert.Equal(t, testCase.rcFile, target.RCFile)
		})
	}
}

func TestInstallIsIdempotent(
	t *testing.T,
) {
	root := &cobra.Command{Use: "archyl"}
	root.AddCommand(&cobra.Command{Use: "map", Run: func(*cobra.Command, []string) {}})
	home := t.TempDir()

	for _, shell := range Shells {
		t.Run(string(shell), func(t *testing.T) {
			target := TargetFor(shell, envWith(home, nil))
			script, err := Script(root, shell)
			require.NoError(t, err)
			require.Contains(t, string(script), "archyl")

			action, err := Install(target, script)
			require.NoError(t, err)
			assert.Equal(t, "created", action)

			action, err = Install(target, script)
			require.NoError(t, err)
			assert.Equal(t, "unchanged", action)

			action, err = Install(target, append(script, '\n'))
			require.NoError(t, err)
			assert.Equal(t, "updated", action)
		})
	}
}

func TestAddRCLine(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		existing *string
		want     string
	}{
		{
			name: "no rc file yet",
			want: "# archyl shell completion\nLINE\n",
		},
		{
			name:     "appends after a blank line",
			existing: ptr("export A=1\n"),
			want:     "export A=1\n\n# archyl shell completion\nLINE\n",
		},
		{
			name:     "file without a final newline",
			existing: ptr("export A=1"),
			want:     "export A=1\n\n# archyl shell completion\nLINE\n",
		},
		{
			name:     "already there",
			existing: ptr("LINE\n"),
			want:     "LINE\n",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rc := filepath.Join(t.TempDir(), ".zshrc")
			if testCase.existing != nil {
				require.NoError(t, os.WriteFile(rc, []byte(*testCase.existing), 0o644))
			}
			target := Target{RCFile: rc, RCLine: "LINE"}
			require.NoError(t, AddRCLine(target))
			require.NoError(t, AddRCLine(target), "a second run changes nothing")
			got, err := os.ReadFile(rc)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, string(got))
			assert.False(t, NeedsRCLine(target))
		})
	}
}

func ptr(s string) *string { return &s }
