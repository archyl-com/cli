package decide

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditorCommand(
	t *testing.T,
) {
	fallback := []string{"vi"}
	if runtime.GOOS == "windows" {
		fallback = []string{"notepad"}
	}
	testCases := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{name: "VISUAL first", env: map[string]string{"VISUAL": "nvim", "EDITOR": "nano"}, want: []string{"nvim"}},
		{name: "then EDITOR, with its arguments", env: map[string]string{"EDITOR": "code --wait"}, want: []string{"code", "--wait"}},
		{name: "a blank VISUAL is skipped", env: map[string]string{"VISUAL": "  ", "EDITOR": "nano"}, want: []string{"nano"}},
		{
			name: "a quoted path with spaces",
			env:  map[string]string{"EDITOR": `"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl" -w`},
			want: []string{"/Applications/Sublime Text.app/Contents/SharedSupport/bin/subl", "-w"},
		},
		{name: "nothing set", env: map[string]string{}, want: fallback},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			getenv := func(key string) string { return testCase.env[key] }
			assert.Equal(t, testCase.want, EditorCommand(getenv))
		})
	}
}

func TestShellWords(
	t *testing.T,
) {
	testCases := []struct {
		name string
		line string
		want []string
	}{
		{name: "empty", line: "", want: nil},
		{name: "spaces only", line: " \t ", want: nil},
		{name: "plain words", line: "  code  --wait ", want: []string{"code", "--wait"}},
		{name: "single quotes keep everything", line: `'my editor' '-a "b"' 'x\y'`, want: []string{"my editor", `-a "b"`, `x\y`}},
		{name: "double quotes with escapes", line: `"a \"b\" c\\d" "e\f"`, want: []string{`a "b" c\d`, `e\f`}},
		{name: "backslash escapes a space", line: `/opt/My\ Editor/bin/ed -n`, want: []string{"/opt/My Editor/bin/ed", "-n"}},
		{name: "quotes join with the word around them", line: `--opt="a b"c`, want: []string{"--opt=a bc"}},
		{name: "an empty quoted word is a word", line: `ed ""`, want: []string{"ed", ""}},
		{name: "an unclosed quote runs to the end", line: `"/Applications/Sub lime -w`, want: []string{"/Applications/Sub lime -w"}},
		{name: "non-ASCII", line: `"Éditeur" -é`, want: []string{"Éditeur", "-é"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, shellWords(testCase.line))
		})
	}
}

func TestEdit(
	t *testing.T,
) {
	errQuit := errors.New("exit status 1")
	testCases := []struct {
		name    string
		edit    func(path string) error
		want    string
		wantErr string
	}{
		{
			name: "returns what was saved",
			edit: func(path string) error { return os.WriteFile(path, []byte("# Edited\n"), 0o600) },
			want: "# Edited\n",
		},
		{
			name: "closed without saving",
			edit: func(string) error { return nil },
			want: "# Draft\n",
		},
		{
			name: "emptied",
			edit: func(path string) error { return os.WriteFile(path, nil, 0o600) },
			want: "",
		},
		{
			name:    "the editor fails",
			edit:    func(string) error { return errQuit },
			wantErr: "the editor exited with an error: exit status 1",
		},
		{
			name:    "the file is gone",
			edit:    os.Remove,
			wantErr: "read the draft back",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			var opened string
			editor := func(_ context.Context, path string) error {
				opened = path
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "# Draft\n", string(raw))
				return testCase.edit(path)
			}
			got, err := Edit(context.Background(), editor, "# Draft\n")
			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCase.want, got)
			}
			assert.Regexp(t, `archyl-decision-.*\.md$`, opened)
			_, statErr := os.Stat(opened)
			assert.True(t, os.IsNotExist(statErr), "the draft file is removed")
		})
	}
}

func TestSystemEditor(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	path := t.TempDir() + "/draft.md"
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))

	testCases := []struct {
		name    string
		command []string
		want    string
		wantErr bool
	}{
		{name: "arguments come before the file", command: []string{"sh", "-c", `printf edited > "$0"`}, want: "edited"},
		{name: "a failing editor", command: []string{"sh", "-c", "exit 3"}, want: "edited", wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := SystemEditor(testCase.command)(context.Background(), path)
			assert.Equal(t, testCase.wantErr, err != nil)
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, testCase.want, string(raw))
		})
	}
}
