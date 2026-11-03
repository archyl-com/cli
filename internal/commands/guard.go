package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/guard"
	"github.com/archyl-com/cli/internal/radar"
	"github.com/archyl-com/cli/internal/repo"
)

func newGuardCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Agent hook: check an edit against the architecture rules",
		Long: `Read a Claude Code PreToolUse payload on stdin, run the project's conformance
rules on the file as it would be after the edit, and answer with a hook
decision: critical violations block the edit, high ones warn.

Install it with ` + "`archyl setup`" + `. It always fails open — no credential, no
network or an unknown tool lets the edit through.

  ARCHYL_GUARD_BLOCK   critical (default) | high | off
  ARCHYL_SESSION_ID    also attribute the file to this harness session
                       (set by ` + "`archyl wrap`" + `)`,
		Args:   cobra.NoArgs,
		Hidden: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out := runGuard(cmd.Context(), app, os.Stdin); out != nil {
				_, _ = os.Stdout.Write(out)
			}
			return nil
		},
	}
}

func runGuard(ctx context.Context, app *App, stdin io.Reader) []byte {
	level := guard.ParseLevel(os.Getenv("ARCHYL_GUARD_BLOCK"))
	if level == guard.BlockOff {
		return nil
	}

	var in guard.HookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil || in.ToolInput.FilePath == "" {
		return nil
	}
	content, ok := guard.ProposedContent(in, guard.ReadFile)
	if !ok {
		return nil
	}

	git, err := app.Repo(ctx)
	if err != nil {
		return nil
	}
	project, err := app.Project(ctx)
	if err != nil {
		return nil
	}
	client, err := app.Client()
	if err != nil {
		return nil
	}
	rel, err := git.Rel(git.Root, in.ToolInput.FilePath)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if sessionID := os.Getenv("ARCHYL_SESSION_ID"); sessionID != "" {
		status := "modified"
		if in.ToolName == "Write" {
			if _, err := os.Stat(in.ToolInput.FilePath); err != nil {
				status = "added"
			}
		}
		_ = client.ReportFiles(ctx, sessionID, []api.ChangedFile{{Path: rel, Status: status}})
	}

	if radar.IsManifest(rel) {
		if out := guardManifest(ctx, client, project.ID, rel, in.ToolInput.FilePath, content); out != nil {
			return out
		}
	}

	if repo.SecretPath(rel) {
		return nil
	}
	result, err := client.CheckConformance(ctx, project.ID, api.ConformanceCheckRequest{
		Branch:       "archyl-guard",
		ChangedFiles: []api.ChangedFile{{Path: rel, Status: "modified"}},
		FileContents: map[string]string{rel: content},
	})
	if err != nil {
		return nil
	}

	findings := make([]guard.Finding, 0, len(result.Violations))
	for _, v := range result.Violations {
		findings = append(findings, guard.Finding{Severity: v.Severity, Title: v.Title, Suggestion: v.Suggestion})
	}
	return guard.Decide(rel, result.Summary.Critical, result.Summary.High, findings, level).Output()
}

// guardManifest blocks an edit that adds a dependency the radar puts on hold.
// Dependencies on hold that were already there are not the agent's doing and
// do not block it.
func guardManifest(ctx context.Context, client *api.Client, projectID, rel, abs, proposed string) []byte {
	const beforePrefix = ".archyl-before/"
	manifests := []api.Manifest{{Path: rel, Content: proposed}}
	if current, err := os.ReadFile(abs); err == nil {
		manifests = append(manifests, api.Manifest{Path: beforePrefix + rel, Content: string(current)})
	}
	result, err := client.CheckDependencies(ctx, projectID, manifests)
	if err != nil {
		return nil
	}
	var before, after []api.CheckedDependency
	for _, d := range result.Dependencies {
		if strings.HasPrefix(d.FilePath, beforePrefix) {
			before = append(before, d)
		} else {
			after = append(after, d)
		}
	}
	added := radar.NewHolds(before, after)
	if len(added) == 0 {
		return nil
	}
	var lines []string
	for _, d := range added {
		line := fmt.Sprintf("- %s (%s) is on hold", d.Name, d.Technology.Name)
		if d.Technology.RadarNote != "" {
			line += " — " + d.Technology.RadarNote
		}
		lines = append(lines, line)
	}
	return guard.Decision{
		Deny: true,
		Reason: fmt.Sprintf("Archyl Guard: this change adds dependencies the technology radar puts on hold in %s:\n%s\n\n"+
			"Use the alternative the radar suggests, or ask the user whether to override it.", rel, strings.Join(lines, "\n")),
	}.Output()
}
