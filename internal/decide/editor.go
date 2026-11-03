package decide

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Editor opens a file for a person to edit and returns once they are done.
type Editor func(ctx context.Context, path string) error

// EditorCommand is the editor the person chose: $VISUAL, then $EDITOR, then
// vi (notepad on Windows). It is split the way a shell would, so
// "code --wait" and a quoted path with spaces both work.
func EditorCommand(getenv func(string) string) []string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if words := shellWords(getenv(name)); len(words) > 0 {
			return words
		}
	}
	if runtime.GOOS == "windows" {
		return []string{"notepad"}
	}
	return []string{"vi"}
}

// SystemEditor runs the command on the file, attached to the terminal.
func SystemEditor(command []string) Editor {
	return func(ctx context.Context, path string) error {
		args := append(append([]string(nil), command[1:]...), path)
		cmd := exec.CommandContext(ctx, command[0], args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
}

// Edit writes text to a temporary Markdown file, opens it in the editor and
// returns what was saved. The file is removed afterwards.
func Edit(ctx context.Context, editor Editor, text string) (string, error) {
	f, err := os.CreateTemp("", "archyl-decision-*.md")
	if err != nil {
		return "", fmt.Errorf("create the draft file: %w", err)
	}
	path := f.Name()
	defer func() { _ = os.Remove(path) }()
	_, err = f.WriteString(text)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write the draft file: %w", err)
	}
	if err := editor(ctx, path); err != nil {
		return "", fmt.Errorf("the editor exited with an error: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the draft back: %w", err)
	}
	return string(raw), nil
}

// shellWords splits a command line into words like a POSIX shell, without
// expanding anything: single quotes keep everything, double quotes keep
// everything but a backslash before " or \, and a backslash elsewhere
// escapes the next character. An unclosed quote runs to the end.
func shellWords(line string) []string {
	var words []string
	var word strings.Builder
	inWord := false
	var quote rune
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\'):
				i++
				word.WriteRune(runes[i])
			default:
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '\\' && i+1 < len(runes):
			i++
			word.WriteRune(runes[i])
			inWord = true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}
