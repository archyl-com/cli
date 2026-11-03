package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

// fakeAPI answers the endpoints the server uses. A file violates the
// layering rule when its content contains "BAD" on line 2.
type fakeAPI struct {
	locates atomic.Int32
	checks  atomic.Int32
}

func (f *fakeAPI) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/p1/conformance/check", func(w http.ResponseWriter, r *http.Request) {
		f.checks.Add(1)
		var req api.ConformanceCheckRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		result := api.ConformanceCheckResult{Rules: []api.Rule{{ID: "r1", Name: "Layering"}}}
		for path, content := range req.FileContents {
			if strings.Contains(content, "BAD") {
				result.Violations = append(result.Violations, api.Violation{
					ID: "v1", RuleID: "r1", Severity: "high", Title: "Domain imports infra",
					Description: "The domain layer must not depend on infrastructure.",
					Suggestion:  "Depend on a port instead.", FilePath: path, LineStart: 2, LineEnd: 2,
				})
			}
		}
		// Another file's finding must not land on this one.
		result.Violations = append(result.Violations, api.Violation{RuleID: "r1", Severity: "low", Title: "elsewhere", FilePath: "other.go", LineStart: 1})
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /api/v1/projects/p1/harness/locate", func(w http.ResponseWriter, _ *http.Request) {
		f.locates.Add(1)
		_ = json.NewEncoder(w).Encode(api.LocateResult{
			Files: []api.FileLocation{{Path: "internal/billing/invoice.go", Elements: []api.LocatedElement{{ID: "comp1", Type: "component", Name: "Invoicing", Match: "path"}}}},
			Impact: &api.BlastRadius{Elements: []api.TouchedElement{{
				Element:    api.Element{ID: "comp1", Name: "Invoicing", Type: "component", Path: "Shop / API / Invoicing"},
				TeamOwners: []api.TeamOwner{{Slug: "billing"}},
				Owners:     []api.Owner{{Email: "ana@example.com"}},
				Dependents: []api.ImpactedElement{{Element: api.Element{Name: "Checkout"}}, {Element: api.Element{Name: "Reports"}}},
			}}},
		})
	})
	mux.HandleFunc("GET /api/v1/projects/p1/c4", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Model{Systems: []api.System{{ID: "sys1", Name: "Shop", Containers: []api.Container{{
			ID: "ctr1", Name: "API", Components: []api.Component{{ID: "comp1", Name: "Invoicing", Description: "Issues invoices."}},
		}}}}})
	})
	mux.HandleFunc("GET /api/v1/projects/p1/adrs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []api.ADR{
			{Number: 7, Title: "Hexagonal billing", Status: "accepted", Links: []api.ADRLink{{ElementID: "ctr1"}}},
			{Number: 9, Title: "Unrelated", Status: "accepted", Links: []api.ADRLink{{ElementID: "other"}}},
		}})
	})
	mux.HandleFunc("POST /api/v1/chat", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"content\":\"Because ADR-7 \"}\n\ndata: {\"content\":\"keeps billing hexagonal.\"}\n\ndata: [DONE]\n\n")
	})
	return mux
}

// client drives a server over pipes, the way an editor would.
type client struct {
	t        *testing.T
	in       *io.PipeWriter
	messages chan map[string]any
	done     chan error
	nextID   int
}

func startServer(t *testing.T, open Opener) *client {
	t.Helper()
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	server := NewServer(Options{Open: open, Version: "test", Debounce: 10 * time.Millisecond, TempDir: t.TempDir()})

	c := &client{t: t, in: clientOut, messages: make(chan map[string]any, 100), done: make(chan error, 1)}
	go func() {
		c.done <- server.Serve(context.Background(), serverIn, serverOut)
		_ = serverOut.Close()
	}()
	go func() {
		r := newReader(clientIn)
		for {
			body, err := r.read()
			if err != nil {
				close(c.messages)
				return
			}
			var msg map[string]any
			if json.Unmarshal(body, &msg) == nil {
				c.messages <- msg
			}
		}
	}()
	t.Cleanup(func() { _ = clientOut.Close() })
	return c
}

func (c *client) send(method string, params any, request bool) int {
	c.t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if request {
		c.nextID++
		msg["id"] = c.nextID
	}
	body, err := json.Marshal(msg)
	require.NoError(c.t, err)
	_, err = fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(body), body)
	require.NoError(c.t, err)
	return c.nextID
}

// await returns the first message matching, skipping others.
func (c *client) await(match func(map[string]any) bool) map[string]any {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-c.messages:
			require.True(c.t, ok, "the server closed the stream")
			if match(msg) {
				return msg
			}
		case <-timeout:
			c.t.Fatal("timed out waiting for a message")
		}
	}
}

