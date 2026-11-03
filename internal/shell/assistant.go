package shell

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/tui"
)

// The assistant asks for commands in one of two ways, depending on the
// organization's model: native tool calls (tool_call events, answered with
// "tool" messages, several per turn) or the ACTION text protocol (one
// "ACTION: name args" reply at a time, answered with a user message). The
// session handles whichever arrives.

const (
	// maxToolCallsPerQuestion bounds the commands native tool calling runs
	// before the assistant must answer; they may come several at a time.
	maxToolCallsPerQuestion = 8
	// maxRounds stops a model that keeps asking once it is refused.
	maxRounds = maxActionsPerQuestion + 2
	// The chat API's limits on a conversation.
	maxConversationMessages = 50
	maxConversationBytes    = 120 << 10
)

// toolBatch is the tool calls of one assistant turn: read-only ones run
// together, then writes one at a time, each confirmed.
type toolBatch struct {
	calls   []api.ToolCall
	results map[string]string
	running int
	writes  []api.ToolCall
}

// stream sends the conversation and follows the answer.
func (m *Model) stream() tea.Cmd {
	if m.o.Client == nil {
		return printLine(m.s.warn.Render("  Sign in first: ") + m.s.accent.Render("/login"))
	}
	m.busy = "thinking"
	m.live.Reset()
	m.calls = nil
	m.save()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch := make(chan any, 64)
	m.chunks = ch
	req := api.ChatRequest{
		ProjectID:   m.o.ProjectID,
		Messages:    fitConversation(offeredOnly(m.conversation)),
		Actions:     Actions(),
		NativeTools: true,
	}
	client := m.o.Client
	go func() {
		err := client.ChatStream(ctx, req, func(e api.ChatEvent) {
			if e.ToolCall != nil {
				ch <- toolCallMsg{call: *e.ToolCall}
				return
			}
			ch <- chunkMsg{text: e.Text}
		})
		ch <- streamDone{err: err}
	}()
	return m.waitChunk()
}

func (m *Model) waitChunk() tea.Cmd {
	ch := m.chunks
	return func() tea.Msg { return <-ch }
}

// fitConversation keeps the most recent messages the chat API accepts,
// starting at a question.
func fitConversation(conversation []api.ChatMessage) []api.ChatMessage {
	start, size := len(conversation), 0
	for i := len(conversation) - 1; i >= 0; i-- {
		size += len(conversation[i].Content)
		for _, call := range conversation[i].ToolCalls {
			size += len(call.Args)
		}
		if len(conversation)-i > maxConversationMessages || size > maxConversationBytes {
			break
		}
		start = i
	}
	for start < len(conversation)-1 && conversation[start].Role != "user" {
		start++
	}
	return conversation[start:]
}

// offeredOnly drops tool calls to actions the session no longer offers, and
// their results: a conversation resumed after an upgrade may hold some, and
// the API refuses them.
func offeredOnly(conversation []api.ChatMessage) []api.ChatMessage {
	offered := map[string]bool{}
	for _, a := range Actions() {
		offered[a.Name] = true
	}
	dropped := map[string]bool{}
	out := make([]api.ChatMessage, 0, len(conversation))
	for _, msg := range conversation {
		if msg.Role == "tool" && dropped[msg.ToolCallID] {
			continue
		}
		if len(msg.ToolCalls) > 0 {
			var kept []api.ToolCall
			for _, call := range msg.ToolCalls {
				if offered[call.Name] {
					kept = append(kept, call)
				} else {
					dropped[call.ID] = true
				}
			}
			msg.ToolCalls = kept
		}
		out = append(out, msg)
	}
	return out
}

// stop cancels the answer or the commands in progress. Tool calls left
// without a result get one, so the conversation stays well-formed.
func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
	}
	for _, r := range m.running {
		r.cancel()
	}
	m.busy = ""
	m.running = nil
	m.live.Reset()
	m.calls = nil
	if m.batch != nil {
		m.settleBatch("Stopped by the user.")
	}
}

