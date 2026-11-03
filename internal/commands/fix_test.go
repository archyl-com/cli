package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// fixAPI fakes the endpoints fix uses. A file violates the layering rule
// for as long as it contains "BAD".
type fixAPI struct {
	sessions atomic.Int32
	finished atomic.Int32
}

func (f *fixAPI) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/p1/conformance/check", func(w http.ResponseWriter, r *http.Request) {
		var req api.ConformanceCheckRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		result := api.ConformanceCheckResult{Rules: []api.Rule{{ID: "r1", Name: "Layering", Description: "Domain code stays free of infrastructure."}}}
		for path, content := range req.FileContents {
			if strings.Contains(content, "BAD") {
				result.Violations = append(result.Violations, api.Violation{
					RuleID: "r1", Severity: "high", Title: "Domain imports infra", FilePath: path, LineStart: 2,
					Description: "domain/order.go imports the database driver.", Suggestion: "Depend on a repository port.",
				})
			}
		}
		result.Summary.Total = len(result.Violations)
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /api/v1/projects/p1/harness/locate", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.LocateResult{
			Files: []api.FileLocation{{Path: "domain/order.go", Elements: []api.LocatedElement{{ID: "comp1", Type: "component", Name: "Orders"}}}},
		})
	})
	mux.HandleFunc("GET /api/v1/projects/p1/c4", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.Model{Systems: []api.System{{ID: "sys1", Name: "Shop", Containers: []api.Container{{
			ID: "ctr1", Name: "API", Components: []api.Component{{ID: "comp1", Name: "Orders"}},
		}}}}})
	})
	mux.HandleFunc("GET /api/v1/projects/p1/adrs", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []api.ADR{
			{Number: 4, Title: "Hexagonal core", Status: "accepted", Decision: "The domain depends on ports only.", Links: []api.ADRLink{{ElementID: "ctr1"}}},
		}})
	})
	mux.HandleFunc("POST /api/v1/projects/p1/harness/sessions", func(w http.ResponseWriter, _ *http.Request) {
		f.sessions.Add(1)
		_ = json.NewEncoder(w).Encode(api.StartSessionResult{Session: &api.Session{ID: "sess-1", Gate: "clear"}, Markdown: "# Briefing\n"})
	})
	mux.HandleFunc("POST /api/v1/harness/sessions/sess-1/finish", func(w http.ResponseWriter, _ *http.Request) {
		f.finished.Add(1)
		_ = json.NewEncoder(w).Encode(api.FinishSessionResult{})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})
	return mux
}

// fixRepo is a repository whose working tree changes domain/order.go.
func fixRepo(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "domain"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "domain", "order.go"), []byte("package domain\n"), 0o644))
	git("add", ".")
	git("commit", "-q", "-m", "base")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "domain", "order.go"), []byte(content), 0o644))
	return dir
}

// fakeClaude installs a `claude` on PATH that records its arguments and the
// task file, and removes the violation unless told not to.
func fakeClaude(t *testing.T, fixes bool) string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "agent")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n---\\n' \"$a\"; done > " + record + ".args\n" +
		"cp \"$ARCHYL_FIX_TASK_FILE\" " + record + ".task\n" +
		"echo \"$ARCHYL_SESSION_ID\" > " + record + ".session\n"
	if fixes {
		script += "printf 'package domain\\n\\nimport \"orders/port\"\\n' > domain/order.go\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func TestFix(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake agent is a shell script")
	}
	const violating = "package domain\nimport \"database/sql\" // BAD\n"
	testCases := []struct {
		name       string
		content    string
		opts       fixOptions
		agentFixes bool
		wantExit   int
		wantRun    bool
		check      func(t *testing.T, out string, result fixResult, record string)
	}{
		{
			name: "the agent fixes the violation", content: violating, agentFixes: true, wantRun: true,
			check: func(t *testing.T, _ string, result fixResult, record string) {
				assert.Len(t, result.Selected, 1)
				assert.Empty(t, result.Remaining)
				assert.Equal(t, 1, result.Fixed)
				assert.Equal(t, "claude", result.Agent)

				args, err := os.ReadFile(record + ".args")
				require.NoError(t, err)
				assert.Contains(t, string(args), "--append-system-prompt", "Claude Code gets the session briefing")
				assert.Contains(t, string(args), "(also in $ARCHYL_FIX_TASK_FILE)", "and a pointer to the task file as its prompt")
				assert.NotContains(t, string(args), "[HIGH] Domain imports infra", "never the whole task on the command line")
				task, err := os.ReadFile(record + ".task")
				require.NoError(t, err)
				for _, want := range []string{
					"- **Rule:** Layering — Domain code stays free of infrastructure.",
					"- **Location:** `domain/order.go:2`",
					"- **Suggestion:** Depend on a repository port.",
					"### Orders (component)",
					"ADR-4 Hexagonal core (accepted): The domain depends on ports only.",
					"Do not commit",
				} {
					assert.Contains(t, string(task), want)
				}
				session, _ := os.ReadFile(record + ".session")
				assert.Equal(t, "sess-1\n", string(session))
			},
		},
		{
			name: "violations left fail the command", content: violating, wantRun: true, wantExit: 1,
			check: func(t *testing.T, _ string, result fixResult, _ string) {
				assert.Len(t, result.Remaining, 1)
				assert.Zero(t, result.Fixed)
			},
		},
		{
			name: "--print writes the task without running an agent", content: violating, opts: fixOptions{PrintOnly: true},
			check: func(t *testing.T, _ string, result fixResult, record string) {
				assert.Contains(t, result.Prompt, "Fix the architecture violation below")
				_, err := os.Stat(record + ".args")
				assert.ErrorIs(t, err, os.ErrNotExist)
			},
		},
		{
			name: "nothing to fix", content: "package domain\n",
			check: func(t *testing.T, _ string, result fixResult, _ string) {
				assert.Empty(t, result.Selected)
			},
		},
		{
			name: "--severity filters out lower findings", content: violating, opts: fixOptions{MinSeverity: 4},
			check: func(t *testing.T, _ string, result fixResult, _ string) {
				assert.Empty(t, result.Selected)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fake := &fixAPI{}
			server := httptest.NewServer(fake.handler(t))
			defer server.Close()
			t.Setenv("ARCHYL_HOST", server.URL)
			t.Setenv("ARCHYL_API_KEY", "test-key")
			t.Chdir(fixRepo(t, testCase.content))
			record := fakeClaude(t, testCase.agentFixes)

			var out bytes.Buffer
			app := &App{Printer: &ui.Printer{Out: &out, Err: &bytes.Buffer{}, JSON: true}, project: "p1", configDir: t.TempDir()}
			opts := testCase.opts
			opts.Base = "HEAD"
			if opts.MinSeverity == 0 {
				opts.MinSeverity = 1
			}
			err := runFix(t.Context(), app, opts)

			var exit *ExitError
			if testCase.wantExit != 0 {
				require.True(t, errors.As(err, &exit), "want an exit error, got %v", err)
				assert.Equal(t, testCase.wantExit, exit.Code)
			} else {
				require.NoError(t, err)
			}
			var result fixResult
			require.NoError(t, json.Unmarshal(out.Bytes(), &result), out.String())
			if testCase.wantRun {
				assert.Equal(t, int32(1), fake.sessions.Load())
				assert.Equal(t, int32(1), fake.finished.Load())
			} else {
				assert.Zero(t, fake.sessions.Load())
			}
			testCase.check(t, out.String(), result, record)
		})
	}
}