func (c *client) call(method string, params any) map[string]any {
	c.t.Helper()
	id := c.send(method, params, true)
	return c.await(func(m map[string]any) bool { return m["id"] == float64(id) && m["method"] == nil })
}

func isMethod(method string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["method"] == method }
}

func remarshal(t *testing.T, in any, out any) {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, out))
}

func workspace(t *testing.T, apiURL string) (string, Opener) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "billing"), 0o755))
	backend := &APIBackend{Client: api.NewClient(apiURL, "", nil), ProjectID: "p1", Branch: "main", AppURL: "https://app.example.com"}
	return root, func(context.Context, string) (Backend, string, error) { return backend, root, nil }
}

func TestServerEndToEnd(
	t *testing.T,
) {
	fake := &fakeAPI{}
	apiServer := httptest.NewServer(fake.handler(t))
	defer apiServer.Close()
	root, open := workspace(t, apiServer.URL)
	file := filepath.Join(root, "internal", "billing", "invoice.go")
	uri := pathToURI(file)

	c := startServer(t, open)
	init := c.call("initialize", map[string]any{
		"rootUri":      pathToURI(root),
		"capabilities": map[string]any{"window": map[string]any{"showDocument": map[string]any{"support": true}}},
	})
	caps := init["result"].(map[string]any)["capabilities"].(map[string]any)
	assert.Equal(t, true, caps["hoverProvider"])
	assert.Equal(t, true, caps["codeActionProvider"])
	c.send("initialized", map[string]any{}, false)

	t.Run("a clean file has no diagnostics", func(t *testing.T) {
		c.send("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "go", "version": 1, "text": "package billing\n"}}, false)
		var p publishDiagnosticsParams
		remarshal(t, c.await(isMethod("textDocument/publishDiagnostics"))["params"], &p)
		assert.Equal(t, uri, p.URI)
		assert.Empty(t, p.Diagnostics)
	})

	var diag diagnostic
	t.Run("a save publishes the file's violations", func(t *testing.T) {
		// Two saves in a row run one check.
		before := fake.checks.Load()
		c.send("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}, "text": "package billing\nimport \"db\" // BAD\n"}, false)
		c.send("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}, "text": "package billing\nimport \"db\" // BAD\n"}, false)
		var p publishDiagnosticsParams
		remarshal(t, c.await(isMethod("textDocument/publishDiagnostics"))["params"], &p)
		require.Len(t, p.Diagnostics, 1, "the other file's finding is filtered out")
		diag = p.Diagnostics[0]
		assert.Equal(t, severityError, diag.Severity)
		assert.Equal(t, "archyl", diag.Source)
		assert.Equal(t, "Layering", diag.Code)
		assert.Equal(t, lspRange{Start: position{Line: 1}, End: position{Line: 1, Character: 18}}, diag.Range)
		assert.Contains(t, diag.Message, "Domain imports infra")
		assert.Contains(t, diag.Message, "→ Depend on a port instead.")
		assert.Equal(t, int32(1), fake.checks.Load()-before)
	})

	t.Run("hover shows the file's element", func(t *testing.T) {
		params := map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 0, "character": 3}}
		resp := c.call("textDocument/hover", params)
		var h hover
		remarshal(t, resp["result"], &h)
		assert.Equal(t, "markdown", h.Contents.Kind)
		for _, want := range []string{"**Invoicing** · component", "`Shop / API / Invoicing`", "Issues invoices.", "@billing, ana@example.com", "**Dependents:** 2", "ADR-7 Hexagonal billing (accepted)", "[Open in Archyl](https://app.example.com/project/p1?"} {
			assert.Contains(t, h.Contents.Value, want)
		}
		assert.NotContains(t, h.Contents.Value, "Unrelated")

		c.call("textDocument/hover", params)
		assert.Equal(t, int32(1), fake.locates.Load(), "the brief is cached per file")
	})

	t.Run("code actions ask why and open in Archyl", func(t *testing.T) {
		resp := c.call("textDocument/codeAction", map[string]any{
			"textDocument": map[string]any{"uri": uri},
			"range":        diag.Range,
			"context":      map[string]any{"diagnostics": []diagnostic{diag}},
		})
		var actions []codeAction
		remarshal(t, resp["result"], &actions)
		require.Len(t, actions, 2)
		assert.Equal(t, "Ask Archyl why: Layering", actions[0].Title)
		assert.Equal(t, CommandWhy, actions[0].Command.Command)
		assert.Equal(t, "Open in Archyl", actions[1].Title)
	})

	t.Run("open in Archyl shows the element's page", func(t *testing.T) {
		c.send("workspace/executeCommand", map[string]any{"command": CommandOpen, "arguments": []any{map[string]any{"uri": uri}}}, true)
		var p showDocumentParams
		remarshal(t, c.await(isMethod("window/showDocument"))["params"], &p)
		assert.True(t, p.External)
		assert.Contains(t, p.URI, "select=comp1")
	})

	t.Run("ask why opens the answer", func(t *testing.T) {
		c.send("workspace/executeCommand", map[string]any{"command": CommandWhy, "arguments": []any{
			map[string]any{"uri": uri, "line": 1, "ref": diag.Data},
		}}, true)
		var p showDocumentParams
		remarshal(t, c.await(isMethod("window/showDocument"))["params"], &p)
		assert.False(t, p.External)
		raw, err := os.ReadFile(uriToPath(p.URI))
		require.NoError(t, err)
		assert.Contains(t, string(raw), "Because ADR-7 keeps billing hexagonal.")
		assert.Contains(t, string(raw), `rule "Layering" flags `+"`internal/billing/invoice.go`"+` line 2`)
	})

	t.Run("unknown requests are refused, not fatal", func(t *testing.T) {
		resp := c.call("textDocument/definition", map[string]any{})
		assert.Equal(t, float64(codeMethodNotFound), resp["error"].(map[string]any)["code"])
	})

	t.Run("closing clears the diagnostics", func(t *testing.T) {
		c.send("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}}, false)
		var p publishDiagnosticsParams
		remarshal(t, c.await(isMethod("textDocument/publishDiagnostics"))["params"], &p)
		assert.Empty(t, p.Diagnostics)
	})

	resp := c.call("shutdown", nil)
	assert.Contains(t, resp, "result")
	c.send("exit", nil, false)
	select {
	case err := <-c.done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not exit")
	}
}

