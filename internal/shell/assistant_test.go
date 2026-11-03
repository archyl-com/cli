package shell

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// chatServer is a fake chat API: reply returns the SSE events of the nth
// request (1-based), as JSON values.
type chatServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []map[string]any
}

func newChatServer(t *testing.T, reply func(n int) []any) *chatServer {
	t.Helper()
	s := &chatServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.requests = append(s.requests, body)
		n := len(s.requests)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range reply(n) {
			payload, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *chatServer) messages(n int) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, m := range s.requests[n-1]["messages"].([]any) {
		out = append(out, m.(map[string]any))
	}
	return out
}

func text(s string) map[string]string { return map[string]string{"content": s} }

func toolCall(id, name, args string) map[string]string {
	return map[string]string{"type": "tool_call", "id": id, "name": name, "args": args}
}

func ask(t *testing.T, model tea.Model, question string) (tea.Model, []string) {
	t.Helper()
	model = typeText(model, question)
	model, cmd := model.Update(key("enter"))
	return drain(t, model, cmd)
}

func TestNativeToolCalls(
	t *testing.T,
) {
	server := newChatServer(t, func(n int) []any {
		switch n {
		case 1:
			return []any{text("Let me look."), toolCall("call_1", "impact", ""), toolCall("call_2", "brief", "a.go b.go"), toolCall("call_3", "remember", "keep v1")}
		default:
			return []any{text("Your branch touches **Payments**.")}
		}
	})

	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: api.NewClient(server.URL, "", nil), ProjectID: "p1"})
	model, printed := ask(t, m, "what does my branch break?")

	joined := strings.Join(printed, "\n")
	assert.Contains(t, joined, "Let me look.", "text before the calls is shown")
	assert.Contains(t, joined, "⏺ archyl impact\n    ran: impact", "each read-only call is shown with its output")
	assert.Contains(t, joined, "⏺ archyl brief a.go b.go")
	require.NotNil(t, m.confirm, "the write waits for a yes")
	assert.Equal(t, "call_3", m.confirm.callID)
	assert.Contains(t, ansi.Strip(model.View().Content), "archyl remember --kind note keep v1")

	model, cmd := model.Update(key("y"))
	_, printed = drain(t, model, cmd)
	joined = strings.Join(printed, "\n")
	assert.Contains(t, joined, "ran: remember --kind note -- keep v1", "the model's arguments come after --")
	assert.Contains(t, joined, "Your branch touches Payments.")

	require.Len(t, server.requests, 2)
	assert.Equal(t, true, server.requests[0]["nativeTools"], "the session asks for native tools")
	msgs := server.messages(2)
	require.Len(t, msgs, 5)
	assistant := msgs[1]
	assert.Equal(t, "assistant", assistant["role"])
	assert.Equal(t, "Let me look.", assistant["content"])
	assert.Len(t, assistant["toolCalls"], 3)
	for i, id := range []string{"call_1", "call_2", "call_3"} {
		result := msgs[2+i]
		assert.Equal(t, "tool", result["role"])
		assert.Equal(t, id, result["toolCallId"], "results follow the order of the calls")
	}
	assert.Contains(t, msgs[3]["content"], "Output of `archyl brief a.go b.go`")
	assert.Contains(t, msgs[4]["content"], "ran: remember")
}

func TestNativeToolCallsRefused(
	t *testing.T,
) {
	server := newChatServer(t, func(n int) []any {
		if n == 1 {
			return []any{toolCall("call_1", "rm-rf", "/"), toolCall("call_2", "approve-run", "3f2a")}
		}
		return []any{text("Fine.")}
	})

	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: api.NewClient(server.URL, "", nil)})
	model, _ := ask(t, m, "approve it")
	require.NotNil(t, m.confirm)

	model, cmd := model.Update(key("n"))
	_, printed := drain(t, model, cmd)
	assert.Contains(t, strings.Join(printed, "\n"), "declined archyl runs approve 3f2a")

	msgs := server.messages(2)
	require.Len(t, msgs, 3)
	assert.Len(t, msgs[1]["toolCalls"], 1, "a call to an unknown action is not sent back")
	assert.Contains(t, msgs[2]["content"], "I declined `approve-run 3f2a`")
}

func TestNativeToolCallsUnknown(
	t *testing.T,
) {
	server := newChatServer(t, func(n int) []any {
		if n == 1 {
			return []any{toolCall("call_1", "rm-rf", "/")}
		}
		return []any{text("Sorry.")}
	})

	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: api.NewClient(server.URL, "", nil)})
	_, printed := ask(t, m, "clean up")
	assert.Contains(t, strings.Join(printed, "\n"), "Sorry.")

	msgs := server.messages(2)
	require.Len(t, msgs, 2)
	assert.Equal(t, "user", msgs[1]["role"])
	assert.Contains(t, msgs[1]["content"], `There is no action "rm-rf"`)
}

func TestOfferedOnly(
	t *testing.T,
) {
	got := offeredOnly([]api.ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []api.ToolCall{{ID: "a", Name: "impact"}, {ID: "b", Name: "retired"}}},
		{Role: "tool", ToolCallID: "a", Content: "ok"},
		{Role: "tool", ToolCallID: "b", Content: "old"},
	})
	assert.Equal(t, []api.ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []api.ToolCall{{ID: "a", Name: "impact"}}},
		{Role: "tool", ToolCallID: "a", Content: "ok"},
	}, got)
}