func (m *Model) finishStream(err error) (tea.Model, tea.Cmd) {
	if m.busy == "" {
		return m, nil // stopped by the user
	}
	reply, calls := m.live.String(), m.calls
	m.busy = ""
	m.live.Reset()
	m.calls = nil
	if err != nil {
		return m, printLine(m.chatError(err))
	}

	if len(calls) > 0 {
		return m.startTools(reply, calls)
	}

	if name, args, ok := ParseAction(reply); ok {
		m.conversation = append(m.conversation, api.ChatMessage{Role: "assistant", Content: strings.TrimSpace(reply)})
		// Every action counts, unknown or refused ones too: each round is a
		// paid AI call, and injected text can ask for them forever.
		m.actionsUsed++
		if m.actionsUsed > maxActionsPerQuestion+1 {
			m.save()
			return m, printLine(m.s.muted.Render("  The assistant kept asking for commands; stopped."))
		}
		if m.actionsUsed > maxActionsPerQuestion {
			m.conversation = append(m.conversation, api.ChatMessage{Role: "user", Content: "You have run enough commands. Answer now with what you have."})
			return m, m.stream()
		}
		cmd, ok := Lookup(name)
		if !ok || !cmd.AI {
			m.conversation = append(m.conversation, api.ChatMessage{Role: "user", Content: fmt.Sprintf("There is no action %q. Use one from the list, or answer.", name)})
			return m, m.stream()
		}
		if _, err := cmd.ModelArgs(args); err != nil {
			m.conversation = append(m.conversation, api.ChatMessage{Role: "user", Content: "Not run: " + err.Error()})
			return m, m.stream()
		}
		if cmd.Writes {
			m.confirm = &pending{cmd: cmd, args: args, shown: time.Now()}
			return m, nil
		}
		return m, m.runForAI(cmd, args)
	}

	m.conversation = append(m.conversation, api.ChatMessage{Role: "assistant", Content: reply})
	m.record("answer", reply)
	return m, printLine(m.renderAnswer(reply))
}

func (m *Model) renderAnswer(reply string) string {
	return "\n" + tui.Markdown(reply, min(m.width-4, 100), m.o.Dark)
}

func (m *Model) chatError(err error) string {
	var apiErr *api.Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden && strings.Contains(strings.ToLower(apiErr.Message), "plan") {
		return m.s.warn.Render("  The assistant is not on your plan.") + m.s.muted.Render(" /commands still work — /help lists them.")
	}
	return m.s.danger.Render("  ✗ " + err.Error())
}

// runForAI runs an action asked for in the text protocol.
func (m *Model) runForAI(c Command, args string) tea.Cmd {
	m.record("action", label(c, args))
	return tea.Batch(printLine(m.s.accent.Render("  ⏺ ")+m.s.text.Render(label(c, args))), m.run(c, args, true))
}