func TestFixAgentArgs(
	t *testing.T,
) {
	installed := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, n := range names {
				if n == name {
					return "/bin/" + name, nil
				}
			}
			return "", exec.ErrNotFound
		}
	}
	const taskFile = "/tmp/archyl-fix-1.md"
	task := fixTaskPointer(taskFile)
	assert.Contains(t, task, taskFile)
	testCases := []struct {
		name      string
		agent     string
		custom    []string
		lookPath  func(string) (string, error)
		wantArgs  []string
		wantAgent string
		wantErr   bool
	}{
		{name: "claude first", lookPath: installed("codex", "claude"), wantArgs: []string{"claude", task}, wantAgent: "claude"},
		{name: "codex when claude is missing", lookPath: installed("codex"), wantArgs: []string{"codex", task}, wantAgent: "codex"},
		{name: "no agent installed", lookPath: installed(), wantErr: true},
		{name: "explicit agent", agent: "gemini", lookPath: installed("gemini", "claude"), wantArgs: []string{"gemini", "--prompt-interactive", task}, wantAgent: "gemini"},
		{name: "explicit agent missing", agent: "codex", lookPath: installed("claude"), wantErr: true},
		{name: "custom claude gets the task", custom: []string{"claude", "--model", "opus"}, lookPath: installed(), wantArgs: []string{"claude", "--model", "opus", task}, wantAgent: "claude"},
		{name: "custom command reads the task file", custom: []string{"aider", "--yes"}, lookPath: installed(), wantArgs: []string{"aider", "--yes"}, wantAgent: "aider"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			args, agent, err := fixAgentArgs(testCase.agent, testCase.custom, taskFile, testCase.lookPath)
			if testCase.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantArgs, args)
			assert.Equal(t, testCase.wantAgent, agent)
		})
	}
}

func TestSplitRemaining(
	t *testing.T,
) {
	a := api.Violation{RuleID: "r1", FilePath: "a.go", Title: "x", ID: "1", LineStart: 3}
	b := api.Violation{RuleID: "r2", FilePath: "b.go", Title: "y", ID: "2"}
	aMoved := a
	aMoved.ID, aMoved.LineStart = "other check", 9
	testCases := []struct {
		name          string
		selected      []api.Violation
		after         []api.Violation
		wantRemaining []api.Violation
		wantOthers    []api.Violation
	}{
		{name: "all fixed", selected: []api.Violation{a, b}},
		{name: "matched across checks despite new IDs and lines", selected: []api.Violation{a, b}, after: []api.Violation{aMoved}, wantRemaining: []api.Violation{aMoved}},
		{name: "a violation not handed over is not remaining", selected: []api.Violation{a}, after: []api.Violation{b}, wantOthers: []api.Violation{b}},
		{name: "as many times as selected", selected: []api.Violation{a}, after: []api.Violation{a, aMoved}, wantRemaining: []api.Violation{a}, wantOthers: []api.Violation{aMoved}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			remaining, others := splitRemaining(testCase.selected, testCase.after)
			assert.Equal(t, testCase.wantRemaining, remaining)
			assert.Equal(t, testCase.wantOthers, others)
		})
	}
}
