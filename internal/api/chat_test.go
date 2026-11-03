package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatStreamToolCalls(
	t *testing.T,
) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"content\":\"Looking.\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"tool_call\",\"id\":\"call_1\",\"name\":\"brief\",\"args\":\"a.go\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"tool_call\",\"id\":\"\",\"name\":\"broken\"}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var events []ChatEvent
	err := NewClient(server.URL, "", nil).ChatStream(context.Background(), ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "what?"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_0", Name: "impact"}}},
			{Role: "tool", ToolCallID: "call_0", Content: "out"},
		},
		Actions:     []ClientAction{{Name: "brief", Description: "Brief"}},
		NativeTools: true,
	}, func(e ChatEvent) { events = append(events, e) })
	require.NoError(t, err)

	assert.Equal(t, []ChatEvent{
		{Text: "Looking."},
		{ToolCall: &ToolCall{ID: "call_1", Name: "brief", Args: "a.go"}},
	}, events, "a tool call without an id is dropped")
	assert.Equal(t, true, body["nativeTools"])
	msgs := body["messages"].([]any)
	assert.Equal(t, "call_0", msgs[2].(map[string]any)["toolCallId"])
	assert.NotContains(t, msgs[0].(map[string]any), "toolCalls", "plain messages keep the old shape")
}
