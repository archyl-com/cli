package runstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func ev(seq int64, kind string, payload map[string]any) api.RunEvent {
	return api.RunEvent{Seq: seq, Type: kind, Payload: payload}
}

func TestFold(
	t *testing.T,
) {
	s := New()
	s.Apply([]api.RunEvent{
		ev(0, "status", map[string]any{"status": "running"}),
		ev(1, "message_delta", map[string]any{"text": "Looking at "}),
		ev(2, "message_delta", map[string]any{"text": "the refunds code."}),
		ev(3, "tool_use", map[string]any{"name": "Read", "input": map[string]any{"file_path": "internal/refund.go"}}),
		ev(4, "tool_use", map[string]any{"name": "archyl__propose_plan", "input": map[string]any{}}),
		ev(5, "plan", map[string]any{"planId": "p1", "summary": "Two steps", "awaitingApproval": true,
			"steps": []any{map[string]any{"title": "Add the column"}, map[string]any{"title": "Wire the API"}}}),
	})

	require.Len(t, s.Items, 4, "deltas merge into one text item, loop tools are hidden")
	assert.Equal(t, "Looking at the refunds code.", s.Items[1].Body)
	assert.Equal(t, "internal/refund.go", s.Items[2].Body)
	assert.Equal(t, "plan", s.Pending())

	s.Apply([]api.RunEvent{
		ev(5, "plan", map[string]any{"planId": "ignored"}),
		ev(6, "human_response", map[string]any{"kind": "plan", "requestId": "p1", "approved": true}),
		ev(7, "plan_step", map[string]any{"planId": "p1", "step": float64(1), "status": "completed"}),
		ev(8, "question", map[string]any{"questionId": "q1", "question": "Keep the old endpoint?", "options": []any{"Yes", "No", " "}}),
	})
	assert.Equal(t, "p1", s.Plan.ID, "events already seen are skipped")
	assert.Equal(t, "completed", s.Plan.Steps[0].Status)
	assert.Equal(t, "question", s.Pending())
	assert.Equal(t, []string{"Yes", "No"}, s.Question.Options)

	s.Apply([]api.RunEvent{
		ev(9, "human_response", map[string]any{"kind": "question", "requestId": "q1", "answer": "No"}),
		ev(10, "file_change", map[string]any{"path": "internal/refund.go", "status": "modified", "additions": float64(12), "deletions": float64(3)}),
		ev(11, "file_change", map[string]any{"path": "internal/refund.go", "status": "modified", "additions": float64(14), "deletions": float64(3)}),
		ev(12, "result", map[string]any{"status": "succeeded", "pr_url": "https://github.com/acme/shop/pull/9", "branch": "archyl/refunds"}),
	})
	assert.Empty(t, s.Pending())
	require.Len(t, s.Files, 1, "a file changed twice is listed once, with its latest counts")
	assert.Equal(t, 14, s.Files[0].Additions)
	assert.Equal(t, "https://github.com/acme/shop/pull/9", s.PRURL)
	assert.Equal(t, int64(12), s.LastSeq)
}

func TestToolSummary(
	t *testing.T,
) {
	assert.Equal(t, "go test ./... …", toolSummary(map[string]any{"command": "go test ./...\nmore"}))
	assert.Equal(t, `{"x":1}`, toolSummary(map[string]any{"x": 1}))
	assert.Equal(t, "who_owns", shortTool("archyl__who_owns"))
}
