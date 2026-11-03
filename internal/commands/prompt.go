package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/update"
)

// promptState is what the shell prompt segment shows.
type promptState struct {
	Project  string `json:"project"`
	Score    int    `json:"score"`
	HasScore bool   `json:"hasScore"`
	Agents   int    `json:"agents"`
}

const promptTTL = 10 * time.Minute

// promptKey scopes the prompt state like the model cache, so another
// profile, host, organization or API key never shows it.
func promptKey(app *App, projectID string) (string, bool) {
	scope, ok := app.cacheScope()
	if !ok {
		return "", false
	}
	return scope.Key("prompt", projectID), true
}

// loadPrompt returns the cached prompt state, starting a background refresh
// when it is missing or older than promptTTL.
func loadPrompt(app *App, projectID string) *cache.Entry[promptState] {
	key, ok := promptKey(app, projectID)
	if !ok {
		return nil
	}
	entry, _ := cache.Load[promptState](key)
	if !entry.Fresh(promptTTL) {
		app.spawnForProject("prompt", projectID, "--refresh")
	}
	return entry
}

func newPromptCmd(app *App) *cobra.Command {
	var refresh bool

	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Print a short status for your shell prompt",
		Long: `Print the project's alignment score and active agents in a few characters, for
a shell prompt. It never waits on the network: it prints the last known state
and refreshes it in the background when it is older than ten minutes. Outside
a linked repository it prints nothing.

Starship (~/.config/starship.toml):

  [custom.archyl]
  command = "archyl prompt"
  when = "test -f .archyl.json"
  format = "[$output]($style) "
  style = "purple"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			ref, err := app.Project(ctx)
			if err != nil {
				return nil
			}
			if refresh {
				return refreshPrompt(ctx, app, ref.ID)
			}
			entry := loadPrompt(app, ref.ID)
			if entry == nil {
				return nil
			}
			_, _ = fmt.Fprintln(app.Printer.Out, promptSegment(entry.Value))
			return nil
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "fetch the state now instead of printing it")
	_ = cmd.Flags().MarkHidden("refresh")
	return cmd
}

func promptSegment(s promptState) string {
	parts := []string{"◆"}
	if s.HasScore {
		parts = append(parts, fmt.Sprint(s.Score))
	}
	if s.Agents > 0 {
		parts = append(parts, fmt.Sprintf("⚙%d", s.Agents))
	}
	return strings.Join(parts, " ")
}

func refreshPrompt(ctx context.Context, app *App, projectID string) error {
	client, err := app.Client()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var s promptState
	if project, err := client.Project(ctx, projectID); err == nil {
		s.Project = project.Name
	}
	if drift, err := client.LatestDrift(ctx, projectID); err == nil && drift != nil && drift.TotalElements > 0 {
		s.Score, s.HasScore = drift.Score, true
	}
	if sessions, err := client.Sessions(ctx, projectID, true); err == nil {
		s.Agents = len(sessions)
	}
	key, ok := promptKey(app, projectID)
	if !ok {
		return nil
	}
	return cache.Save(key, s)
}

// spawnSelf runs archyl again in the background, detached from this process,
// for work the user should never wait on.
func spawnSelf(args ...string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, args...)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func newUpdateCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "__update-check",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := update.Fetch(cmd.Context())
			return err
		},
	}
}

func newStatuslineCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Status line for Claude Code",
		Long: `Print one line for Claude Code's status line: the project, its alignment score
and the agents at work. It reads Claude Code's JSON on stdin to find the
workspace, answers from the cache and refreshes it in the background.

` + "`archyl setup`" + ` can install it; by hand, in .claude/settings.json:

  "statusLine": {"type": "command", "command": "archyl statusline"}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var input struct {
				Workspace struct {
					CurrentDir string `json:"current_dir"`
				} `json:"workspace"`
			}
			if !term.IsTerminal(os.Stdin.Fd()) {
				_ = json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&input)
			}
			if dir := input.Workspace.CurrentDir; dir != "" {
				_ = os.Chdir(dir)
			}
			ref, err := app.Project(cmd.Context())
			if err != nil {
				return nil
			}
			entry := loadPrompt(app, ref.ID)
			if entry == nil {
				fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Render("◆ archyl"))
				return nil
			}
			fmt.Println(statusline(ui.NewTheme(true), entry.Value, os.Getenv("ARCHYL_SESSION_ID") != ""))
			return nil
		},
	}
}

// statusline renders Claude Code's status line. Colors are kept even though
// stdout is a pipe: Claude Code renders them.
func statusline(t ui.Theme, s promptState, inSession bool) string {
	parts := []string{lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("◆ " + firstNonEmpty(s.Project, "archyl"))}
	if s.HasScore {
		c := t.Success
		switch {
		case s.Score < 60:
			c = t.Danger
		case s.Score < 80:
			c = t.Warning
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(c).Render(fmt.Sprintf("alignment %d", s.Score)))
	}
	if s.Agents > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Muted).Render(ui.Plural(s.Agents, "agent at work", "agents at work")))
	}
	if inSession {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Success).Render("● harness session"))
	}
	return strings.Join(parts, lipgloss.NewStyle().Foreground(t.Muted).Render(" · "))
}
