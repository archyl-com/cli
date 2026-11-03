package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

type recorder struct {
	responses []api.RunResponse
	steered   []string
	cancelled bool
}

func runView(rec *recorder) *RunView {
	return NewRunView(ui.NewTheme(true), RunActions{
		Run:    func(context.Context) (*api.Run, error) { return &api.Run{ID: "r1", Status: "waiting_for_input"}, nil },
		Events: func(context.Context, int64) ([]api.RunEvent, error) { return nil, nil },
		Respond: func(_ context.Context, r api.RunResponse) error {
			rec.responses = append(rec.responses, r)
			return nil
		},
		Steer: func(_ context.Context, m string) error {
			rec.steered = append(rec.steered, m)
			return nil
		},
		Cancel: func(context.Context) error {
			rec.cancelled = true
			return nil
		},
	})
}

// run executes a command and feeds its message back, the way Bubble Tea would.
func run(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	if _, isBatch := msg.(tea.BatchMsg); isBatch {
		return m
	}
	m, _ = m.Update(msg)
	return m
}

func typeText(m tea.Model, text string) tea.Model {
	for _, r := range text {
		m, _ = m.Update(key(string(r)))
	}
	return m
}

func TestRunViewQuestionAndPlan(
	t *testing.T,
) {
	rec := &recorder{}
	v := runView(rec)
	var m tea.Model = v
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(runPolled{run: &api.Run{ID: "r1", Status: "waiting_for_input", TaskText: "Add refunds"}, events: []api.RunEvent{
		{Seq: 0, Type: "plan", Payload: map[string]any{"planId": "p1", "awaitingApproval": true, "steps": []any{map[string]any{"title": "Add the column"}}}},
	}})
	assert.Contains(t, screen(t, m), "review its plan")

	m, cmd := m.Update(key("a"))
	m = run(t, m, cmd)
	require.Len(t, rec.responses, 1)
	assert.Equal(t, "p1", rec.responses[0].RequestID)
	assert.True(t, *rec.responses[0].Approved)

	m, _ = m.Update(runPolled{run: &api.Run{ID: "r1", Status: "waiting_for_input"}, events: []api.RunEvent{
		{Seq: 1, Type: "human_response", Payload: map[string]any{"kind": "plan", "requestId": "p1", "approved": true}},
		{Seq: 2, Type: "question", Payload: map[string]any{"questionId": "q1", "question": "Keep the v1 endpoint?", "options": []any{"Yes", "No"}}},
	}})
	assert.Contains(t, screen(t, m), "Keep the v1 endpoint?")

	m, cmd = m.Update(key("2"))
	m = run(t, m, cmd)
	require.Len(t, rec.responses, 2)
	assert.Equal(t, api.RunResponse{RequestID: "q1", Answer: "No"}, rec.responses[1])
	assert.Contains(t, screen(t, m), "answered: No")
}

func TestRunViewSteerAndCancel(
	t *testing.T,
) {
	rec := &recorder{}
	v := runView(rec)
	var m tea.Model = v
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(runPolled{run: &api.Run{ID: "r1", Status: "running"}, events: []api.RunEvent{
		{Seq: 0, Type: "message_delta", Payload: map[string]any{"text": "Reading the refunds module."}},
		{Seq: 1, Type: "tool_use", Payload: map[string]any{"name": "Read", "input": map[string]any{"file_path": "internal/refund.go"}}},
	}})
	view := screen(t, m)
	assert.Contains(t, view, "Reading the refunds module.")
	assert.Contains(t, view, "internal/refund.go")

	m, _ = m.Update(key("s"))
	m = typeText(m, "use the ledger")
	m, cmd := m.Update(key("enter"))
	m = run(t, m, cmd)
	assert.Equal(t, []string{"use the ledger"}, rec.steered)

	m, _ = m.Update(key("x"))
	assert.Contains(t, screen(t, m), "Cancel this run?")
	m, cmd = m.Update(key("n"))
	assert.Nil(t, cmd)
	assert.False(t, rec.cancelled, "anything but y keeps the run")
	m, _ = m.Update(key("x"))
	_, cmd = m.Update(key("y"))
	run(t, m, cmd)
	assert.True(t, rec.cancelled)
}

func TestWrapNeverOverflows(
	t *testing.T,
) {
	for _, line := range wrap("Inspect the project’s C4 model, enumerate every container, and provide a one-sentence description of internal/service/harness/observed_test.go", 20) {
		assert.LessOrEqual(t, len([]rune(line)), 20, line)
	}
}
