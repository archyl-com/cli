package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/agents"
	"github.com/archyl-com/cli/internal/completion"
)

func newSetupCmd(app *App) *cobra.Command {
	var selected []string
	var noGuard, noInstructions, noStatusline, noCompletion bool
	var transport string
	var completionShell completion.Shell
	var installShellCompletion bool

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Connect your coding agents to Archyl",
		Long: `Configure Claude Code, Cursor, VS Code and Codex to use Archyl's MCP server
with the coding tool profile, and install the Guard hook that checks every
edit against the architecture rules.

Configuration is written to the repository (.mcp.json, .cursor/mcp.json,
.vscode/mcp.json) so your team shares it, and holds no secret: each agent
signs in through Archyl itself. Codex is configured in ~/.codex/config.toml.`,
		Example: `  archyl setup
  archyl setup --agent claude-code --agent cursor
  archyl setup --agent claude-code --no-guard`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer

			git, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			sess, err := app.Session()
			if err != nil {
				return err
			}
			home, _ := os.UserHomeDir()
			env := agents.Env{Root: git.Root, Home: home}
			available := agents.Detect(env)

			if len(selected) == 0 {
				if !p.Interactive() {
					return fmt.Errorf("pass --agent (claude-code, cursor, vscode, codex) when not in a terminal")
				}
				options := make([]huh.Option[string], 0, len(available))
				for _, a := range available {
					label := a.Name
					if a.Detected {
						label += p.Muted("  detected")
					}
					options = append(options, huh.NewOption(label, a.ID).Selected(a.Detected))
				}
				installGuard, teach, statusline := !noGuard, !noInstructions, !noStatusline
				var completionGroup []*huh.Group
				if !noCompletion {
					if shell, ok := completionOffer(cmd.Root(), completionEnv()); ok {
						completionShell, installShellCompletion = shell, true
						completionGroup = append(completionGroup, huh.NewGroup(
							huh.NewConfirm().
								Title("Install "+string(shell)+" completion for archyl?").
								Description("Tab completes commands and flags. Written to "+tildePath(completion.TargetFor(shell, completionEnv()).Path)+"; your shell's startup files are left alone.").
								Value(&installShellCompletion),
						))
					}
				}
				form := huh.NewForm(append([]*huh.Group{
					huh.NewGroup(
						huh.NewMultiSelect[string]().
							Title("Which agents should use Archyl?").
							Options(options...).
							Value(&selected),
					),
					huh.NewGroup(
						huh.NewSelect[string]().
							Title("How should they connect?").
							Options(
								huh.NewOption("Through the archyl CLI — no separate sign-in per agent", string(agents.TransportCLI)),
								huh.NewOption("Directly over HTTP — each agent signs in to Archyl", string(agents.TransportHTTP)),
							).
							Value(&transport),
						huh.NewConfirm().
							Title("Teach them the archyl CLI?").
							Description("Adds a short block to CLAUDE.md / AGENTS.md: plan, brief, check, remember.").
							Value(&teach),
						huh.NewConfirm().
							Title("Install the Guard hook for Claude Code?").
							Description("Checks each edit against your architecture rules and technology radar before it is written.").
							Value(&installGuard),
						huh.NewConfirm().
							Title("Show Archyl in Claude Code's status line?").
							Description("Project, alignment score and agents at work, under the prompt.").
							Value(&statusline),
					),
				}, completionGroup...)...)
				if err := form.Run(); err != nil {
					return err
				}
				noGuard, noInstructions, noStatusline = !installGuard, !teach, !statusline
			}
			if len(selected) == 0 {
				p.Println(p.Muted("Nothing selected."))
				return nil
			}
			if transport != string(agents.TransportCLI) && transport != string(agents.TransportHTTP) {
				return fmt.Errorf("--transport must be cli or http")
			}

			url := agents.MCPURL(sess.Host)
			var changes []agents.Change
			var next []string
			for _, a := range available {
				if !slices.Contains(selected, a.ID) {
					continue
				}
				change, err := agents.ConfigureWith(env, a.ID, url, agents.Transport(transport))
				if err != nil {
					return fmt.Errorf("%s: %w", a.Name, err)
				}
				changes = append(changes, change)
				after := a.AfterSetup
				if agents.Transport(transport) == agents.TransportCLI {
					after = "Restart it; it reaches Archyl through `archyl mcp`, signed in as you."
				}
				next = append(next, p.Bold(a.Name)+p.Muted(" — "+after))
			}
			claude := slices.Contains(selected, "claude-code")
			if claude && !noGuard {
				change, err := agents.InstallGuard(env)
				if err != nil {
					return fmt.Errorf("guard hook: %w", err)
				}
				changes = append(changes, change)
			}
			if claude && !noStatusline {
				change, err := agents.InstallStatusline(env)
				if err != nil {
					return fmt.Errorf("status line: %w", err)
				}
				changes = append(changes, change)
			}
			if !noInstructions {
				var files []string
				if claude {
					files = append(files, "CLAUDE.md")
				}
				if slices.ContainsFunc(selected, func(id string) bool { return id != "claude-code" }) {
					files = append(files, "AGENTS.md")
				}
				for _, f := range files {
					change, err := agents.InstallInstructions(filepath.Join(git.Root, f))
					if err != nil {
						return fmt.Errorf("%s: %w", f, err)
					}
					changes = append(changes, change)
				}
			}

			if installShellCompletion {
				change, target, err := installCompletion(cmd.Root(), completionShell, completionEnv())
				if err != nil {
					return err
				}
				changes = append(changes, change)
				if completion.NeedsRCLine(target) {
					next = append(next, p.Bold(string(completionShell)+" completion")+p.Muted(" — add to "+tildePath(target.RCFile)+": ")+p.Code(target.RCLine))
				}
			}

			if p.JSON {
				return p.WriteJSON(map[string]any{"mcpUrl": url, "changes": changes})
			}
			p.Println()
			for _, c := range mergeChanges(changes) {
				path := c.Path
				if rel, err := filepath.Rel(git.Root, c.Path); err == nil && !strings.HasPrefix(rel, "..") {
					path = rel
				} else if strings.HasPrefix(path, home) {
					path = "~" + strings.TrimPrefix(path, home)
				}
				marker := p.Check()
				if c.Action == "unchanged" || strings.HasPrefix(c.Action, "kept") {
					marker = p.Muted("·")
				}
				p.Println(fmt.Sprintf("%s %s %s", marker, p.Bold(path), p.Muted(c.Action)))
			}
			p.Println()
			p.Println(p.Heading(" Next"))
			for _, line := range next {
				p.Println("  " + line)
			}
			p.Println()
			p.Println(p.Hint("Then put an agent to work under the harness:", "archyl wrap -- claude"))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&selected, "agent", nil, "agent to configure: claude-code, cursor, vscode, codex (repeatable)")
	cmd.Flags().StringVar(&transport, "transport", string(agents.TransportCLI), "cli (agents run `archyl mcp`, no sign-in each) or http (each agent signs in)")
	cmd.Flags().BoolVar(&noGuard, "no-guard", false, "do not install the Guard hook")
	cmd.Flags().BoolVar(&noInstructions, "no-instructions", false, "do not add the archyl block to CLAUDE.md / AGENTS.md")
	cmd.Flags().BoolVar(&noStatusline, "no-statusline", false, "do not set Claude Code's status line")
	cmd.Flags().BoolVar(&noCompletion, "no-completion", false, "do not offer to install shell completion")
	return cmd
}

// mergeChanges keeps one line per file: the first action wins, since a file
// created then updated in the same run was, to the user, created.
func mergeChanges(changes []agents.Change) []agents.Change {
	seen := map[string]int{}
	var out []agents.Change
	for _, c := range changes {
		if i, ok := seen[c.Path]; ok {
			if out[i].Action == "unchanged" {
				out[i].Action = c.Action
			}
			continue
		}
		seen[c.Path] = len(out)
		out = append(out, c)
	}
	return out
}