// startTools runs the tool calls of an assistant turn.
func (m *Model) startTools(reply string, calls []api.ToolCall) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(reply)
	var cmds []tea.Cmd
	if text != "" {
		m.record("answer", text)
		cmds = append(cmds, printLine(m.renderAnswer(text)))
	}

	// A call to an action that does not exist cannot go back to the API,
	// which only accepts calls to the actions offered: the assistant hears
	// about it in a message instead.
	var known []api.ToolCall
	var unknown []string
	for _, call := range calls {
		if cmd, ok := Lookup(call.Name); ok && cmd.AI {
			known = append(known, call)
		} else {
			unknown = append(unknown, call.Name)
		}
	}
	m.rounds++
	if len(known) == 0 || m.rounds > maxRounds {
		if text != "" {
			m.conversation = append(m.conversation, api.ChatMessage{Role: "assistant", Content: text})
		}
		if m.rounds > maxRounds {
			m.save()
			return m, tea.Batch(append(cmds, printLine(m.s.muted.Render("  The assistant kept asking for commands; stopped.")))...)
		}
		m.conversation = append(m.conversation, api.ChatMessage{Role: "user",
			Content: fmt.Sprintf("There is no action %q. Use one from the list, or answer.", strings.Join(unknown, ", "))})
		return m, tea.Batch(append(cmds, m.stream())...)
	}

	m.conversation = append(m.conversation, api.ChatMessage{Role: "assistant", Content: text, ToolCalls: known})
	b := &toolBatch{calls: known, results: map[string]string{}}
	m.batch = b
	for _, call := range known {
		cmd, _ := Lookup(call.Name)
		_, argsErr := cmd.ModelArgs(call.Args)
		switch {
		case m.actionsUsed >= maxToolCallsPerQuestion:
			b.results[call.ID] = "Not run: enough commands for this question. Answer now with what you have."
		case argsErr != nil:
			m.actionsUsed++
			b.results[call.ID] = "Not run: " + argsErr.Error()
		case cmd.Writes:
			m.actionsUsed++
			b.writes = append(b.writes, call)
		default:
			m.actionsUsed++
			b.running++
			cmds = append(cmds, m.runCall(cmd, call.Args, true, call.ID))
		}
	}
	if b.running == 0 {
		cmds = append(cmds, m.nextWrite())
	}
	return m, tea.Batch(cmds...)
}

// finishTool records a tool's output and moves the batch on.
func (m *Model) finishTool(msg cmdDone) (tea.Model, tea.Cmd) {
	b := m.batch
	if !m.doneRunning(msg.id) || b == nil {
		return m, nil // stopped by the user
	}
	m.record("action", msg.label)
	b.results[msg.callID] = Observation(strings.TrimSpace(msg.name), msg.output, msg.err)
	b.running--
	printed := printLine(m.s.accent.Render("  ⏺ ") + m.s.text.Render(msg.label) + "\n" + m.preview(msg.output))
	if b.running > 0 {
		return m, printed
	}
	return m, tea.Batch(printed, m.nextWrite())
}

// nextWrite asks about the next write of the batch, or, when none is left,
// sends every result back to the assistant.
func (m *Model) nextWrite() tea.Cmd {
	b := m.batch
	if len(b.writes) > 0 {
		call := b.writes[0]
		b.writes = b.writes[1:]
		cmd, _ := Lookup(call.Name)
		m.confirm = &pending{cmd: cmd, args: call.Args, callID: call.ID, shown: time.Now()}
		return nil
	}
	m.settleBatch("")
	return m.stream()
}

// settleBatch appends the batch's results to the conversation, in the order
// of the calls; calls without one get fallback.
func (m *Model) settleBatch(fallback string) {
	b := m.batch
	m.batch = nil
	for _, call := range b.calls {
		result, ok := b.results[call.ID]
		if !ok {
			result = fallback
		}
		m.conversation = append(m.conversation, api.ChatMessage{Role: "tool", ToolCallID: call.ID, Content: result})
	}
	m.save()
}

// approve runs the write awaiting confirmation.
func (m *Model) approve() tea.Cmd {
	p := m.confirm
	m.confirm = nil
	if p.callID == "" {
		return m.runForAI(p.cmd, p.args)
	}
	m.batch.running++
	return m.runCall(p.cmd, p.args, true, p.callID)
}

// decline tells the assistant the user said no.
func (m *Model) decline() tea.Cmd {
	p := m.confirm
	m.confirm = nil
	m.record("note", "declined "+label(p.cmd, p.args))
	printed := printLine(m.s.muted.Render("  ⏺ declined " + label(p.cmd, p.args)))
	refusal := fmt.Sprintf("I declined `%s %s`. Answer without it.", p.cmd.Name, p.args)
	if p.callID == "" {
		m.conversation = append(m.conversation, api.ChatMessage{Role: "user", Content: refusal})
		return tea.Batch(printed, m.stream())
	}
	m.batch.results[p.callID] = refusal
	return tea.Batch(printed, m.nextWrite())
}
