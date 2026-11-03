package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

const fakeExeEnv = "ARCHYL_SHELL_TEST_FAKE_EXE"

// fakeExe is an archyl stand-in that prints the arguments it ran with. It is
// the test binary itself, which TestMain turns into the stand-in, so it runs
// on every OS, unlike a shell script.
func fakeExe(t *testing.T) string {
	t.Helper()
	t.Setenv(fakeExeEnv, "1")
	path, err := os.Executable()
	require.NoError(t, err)
	return path
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func typeText(m tea.Model, text string) tea.Model {
	for _, r := range text {
		if r == ' ' {
			m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
			continue
		}
		m, _ = m.Update(key(string(r)))
	}
	return m
}

// drain runs commands the way Bubble Tea would, collecting what gets
// printed to the scrollback, until nothing is left to do.
func drain(t *testing.T, m tea.Model, cmd tea.Cmd) (tea.Model, []string) {
	t.Helper()
	var printed []string
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0 && steps < 200; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case frameDrawn:
			continue
		case nil:
			continue
		case tickMsg:
			continue
		}
		if seq, ok := sequenced(msg); ok {
			queue = append(queue, seq...)
			continue
		}
		if text := printedText(msg); text != "" {
			printed = append(printed, ansi.Strip(text))
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		queue = append(queue, next)
	}
	return m, printed
}

// sequenced unwraps the message tea.Sequence returns, which is unexported.
func sequenced(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeFor[tea.Cmd]() {
		return nil, false
	}
	cmds := make([]tea.Cmd, v.Len())
	for i := range cmds {
		cmds[i] = v.Index(i).Interface().(tea.Cmd)
	}
	return cmds, true
}

// messages runs cmd and its batches and sequences in order, returning every
// message they produce.
func messages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, messages(c)...)
		}
		return out
	}
	if seq, ok := sequenced(msg); ok {
		var out []tea.Msg
		for _, c := range seq {
			out = append(out, messages(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// printedText extracts what a tea.Println message prints.
func printedText(msg tea.Msg) string {
	s := fmt.Sprintf("%#v", msg)
	if !strings.Contains(s, "printLineMessage") {
		return ""
	}
	raw := fmt.Sprintf("%+v", msg)
	return strings.TrimSuffix(strings.TrimPrefix(raw, "{messageBody:"), "}")
}

func TestSlashCommandRunsInline(
	t *testing.T,
) {
	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), GlobalArgs: []string{"--project", "p1"}})
	var model tea.Model = m
	model = typeText(model, "/imp")
	assert.Contains(t, ansi.Strip(model.View().Content), "/impact", "typing a slash suggests commands")

	model, cmd := model.Update(key("enter"))
	_, printed := drain(t, model, cmd)
	joined := strings.Join(printed, "\n")
	assert.Contains(t, joined, "› /impact", "enter on a partial command completes and runs it")
	assert.Contains(t, joined, "ran: impact --project p1")
	assert.Contains(t, joined, "embedded=1", "commands render for the session, not for a pipe")
}

func TestAssistantRunsActions(
	t *testing.T,
) {
	var mu sync.Mutex
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		requests = append(requests, body)
		n := len(requests)
		mu.Unlock()
		reply := "ACTION: impact"
		if n > 1 {
			reply = "Your branch touches **Payments**."
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range strings.SplitAfter(reply, " ") {
			payload, _ := json.Marshal(map[string]string{"content": part})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: api.NewClient(server.URL, "", nil), ProjectID: "p1"})
	var model tea.Model = m
	model = typeText(model, "what does my branch break?")
	model, cmd := model.Update(key("enter"))
	_, printed := drain(t, model, cmd)

	joined := strings.Join(printed, "\n")
	assert.Contains(t, joined, "⏺ archyl impact", "the action is shown before it runs")
	assert.Contains(t, joined, "ran: impact", "its output is previewed")
	assert.Contains(t, joined, "Your branch touches Payments.", "then the answer, rendered")

	require.Len(t, requests, 2)
	assert.NotEmpty(t, requests[0]["clientActions"], "the assistant is told what it may run")
	msgs := requests[1]["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	assert.Equal(t, "user", last["role"])
	assert.Contains(t, last["content"], "Output of `archyl impact`")
}

func TestWritesAskFirst(
	t *testing.T,
) {
	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t)})
	cmd, ok := Lookup("approve-run")
	require.True(t, ok)
	m.confirm = &pending{cmd: cmd, args: "3f2a"}
	var model tea.Model = m
	assert.Contains(t, ansi.Strip(model.View().Content), "archyl runs approve 3f2a")

	_, next := model.Update(key("n"))
	assert.Nil(t, m.confirm)
	assert.NotNil(t, next)
	assert.Contains(t, m.conversation[len(m.conversation)-1].Content, "I declined", "a refusal goes back to the assistant")
	_ = context.Background()
}

func TestHistory(
	t *testing.T,
) {
	path := filepath.Join(t.TempDir(), "history")
	appendHistory(path, "/impact")
	appendHistory(path, "who owns payments?")
	appendHistory(path, "multi\nline")

	m := New(Options{Theme: ui.NewTheme(true), HistoryPath: path})
	var model tea.Model = m
	model, _ = model.Update(key("up"))
	assert.Equal(t, "who owns payments?", m.input.Value())
	model, _ = model.Update(key("up"))
	assert.Equal(t, "/impact", m.input.Value())
	_, _ = model.Update(key("down"))
	assert.Equal(t, "who owns payments?", m.input.Value())
}

func TestLiveAreaCollapsesBeforeTheAnswerPrints(
	t *testing.T,
) {
	var answer strings.Builder
	for i := range 30 {
		fmt.Fprintf(&answer, "- line %d of a long answer\n", i)
	}
	testCases := []struct {
		name  string
		end   tea.Msg
		print string
	}{
		{
			name:  "answer",
			end:   streamDone{},
			print: "line 29 of a long answer",
		},
		{
			name:  "error",
			end:   streamDone{err: errors.New("connection reset")},
			print: "connection reset",
		},
		{
			name:  "stopped",
			end:   tea.KeyPressMsg{Code: tea.KeyEscape},
			print: "stopped",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			m := New(Options{Theme: ui.NewTheme(true), Client: api.NewClient("http://127.0.0.1:0", "", nil)})
			var model tea.Model = m
			model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
			idle := lipgloss.Height(model.View().Content)

			m.busy = "thinking"
			m.live.WriteString(answer.String())
			require.Greater(t, lipgloss.Height(model.View().Content), idle+15, "the streamed answer fills the live area")

			model, cmd := model.Update(testCase.end)
			assert.Equal(t, idle, lipgloss.Height(model.View().Content), "the live area is back to the prompt and status line")

			msgs := messages(cmd)
			require.NotEmpty(t, msgs)
			assert.IsType(t, frameDrawn{}, msgs[0], "the collapsed frame is drawn before anything prints")
			var printed []string
			for _, msg := range msgs[1:] {
				printed = append(printed, ansi.Strip(printedText(msg)))
			}
			assert.Contains(t, strings.Join(printed, "\n"), testCase.print)
		})
	}
}
