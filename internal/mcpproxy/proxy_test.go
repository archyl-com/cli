package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type tokens struct{ fresh bool }

func (t *tokens) Apply(_ context.Context, r *http.Request) error {
	if t.fresh {
		r.Header.Set("Authorization", "Bearer new")
	} else {
		r.Header.Set("Authorization", "Bearer old")
	}
	return nil
}

func (t *tokens) Invalidate(context.Context) (bool, error) {
	t.fresh = true
	return true, nil
}

func TestProxy(
	t *testing.T,
) {
	var sessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sessions = append(sessions, r.Header.Get("Mcp-Session-Id"))
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		require.NoError(t, json.Unmarshal(body, &msg))
		switch msg["method"] {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s-1")
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":{"serverInfo":{"name":"archyl"}}}`, msg["id"])
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%v,\n", msg["id"])
			_, _ = fmt.Fprint(w, "data: \"result\":{\"tools\":[]}}\n\n")
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}
	}))
	defer server.Close()

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		``,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"x","method":"tools/call"}`,
	}, "\n")
	var out, log bytes.Buffer
	p := &Proxy{Endpoint: server.URL, Creds: &tokens{}, Log: &log}
	require.NoError(t, p.Serve(context.Background(), strings.NewReader(in), &out))

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3, "a notification gets no answer")
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":1,"result":{"serverInfo":{"name":"archyl"}}}`, lines[0], "a 401 is retried once with refreshed credentials")
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`, lines[1], "SSE data lines are joined into one message")
	assert.Contains(t, lines[2], `"id":"x"`)
	assert.Contains(t, lines[2], `"code":-32603`, "a failed call answers with a JSON-RPC error instead of hanging the client")
	assert.Equal(t, []string{"", "s-1", "s-1", "s-1"}, sessions, "the session ID is echoed after initialize")
	assert.Contains(t, log.String(), "boom")
}
