package shell

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func TestModelArgs(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		command string
		args    string
		want    []string
		refused bool
	}{
		{name: "an element", command: "diagram", args: "Payments API", want: []string{"diagram", "--format", "text", "-p", "w", "--", "Payments API"}},
		{name: "no element", command: "diagram", want: []string{"diagram", "--format", "text", "-p", "w"}},
		{name: "short flag with a value", command: "diagram", args: "-o=/tmp/x", refused: true},
		{name: "long flag", command: "diagram", args: "--output x", refused: true},
		{name: "a dash dash of its own", command: "diagram", args: "-- -o x", refused: true},
		{name: "a flag among files", command: "brief", args: "a.go -o x", refused: true},
		{name: "files", command: "brief", args: "a.go b.go", want: []string{"brief", "-p", "w", "--", "a.go", "b.go"}},
		{name: "a fact", command: "remember", args: "keep v1", want: []string{"remember", "--kind", "note", "-p", "w", "--", "keep v1"}},
		{name: "a flag after the run", command: "steer-run", args: "3f2a --yes", refused: true},
		{name: "a message with a dash inside", command: "steer-run", args: "3f2a use -v", want: []string{"runs", "steer", "-p", "w", "--", "3f2a", "use -v"}},
		{name: "arguments to a command that takes none", command: "check", args: "--output /tmp/x", refused: true},
		{name: "none to a command that takes none", command: "impact", want: []string{"impact", "-p", "w"}},
		{name: "a range and an element", command: "history", args: "v1.2..v2.0 Billing API", want: []string{"history", "-p", "w", "--", "v1.2..v2.0", "Billing API"}},
		{name: "a point and an element", command: "history", args: "30d Billing", want: []string{"history", "-p", "w", "--", "30d", "Billing"}},
		{name: "an element alone", command: "history", args: "Billing API", want: []string{"history", "-p", "w", "--", "Billing API"}},
		{name: "a range starting with a dash", command: "history", args: "-o x..", refused: true},
	}

	m := New(Options{Theme: ui.NewTheme(true), GlobalArgs: []string{"-p", "w"}})
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			c, ok := Lookup(testCase.command)
			require.True(t, ok)
			argv, err := m.modelArgv(c, testCase.args)
			if testCase.refused {
				require.ErrorIs(t, err, ErrModelArgs)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, argv)
		})
	}
}

func TestEveryActionKeepsModelArgsAfterDashDash(
	t *testing.T,
) {
	m := New(Options{Theme: ui.NewTheme(true)})
	for _, c := range Catalog {
		if !c.AI || c.Split == nil {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			argv, err := m.modelArgv(c, "x y")
			require.NoError(t, err)
			dash := len(c.Base)
			require.Greater(t, len(argv), dash)
			assert.Equal(t, "--", argv[dash], "nothing the model chose comes before --")
		})
	}
}

func TestModelFlagNeverReachesTheCommand(
	t *testing.T,
) {
	server := newChatServer(t, func(n int) []any {
		if n == 1 {
			return []any{toolCall("call_1", "diagram", "-o=/tmp/x"), toolCall("call_2", "remember", "--output x")}
		}
		return []any{text("Fine.")}
	})

	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: api.NewClient(server.URL, "", nil)})
	_, printed := ask(t, m, "draw it")
	assert.Nil(t, m.confirm, "a refused write is not even asked about")
	assert.NotContains(t, strings.Join(printed, "\n"), "ran:", "the command never runs")

	msgs := server.messages(2)
	require.Len(t, msgs, 4)
	assert.Contains(t, msgs[2]["content"], `Not run: refused arguments: "-o=/tmp/x" starts with "-"`)
	assert.Contains(t, msgs[3]["content"], `Not run: refused arguments: "--output x" starts with "-"`)
}

func TestStopKillsTheRunningCommand(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake archyl is a shell script")
	}
	exe := filepath.Join(t.TempDir(), "archyl")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nsleep 30\necho late output\n"), 0o755))

	m := New(Options{Theme: ui.NewTheme(true), Exe: exe})
	c, _ := Lookup("impact")
	cmd := m.run(c, "", false)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	_, next := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Contains(t, strings.Join(printedAll(next), "\n"), "⏹ stopped")

	select {
	case msg := <-done:
		_, after := m.Update(msg)
		assert.Nil(t, after, "a stopped command prints nothing")
	case <-time.After(5 * time.Second):
		t.Fatal("the command kept running after esc")
	}
}

// printedAll is what cmd prints, through batches and sequences.
func printedAll(cmd tea.Cmd) []string {
	var out []string
	for _, msg := range messages(cmd) {
		if text := printedText(msg); text != "" {
			out = append(out, text)
		}
	}
	return out
}
