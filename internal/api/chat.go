package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/safehttp"
)

// ToolCall is the assistant asking to run a client action, with native tool
// calling.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// ChatEvent is one piece of an answer: some text, or a tool call.
type ChatEvent struct {
	Text     string
	ToolCall *ToolCall
}

// ChatRequest is a conversation sent to the assistant.
type ChatRequest struct {
	ProjectID string
	Messages  []ChatMessage
	// Actions are local commands the assistant may ask for.
	Actions []ClientAction
	// NativeTools offers the actions as real tools when the organization's
	// model supports them: calls then arrive as ChatEvent.ToolCall, and
	// their output goes back as "tool" messages. Otherwise the assistant
	// asks in text, with ACTION lines.
	NativeTools bool
}

// ChatStream streams the assistant's answer, calling onEvent for each piece.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onEvent func(ChatEvent)) error {
	body := map[string]any{"messages": req.Messages}
	if req.ProjectID != "" {
		body["currentProjectId"] = req.ProjectID
	}
	if len(req.Actions) > 0 {
		body["clientActions"] = req.Actions
		if req.NativeTools {
			body["nativeTools"] = true
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	for attempt := 0; ; attempt++ {
		err := c.chatOnce(ctx, payload, onEvent)
		if attempt > 0 || !IsStatus(err, http.StatusUnauthorized) || c.creds == nil {
			return err
		}
		if retry, refreshErr := c.creds.Invalidate(ctx); refreshErr != nil || !retry {
			return err
		}
	}
}

func (c *Client) chatOnce(ctx context.Context, payload []byte, onEvent func(ChatEvent)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/api/v1/chat", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	if c.organizationID != "" {
		req.Header.Set("X-Organization-ID", c.organizationID)
	}
	if c.creds != nil {
		if err := c.creds.Apply(ctx, req); err != nil {
			return err
		}
	}

	// No client timeout: answers stream for as long as the model writes.
	resp, err := safehttp.Client(0).Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", c.host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &Error{Status: resp.StatusCode, Message: errorMessage(raw)}
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			return nil
		}
		var event struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			ID      string `json:"id"`
			Name    string `json:"name"`
			Args    string `json:"args"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		switch {
		case event.Type == "tool_call" && event.ID != "" && event.Name != "":
			onEvent(ChatEvent{ToolCall: &ToolCall{ID: event.ID, Name: event.Name, Args: event.Args}})
		case event.Content != "":
			onEvent(ChatEvent{Text: event.Content})
		}
	}
	return scanner.Err()
}
