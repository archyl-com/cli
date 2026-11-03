package commands

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/agents"
	"github.com/archyl-com/cli/internal/completion"
	"github.com/archyl-com/cli/internal/update"
)

// addCompletionInstall puts `install` under cobra's completion command.
func addCompletionInstall(root *cobra.Command, app *App) {
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.AddCommand(newCompletionInstallCmd(app))
			return
		}
	}
}

func newCompletionInstallCmd(app *App) *cobra.Command {
	var shellName string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install shell completion for your shell",
		Long: `Write the completion script where your shell loads it from: a directory of
zsh's fpath you own (or ~/.zfunc), ~/.config/fish/completions, or
~/.local/share/bash-completion/completions. Running it again updates the script.

Your shell's startup file is only changed when it has to be (zsh without a
fpath directory of yours, PowerShell) and you agree.`,
		Example: `  archyl completion install
  archyl completion install --shell fish`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := app.Printer
			env := completionEnv()
			shell, err := completion.Detect(env)
			if shellName != "" {
				shell, err = completion.Parse(shellName)
			}
			if err != nil {
				return fmt.Errorf("%w (pass --shell)", err)
			}
			change, target, err := installCompletion(cmd.Root(), shell, env)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(map[string]any{"shell": shell, "changes": []agents.Change{change}, "rcLine": target.RCLine, "rcNeeded": completion.NeedsRCLine(target)})
			}
			marker := p.Check()
			if change.Action == "unchanged" {
				marker = p.Muted("·")
			}
			p.Println(fmt.Sprintf("%s %s %s", marker, p.Bold(tildePath(change.Path)), p.Muted(change.Action)))
			if target.Note != "" {
				p.Println(p.Muted("  " + target.Note))
			}
			if completion.NeedsRCLine(target) {
				add := false
				if p.Interactive() {
					err := huh.NewConfirm().
						Title("Add this line to " + tildePath(target.RCFile) + "?").
						Description(target.RCLine).
						Value(&add).
						Run()
					if err != nil {
						return err
					}
				}
				if !add {
					p.Println(p.Hint("To load it, add to "+tildePath(target.RCFile)+":", target.RCLine))
					return nil
				}
				if err := completion.AddRCLine(target); err != nil {
					return err
				}
				p.Println(fmt.Sprintf("%s %s %s", p.Check(), p.Bold(tildePath(target.RCFile)), p.Muted("updated")))
			}
			p.Println(p.Muted("Open a new " + string(shell) + " to use it."))
			return nil
		},
	}
	cmd.Flags().StringVar(&shellName, "shell", "", "bash, zsh, fish or powershell (default: $SHELL)")
	return cmd
}

func completionEnv() completion.Env {
	home, _ := os.UserHomeDir()
	return completion.Env{Getenv: os.Getenv, Home: home, GOOS: runtime.GOOS, Fpath: completion.ZshFpath}
}

func installCompletion(root *cobra.Command, shell completion.Shell, env completion.Env) (agents.Change, completion.Target, error) {
	target := completion.TargetFor(shell, env)
	script, err := completion.Script(root, shell)
	if err != nil {
		return agents.Change{}, target, err
	}
	action, err := completion.Install(target, script)
	if err != nil {
		return agents.Change{}, target, fmt.Errorf("install %s completion: %w", shell, err)
	}
	return agents.Change{Path: target.Path, Action: action}, target, nil
}

// completionOffer is the shell setup offers completion for: the user's,
// unless Homebrew installed it with archyl or it is already up to date.
func completionOffer(root *cobra.Command, env completion.Env) (completion.Shell, bool) {
	if exe, err := executable(); err == nil && update.DetectMethod(exe, env.Getenv) == update.MethodHomebrew {
		return "", false
	}
	shell, err := completion.Detect(env)
	if err != nil {
		return "", false
	}
	target := completion.TargetFor(shell, env)
	script, err := completion.Script(root, shell)
	if err != nil {
		return "", false
	}
	existing, err := os.ReadFile(target.Path)
	return shell, err != nil || !bytes.Equal(existing, script)
}

func tildePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
