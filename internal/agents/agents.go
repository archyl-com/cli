// Package agents connects coding agents to Archyl: it writes their MCP server
// configuration and installs the Guard hook. Configuration carries only the
// server URL — agents sign in through Archyl's OAuth flow themselves, so no
// secret ends up in a file that may be committed.
package agents

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/archyl-com/cli/internal/repo"
)

// Agent is a coding agent the CLI knows how to configure.
type Agent struct {
	ID   string
	Name string
	// Detected is true when the agent seems installed on this machine or
	// already configured in this repository.
	Detected bool
	// AfterSetup is what the user still has to do, if anything.
	AfterSetup string
}

// Change is a file setup touched.
type Change struct {
	Path   string `json:"path"`
	Action string `json:"action"` // created, updated, unchanged
}

// Env locates the files agents read.
type Env struct {
	Root string // repository root
	Home string // user home
	// LookPath finds binaries; exec.LookPath by default.
	LookPath func(string) (string, error)
}

func (e Env) has(binary string) bool {
	lookPath := e.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	_, err := lookPath(binary)
	return err == nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Detect lists the supported agents, marking the ones present.
func Detect(env Env) []Agent {
	return []Agent{
		{
			ID: "claude-code", Name: "Claude Code",
			Detected:   env.has("claude") || exists(filepath.Join(env.Home, ".claude")),
			AfterSetup: "Approve the archyl server and sign in when Claude Code asks (/mcp).",
		},
		{
			ID: "cursor", Name: "Cursor",
			Detected:   env.has("cursor") || exists(filepath.Join(env.Home, ".cursor")) || exists(filepath.Join(env.Root, ".cursor")),
			AfterSetup: "Enable archyl in Cursor Settings › MCP and sign in.",
		},
		{
			ID: "vscode", Name: "VS Code",
			Detected:   env.has("code") || exists(filepath.Join(env.Root, ".vscode")),
			AfterSetup: "Start the archyl server from .vscode/mcp.json and sign in.",
		},
		{
			ID: "codex", Name: "OpenAI Codex",
			Detected:   env.has("codex") || exists(filepath.Join(env.Home, ".codex")),
			AfterSetup: "Run `codex mcp login archyl` to sign in.",
		},
	}
}

// MCPURL is the server URL agents connect to, with the coding tool profile:
// a dozen task-scoped tools instead of the full catalog.
func MCPURL(host string) string {
	return strings.TrimSuffix(host, "/") + "/mcp?profile=coding"
}

// Transport is how an agent reaches Archyl's MCP server.
type Transport string

const (
	// TransportHTTP connects the agent to the server URL; it signs in itself.
	TransportHTTP Transport = "http"
	// TransportCLI runs `archyl mcp`, which uses the CLI's session.
	TransportCLI Transport = "cli"
)

// Configure writes the MCP configuration for one agent.
func Configure(env Env, agentID, url string) (Change, error) {
	return ConfigureWith(env, agentID, url, TransportHTTP)
}

// ConfigureWith writes the MCP configuration for one agent and transport.
func ConfigureWith(env Env, agentID, url string, transport Transport) (Change, error) {
	if transport == TransportCLI {
		return configureCLI(env, agentID)
	}
	switch agentID {
	case "claude-code":
		return mergeJSON(filepath.Join(env.Root, ".mcp.json"),
			[]string{"mcpServers", "archyl"}, map[string]any{"type": "http", "url": url})
	case "cursor":
		return mergeJSON(filepath.Join(env.Root, ".cursor", "mcp.json"),
			[]string{"mcpServers", "archyl"}, map[string]any{"url": url})
	case "vscode":
		return mergeJSON(filepath.Join(env.Root, ".vscode", "mcp.json"),
			[]string{"servers", "archyl"}, map[string]any{"type": "http", "url": url})
	case "codex":
		return appendTOMLSection(filepath.Join(env.Home, ".codex", "config.toml"),
			"mcp_servers.archyl", fmt.Sprintf("url = %q\n", url))
	}
	return Change{}, fmt.Errorf("unknown agent %q", agentID)
}

// GuardCommand is the hook command the Guard runs.
const GuardCommand = "archyl guard"

// InstallGuard adds the Guard as a PreToolUse hook in the repository's Claude
// Code settings, leaving every other hook in place.
func InstallGuard(env Env) (Change, error) {
	path := filepath.Join(env.Root, ".claude", "settings.json")
	doc, err := readJSON(path)
	if err != nil {
		return Change{}, err
	}
	created := doc == nil
	if doc == nil {
		doc = map[string]any{}
	}

	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	pre, _ := hooks["PreToolUse"].([]any)
	for _, entry := range pre {
		group, _ := entry.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, h := range inner {
			if hook, _ := h.(map[string]any); hook != nil && hook["command"] == GuardCommand {
				return Change{Path: path, Action: "unchanged"}, nil
			}
		}
	}
	pre = append(pre, map[string]any{
		"matcher": "Write|Edit|MultiEdit",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": GuardCommand,
			"timeout": 20,
		}},
	})
	hooks["PreToolUse"] = pre
	doc["hooks"] = hooks

	if err := writeJSON(path, doc); err != nil {
		return Change{}, err
	}
	if created {
		return Change{Path: path, Action: "created"}, nil
	}
	return Change{Path: path, Action: "updated"}, nil
}

