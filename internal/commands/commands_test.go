package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

func TestResolveFormat(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		format string
		json   bool
		env    ci.Env
		want   string
	}{
		{name: "--json wins", format: "sarif", json: true, want: "json"},
		{name: "explicit format", format: "sarif", env: ci.Env{Provider: ci.GitHub}, want: "sarif"},
		{name: "auto on github", format: "auto", env: ci.Env{Provider: ci.GitHub}, want: "github"},
		{name: "auto on gitlab", format: "auto", env: ci.Env{Provider: ci.GitLab}, want: "gitlab"},
		{name: "auto elsewhere", format: "auto", env: ci.Env{Provider: ci.Bitbucket}, want: "pretty"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, resolveFormat(testCase.format, testCase.json, testCase.env))
		})
	}
}

// TestHooks installs, refuses to clobber, and uninstalls the pre-push hook.
func TestHooks(
	t *testing.T,
) {
	dir := t.TempDir()
	out, err := exec.Command("git", "init", "-q", dir).CombinedOutput()
	require.NoError(t, err, string(out))
	t.Chdir(dir)

	run := func(args ...string) error {
		app := &App{Printer: &ui.Printer{Out: os.Stdout, Err: os.Stderr}}
		root := NewRoot(app)
		root.SetArgs(args)
		return root.Execute()
	}
	hook := filepath.Join(dir, ".git", "hooks", "pre-push")

	require.NoError(t, run("hooks", "install", "--fail-on", "high"))
	raw, err := os.ReadFile(hook)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "archyl check --fail-on high --fail-open")
	if runtime.GOOS != "windows" { // Windows has no executable bit; Git for Windows runs hooks anyway
		info, _ := os.Stat(hook)
		assert.NotZero(t, info.Mode()&0o100, "the hook must be executable")
	}

	require.NoError(t, run("hooks", "install"), "reinstalling our own hook is allowed")

	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nmake lint\n"), 0o755))
	assert.Error(t, run("hooks", "install"), "a hook written by someone else is kept")
	assert.Error(t, run("hooks", "uninstall"))

	require.NoError(t, run("hooks", "install", "--force"))
	require.NoError(t, run("hooks", "uninstall"))
	_, err = os.Stat(hook)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestReviewMarkdown(
	t *testing.T,
) {
	r := &reviewReport{
		Check: &checkRun{
			Base:    "origin/main",
			Changes: []repo.Change{{Path: "a.go"}, {Path: "b.go"}},
			Result: &api.ConformanceCheckResult{
				Violations: []api.Violation{{RuleID: "r1", Severity: "critical", Title: "Domain | imports infra", FilePath: "a.go", LineStart: 3}},
				Rules:      []api.Rule{{ID: "r1", Name: "Layering"}},
			},
		},
		Impact: &api.LocateResult{Impact: &api.BlastRadius{
			Elements: []api.TouchedElement{{Element: api.Element{Name: "Payments", Type: "component"},
				Dependents: []api.ImpactedElement{{Element: api.Element{Name: "Checkout"}}}}},
			Notify: api.NotifyList{Teams: []api.TeamOwner{{Slug: "payments"}}},
		}},
		Ready: false,
	}
	md := reviewMarkdown(r, "high", "https://ci/run/1")
	assert.Contains(t, md, "blocked ❌")
	assert.Contains(t, md, "| 🔴 critical | Layering | `a.go:3` | Domain \\| imports infra |", "pipes in findings do not break the table")
	assert.Contains(t, md, "**Payments** (component) — used by Checkout")
	assert.Contains(t, md, "`@payments`")
	assert.Contains(t, md, "[run](https://ci/run/1)")
}

func TestReviewMarkdownTableCells(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		violation api.Violation
		rule      string
		want      string
	}{
		{
			name:      "a multi-line finding stays on its row",
			violation: api.Violation{RuleID: "r1", Severity: "high", Title: "Domain imports infra:\n\n  database/sql\tleaks", FilePath: "a.go"},
			rule:      "Layering",
			want:      "| 🟠 high | Layering | `a.go` | Domain imports infra: database/sql leaks |\n",
		},
		{
			name:      "pipes and newlines in the rule name and the path",
			violation: api.Violation{RuleID: "r1", Severity: "low", Title: "x", FilePath: "odd|dir/a.go", LineStart: 2},
			rule:      "Layer\n| boundaries",
			want:      "| ⚪ low | Layer \\| boundaries | `odd\\|dir/a.go:2` | x |\n",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := &reviewReport{Ready: true, Check: &checkRun{Result: &api.ConformanceCheckResult{
				Violations: []api.Violation{testCase.violation},
				Rules:      []api.Rule{{ID: "r1", Name: testCase.rule}},
			}}}
			md := reviewMarkdown(r, "critical", "")
			assert.Contains(t, md, testCase.want)
		})
	}
}