func TestServerOffline(
	t *testing.T,
) {
	apiServer := httptest.NewServer(http.NotFoundHandler())
	apiServer.Close() // nothing listens any more
	root, open := workspace(t, apiServer.URL)
	uri := pathToURI(filepath.Join(root, "internal", "billing", "invoice.go"))

	c := startServer(t, open)
	c.call("initialize", map[string]any{"rootUri": pathToURI(root)})
	c.send("initialized", map[string]any{}, false)

	for i := range 2 {
		c.send("textDocument/didSave", map[string]any{"textDocument": map[string]any{"uri": uri}, "text": "BAD\nBAD\n"}, false)
		msg := c.await(func(m map[string]any) bool {
			return m["method"] == "textDocument/publishDiagnostics" || m["method"] == "window/logMessage"
		})
		if i == 0 {
			require.Equal(t, "window/logMessage", msg["method"], "the first failure is logged once")
			assert.Equal(t, float64(messageWarning), msg["params"].(map[string]any)["type"])
			msg = c.await(isMethod("textDocument/publishDiagnostics"))
		}
		require.Equal(t, "textDocument/publishDiagnostics", msg["method"], "later failures are not logged again")
		var p publishDiagnosticsParams
		remarshal(t, msg["params"], &p)
		assert.Empty(t, p.Diagnostics)
	}

	resp := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{}})
	assert.Nil(t, resp["result"])
	assert.NotContains(t, resp, "error")
}

func TestServerWithoutWorkspace(
	t *testing.T,
) {
	c := startServer(t, func(context.Context, string) (Backend, string, error) {
		return nil, "", fmt.Errorf("not logged in")
	})
	c.call("initialize", map[string]any{"rootUri": "file:///nowhere"})
	c.send("initialized", map[string]any{}, false)
	log := c.await(isMethod("window/logMessage"))
	assert.Contains(t, log["params"].(map[string]any)["message"], "not logged in")

	resp := c.call("textDocument/hover", map[string]any{"textDocument": map[string]any{"uri": "file:///nowhere/a.go"}, "position": map[string]any{}})
	assert.Nil(t, resp["result"])
	resp = c.call("textDocument/codeAction", map[string]any{"textDocument": map[string]any{"uri": "file:///nowhere/a.go"}})
	assert.Empty(t, resp["result"])
}

func TestRequestBeforeInitialize(
	t *testing.T,
) {
	c := startServer(t, nil)
	resp := c.call("textDocument/hover", map[string]any{})
	assert.Equal(t, float64(codeNotInitialized), resp["error"].(map[string]any)["code"])
}