func readJSON(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON, leaving it alone: %w", path, err)
	}
	return doc, nil
}

func writeJSON(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := repo.RefuseLink(path); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// mergeJSON sets doc[keys...] = value, creating the file and intermediate
// objects as needed.
func mergeJSON(path string, keys []string, value map[string]any) (Change, error) {
	doc, err := readJSON(path)
	if err != nil {
		return Change{}, err
	}
	action := "updated"
	if doc == nil {
		doc = map[string]any{}
		action = "created"
	}

	parent := doc
	for _, key := range keys[:len(keys)-1] {
		next, _ := parent[key].(map[string]any)
		if next == nil {
			next = map[string]any{}
			parent[key] = next
		}
		parent = next
	}
	last := keys[len(keys)-1]
	if current, ok := parent[last].(map[string]any); ok && reflect.DeepEqual(current, value) {
		return Change{Path: path, Action: "unchanged"}, nil
	}
	parent[last] = value

	if err := writeJSON(path, doc); err != nil {
		return Change{}, err
	}
	return Change{Path: path, Action: action}, nil
}

// appendTOMLSection appends [section] with body unless the section exists.
// Editing TOML in place is out of scope; an existing section is left as is.
func appendTOMLSection(path, section, body string) (Change, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Change{}, err
	}
	if strings.Contains(string(raw), "["+section+"]") {
		return Change{Path: path, Action: "unchanged"}, nil
	}
	action := "updated"
	if raw == nil {
		action = "created"
	}

	var buf bytes.Buffer
	buf.Write(raw)
	if len(raw) > 0 && !bytes.HasSuffix(raw, []byte("\n")) {
		buf.WriteByte('\n')
	}
	if len(raw) > 0 {
		buf.WriteByte('\n')
	}
	fmt.Fprintf(&buf, "[%s]\n%s", section, body)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Change{}, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return Change{}, err
	}
	return Change{Path: path, Action: action}, nil
}

func configureCLI(env Env, agentID string) (Change, error) {
	args := []any{"mcp"}
	switch agentID {
	case "claude-code":
		return mergeJSON(filepath.Join(env.Root, ".mcp.json"),
			[]string{"mcpServers", "archyl"}, map[string]any{"type": "stdio", "command": "archyl", "args": args})
	case "cursor":
		return mergeJSON(filepath.Join(env.Root, ".cursor", "mcp.json"),
			[]string{"mcpServers", "archyl"}, map[string]any{"command": "archyl", "args": args})
	case "vscode":
		return mergeJSON(filepath.Join(env.Root, ".vscode", "mcp.json"),
			[]string{"servers", "archyl"}, map[string]any{"type": "stdio", "command": "archyl", "args": args})
	case "codex":
		return appendTOMLSection(filepath.Join(env.Home, ".codex", "config.toml"),
			"mcp_servers.archyl", "command = \"archyl\"\nargs = [\"mcp\"]\n")
	}
	return Change{}, fmt.Errorf("unknown agent %q", agentID)
}
