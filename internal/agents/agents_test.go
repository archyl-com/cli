package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc
}

func TestConfigure(
	t *testing.T,
) {
	const url = "https://api.archyl.com/mcp?profile=coding"

	testCases := []struct {
		name     string
		agent    string
		existing string
		file     func(Env) string
		check    func(t *testing.T, path string)
	}{
		{
			name:  "claude code writes the project .mcp.json",
			agent: "claude-code",
			file:  func(e Env) string { return filepath.Join(e.Root, ".mcp.json") },
			check: func(t *testing.T, path string) {
				server := readFile(t, path)["mcpServers"].(map[string]any)["archyl"].(map[string]any)
				assert.Equal(t, map[string]any{"type": "http", "url": url}, server)
			},
		},
		{
			name:     "cursor keeps the servers already there",
			agent:    "cursor",
			existing: `{"mcpServers":{"github":{"url":"https://example.com"}}}`,
			file:     func(e Env) string { return filepath.Join(e.Root, ".cursor", "mcp.json") },
			check: func(t *testing.T, path string) {
				servers := readFile(t, path)["mcpServers"].(map[string]any)
				assert.Contains(t, servers, "github")
				assert.Equal(t, map[string]any{"url": url}, servers["archyl"])
			},
		},
		{
			name:  "vs code uses the servers key",
			agent: "vscode",
			file:  func(e Env) string { return filepath.Join(e.Root, ".vscode", "mcp.json") },
			check: func(t *testing.T, path string) {
				server := readFile(t, path)["servers"].(map[string]any)["archyl"]
				assert.Equal(t, map[string]any{"type": "http", "url": url}, server)
			},
		},
		{
			name:     "codex appends a TOML section",
			agent:    "codex",
			existing: "model = \"o4\"",
			file:     func(e Env) string { return filepath.Join(e.Home, ".codex", "config.toml") },
			check: func(t *testing.T, path string) {
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "model = \"o4\"\n\n[mcp_servers.archyl]\nurl = \""+url+"\"\n", string(raw))
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			env := Env{Root: t.TempDir(), Home: t.TempDir()}
			path := testCase.file(env)
			if testCase.existing != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, []byte(testCase.existing), 0o644))
			}

			change, err := Configure(env, testCase.agent, url)
			require.NoError(t, err)
			assert.Equal(t, path, change.Path)
			testCase.check(t, path)

			again, err := Configure(env, testCase.agent, url)
			require.NoError(t, err)
			assert.Equal(t, "unchanged", again.Action, "setup must be idempotent")
		})
	}
}

func TestConfigureRefusesInvalidJSON(
	t *testing.T,
) {
	env := Env{Root: t.TempDir()}
	path := filepath.Join(env.Root, ".mcp.json")
	require.NoError(t, os.WriteFile(path, []byte("{oops"), 0o644))

	_, err := Configure(env, "claude-code", "u")
	assert.Error(t, err)
	raw, _ := os.ReadFile(path)
	assert.Equal(t, "{oops", string(raw), "a file we cannot parse is left untouched")
}

func TestInstallGuard(
	t *testing.T,
) {
	env := Env{Root: t.TempDir()}
	path := filepath.Join(env.Root, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"lint"}]}]}}`), 0o644))

	change, err := InstallGuard(env)
	require.NoError(t, err)
	assert.Equal(t, "updated", change.Action)

	doc := readFile(t, path)
	assert.Equal(t, "opus", doc["model"])
	pre := doc["hooks"].(map[string]any)["PreToolUse"].([]any)
	require.Len(t, pre, 2)
	guardHook := pre[1].(map[string]any)
	assert.Equal(t, "Write|Edit|MultiEdit", guardHook["matcher"])
	assert.Equal(t, GuardCommand, guardHook["hooks"].([]any)[0].(map[string]any)["command"])

	again, err := InstallGuard(env)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", again.Action)
}

func TestInstallInstructions(
	t *testing.T,
) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	change, err := InstallInstructions(path)
	require.NoError(t, err)
	assert.Equal(t, "created", change.Action)

	require.NoError(t, os.WriteFile(path, []byte("# Project\n\nOwn notes.\n"), 0o644))
	_, err = InstallInstructions(path)
	require.NoError(t, err)
	again, err := InstallInstructions(path)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", again.Action)

	raw, _ := os.ReadFile(path)
	assert.True(t, strings.HasPrefix(string(raw), "# Project\n\nOwn notes.\n"), "the user's content stays first and intact")
	assert.Equal(t, 1, strings.Count(string(raw), blockStart), "the block is never duplicated")

	stale := strings.Replace(string(raw), "archyl brief", "archyl old-brief", 1)
	require.NoError(t, os.WriteFile(path, []byte(stale+"\nAfter.\n"), 0o644))
	refreshed, err := InstallInstructions(path)
	require.NoError(t, err)
	assert.Equal(t, "updated", refreshed.Action)
	raw, _ = os.ReadFile(path)
	assert.NotContains(t, string(raw), "old-brief")
	assert.Contains(t, string(raw), "After.", "text after the block is kept")
}

func TestInstallStatusline(
	t *testing.T,
) {
	env := Env{Root: t.TempDir()}
	change, err := InstallStatusline(env)
	require.NoError(t, err)
	assert.Equal(t, "created", change.Action)
	doc := readFile(t, filepath.Join(env.Root, ".claude", "settings.json"))
	assert.Equal(t, StatuslineCommand, doc["statusLine"].(map[string]any)["command"])

	require.NoError(t, os.WriteFile(filepath.Join(env.Root, ".claude", "settings.json"), []byte(`{"statusLine":{"type":"command","command":"my-line"}}`), 0o644))
	kept, err := InstallStatusline(env)
	require.NoError(t, err)
	assert.Contains(t, kept.Action, "kept", "someone else's status line is not replaced")
}

func TestConfigureCLITransport(
	t *testing.T,
) {
	env := Env{Root: t.TempDir(), Home: t.TempDir()}
	_, err := ConfigureWith(env, "claude-code", "ignored", TransportCLI)
	require.NoError(t, err)
	server := readFile(t, filepath.Join(env.Root, ".mcp.json"))["mcpServers"].(map[string]any)["archyl"]
	assert.Equal(t, map[string]any{"type": "stdio", "command": "archyl", "args": []any{"mcp"}}, server)

	again, err := ConfigureWith(env, "claude-code", "ignored", TransportCLI)
	require.NoError(t, err)
	assert.Equal(t, "unchanged", again.Action)

	_, err = ConfigureWith(env, "codex", "ignored", TransportCLI)
	require.NoError(t, err)
	raw, _ := os.ReadFile(filepath.Join(env.Home, ".codex", "config.toml"))
	assert.Equal(t, "[mcp_servers.archyl]\ncommand = \"archyl\"\nargs = [\"mcp\"]\n", string(raw))
}
