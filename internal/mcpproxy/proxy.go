// Package mcpproxy bridges an MCP client speaking stdio to Archyl's remote MCP
// server, authenticating with the CLI's own session: sign in once with
// `archyl login` and every agent configured with `archyl mcp` is connected,
// with no separate OAuth flow and no API key in a config file.
package mcpproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/safehttp"
)

// Proxy forwards newline-delimited JSON-RPC messages to an HTTP MCP endpoint.
type Proxy struct {
	Endpoint string
	Creds    api.Credentials
	HTTP     *http.Client
	// Log receives diagnostics; stdout belongs to the protocol.
	Log io.Writer

	mu        sync.Mutex
	sessionID string
	out       *bufio.Writer
}

// Serve reads messages from in until EOF and writes every answer to out.
func (p *Proxy) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	p.out = bufio.NewWriter(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := append([]byte(nil), line...)
		if err := p.forward(ctx, msg); err != nil {
			p.logf("archyl mcp: %v", err)
			p.replyError(msg, err)
		}
	}
	return scanner.Err()
}

func (p *Proxy) logf(format string, args ...any) {
	if p.Log != nil {
		_, _ = fmt.Fprintf(p.Log, format+"\n", args...)
	}
}

func (p *Proxy) forward(ctx context.Context, msg []byte) error {
	for attempt := 0; ; attempt++ {
		status, err := p.post(ctx, msg)
		if status != http.StatusUnauthorized || attempt > 0 || p.Creds == nil {
			return err
		}
		retry, refreshErr := p.Creds.Invalidate(ctx)
		if refreshErr != nil {
			return refreshErr
		}
		if !retry {
			return err
		}
	}
}

func (p *Proxy) post(ctx context.Context, msg []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(msg))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", buildinfo.UserAgent()+" (mcp proxy)")
	p.mu.Lock()
	if p.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", p.sessionID)
	}
	p.mu.Unlock()
	if p.Creds != nil {
		if err := p.Creds.Apply(ctx, req); err != nil {
			return 0, err
		}
	}

	client := p.HTTP
	if client == nil {
		client = safehttp.Client(0)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		p.mu.Lock()
		p.sessionID = id
		p.mu.Unlock()
	}
	switch {
	case resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent:
		return resp.StatusCode, nil
	case resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return resp.StatusCode, p.relaySSE(resp.Body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, p.relayJSON(body)
}

// relayJSON writes a response, or each response of a batch, on its own line.
func (p *Proxy) relayJSON(body []byte) error {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil
	}
	if body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			return err
		}
		for _, m := range batch {
			if err := p.write(m); err != nil {
				return err
			}
		}
		return nil
	}
	return p.write(body)
}

func (p *Proxy) relaySSE(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	var data bytes.Buffer
	flush := func() error {
		if data.Len() == 0 {
			return nil
		}
		defer data.Reset()
		return p.relayJSON(data.Bytes())
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return scanner.Err()
}

func (p *Proxy) write(msg []byte) error {
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, msg); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	compact.WriteByte('\n')
	if _, err := p.out.Write(compact.Bytes()); err != nil {
		return err
	}
	return p.out.Flush()
}

// replyError answers a failed request so the client does not wait forever.
// Notifications (no id) get no answer, as the protocol requires.
func (p *Proxy) replyError(msg []byte, cause error) {
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(msg, &req) != nil || len(req.ID) == 0 || string(req.ID) == "null" {
		return
	}
	reply, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"error":   map[string]any{"code": -32603, "message": "archyl: " + cause.Error()},
	})
	_ = p.write(reply)
}