func TestStopSettlesToolCalls(
	t *testing.T,
) {
	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t)})
	m.conversation = []api.ChatMessage{{Role: "user", Content: "q"}}
	cmd, _ := Lookup("approve-run")
	m.batch = &toolBatch{calls: []api.ToolCall{{ID: "a", Name: "impact"}, {ID: "b", Name: "approve-run"}}, results: map[string]string{"a": "done"}}
	cancelled := false
	m.busy, m.running = "running", []runningCmd{{id: 1, label: "archyl impact", cancel: func() { cancelled = true }}}
	_ = cmd

	m.stop()
	require.Len(t, m.conversation, 3)
	assert.Equal(t, api.ChatMessage{Role: "tool", ToolCallID: "a", Content: "done"}, m.conversation[1])
	assert.Equal(t, api.ChatMessage{Role: "tool", ToolCallID: "b", Content: "Stopped by the user."}, m.conversation[2])
	assert.Nil(t, m.batch)
	assert.True(t, cancelled, "the running command is cancelled")

	_, next := m.Update(cmdDone{id: 1, label: "archyl impact", callID: "a"})
	assert.Nil(t, next, "a command that finishes after a stop is ignored")
}

func TestMultilineInput(
	t *testing.T,
) {
	m := New(Options{Theme: ui.NewTheme(true), Files: func() []string { return []string{"internal/refund.go"} }})
	var model tea.Model = m
	model = typeText(model, "first")
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	model = typeText(model, "second")
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	model = typeText(model, "third")
	model, _ = model.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	model, _ = model.Update(tea.PasteMsg{Content: "pasted\nblock"})
	assert.Equal(t, "first\nsecond\nthird\npasted\nblock", m.input.Value(), "newlines go in, pasted text stays intact")

	model = typeText(model, " @refu")
	assert.Contains(t, ansi.Strip(model.View().Content), "@internal/refund.go", "mentions still complete on a later line")
	model, _ = model.Update(key("tab"))
	assert.True(t, strings.HasSuffix(m.input.Value(), "block @internal/refund.go "))

	_, _ = model.Update(key("up"))
	assert.Equal(t, 3, m.input.Line(), "up moves between lines before it reaches the history")
}

func TestMentionAttachesFiles(
	t *testing.T,
) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "refund.go"), []byte("package refund\n\nfunc Refund() {}\n"), 0o600))
	server := newChatServer(t, func(int) []any { return []any{text("Noted.")} })

	m := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: api.NewClient(server.URL, "", nil), Root: root})
	_, printed := ask(t, m, "what does @refund.go do?")

	assert.Contains(t, strings.Join(printed, "\n"), "attached refund.go (3 lines)")
	content := server.messages(1)[0]["content"].(string)
	assert.Contains(t, content, "look at them with brief or show", "the architectural brief hint stays")
	assert.Contains(t, content, "<file path=\"refund.go\" lines=\"3 lines\">\npackage refund\n\nfunc Refund() {}\n</file>")
}

func TestResume(
	t *testing.T,
) {
	dir := t.TempDir()
	server := newChatServer(t, func(int) []any { return []any{text("Payments is owned by **@payments**.")} })
	client := api.NewClient(server.URL, "", nil)

	first := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: client, SessionDir: dir})
	ask(t, first, "who owns payments?")

	resumed := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: client, SessionDir: dir, Resume: true})
	_, printed := drain(t, resumed, resumed.Init())
	joined := strings.Join(printed, "\n")
	assert.Contains(t, joined, "Resumed who owns payments?")
	assert.Contains(t, joined, "› who owns payments?", "the earlier turns are printed back")
	assert.Contains(t, joined, "Payments is owned by @payments.")
	assert.Len(t, resumed.conversation, 2)

	ask(t, resumed, "and checkout?")
	assert.Len(t, server.messages(2), 3, "the restored conversation is sent with the next question")
	assert.Len(t, listTranscripts(dir), 1, "resuming continues the same conversation")

	fresh := New(Options{Theme: ui.NewTheme(true), Exe: fakeExe(t), Client: client, SessionDir: dir})
	var model tea.Model = fresh
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model = typeText(model, "/resume")
	model, _ = model.Update(key("enter"))
	assert.Contains(t, ansi.Strip(model.View().Content), "who owns payments?", "/resume lists the conversations")
	model, cmd := model.Update(key("enter"))
	_, printed = drain(t, model, cmd)
	assert.Contains(t, strings.Join(printed, "\n"), "Resumed who owns payments?")
	assert.Len(t, fresh.conversation, 4)
}

func TestFitConversation(
	t *testing.T,
) {
	big := strings.Repeat("a", 30<<10)
	conversation := []api.ChatMessage{
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: big},
		{Role: "assistant", ToolCalls: []api.ToolCall{{ID: "a", Name: "impact"}}},
		{Role: "tool", ToolCallID: "a", Content: big},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "next"},
	}
	fitted := fitConversation(conversation)
	assert.Equal(t, "user", fitted[0].Role, "it starts at a question")
	assert.Len(t, fitted, 5)
}
