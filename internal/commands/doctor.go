package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/agents"
	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/update"
)

type checkState int

const (
	checkOK checkState = iota
	checkWarn
	checkFail
	checkSkip
)

// diagnosis is one line of `archyl doctor`.
type diagnosis struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	state  checkState
}

func newDoctorCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that everything is set up, and say how to fix what is not",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			var results []diagnosis
			err := p.Spin("Checking your setup…", func() error {
				results = runDiagnosis(ctx, app)
				return nil
			})
			if err != nil {
				return err
			}

			failed := 0
			for _, d := range results {
				if d.state == checkFail {
					failed++
				}
			}
			if p.JSON {
				if err := p.WriteJSON(results); err != nil {
					return err
				}
			} else {
				p.Println()
				p.Println("  " + p.Brand() + p.Muted("  doctor"))
				p.Println()
				width := 0
				for _, d := range results {
					width = max(width, len(d.Name))
				}
				for _, d := range results {
					marker := p.Check()
					switch d.state {
					case checkWarn:
						marker = p.Bang()
					case checkFail:
						marker = p.Cross()
					case checkSkip:
						marker = p.Muted("·")
					}
					p.Println(fmt.Sprintf("  %s %s  %s", marker, p.Bold(fmt.Sprintf("%-*s", width, d.Name)), p.Muted(d.Detail)))
					if d.Fix != "" {
						p.Println(fmt.Sprintf("    %s %s", strings.Repeat(" ", width), p.Hint("fix:", d.Fix)))
					}
				}
				p.Println()
				if failed == 0 {
					p.Println("  " + p.Success("All good.") + p.Muted(" You are ready to go."))
				} else {
					p.Println("  " + p.Danger(fmt.Sprintf("%s to fix.", pluralWord(failed, "One thing", fmt.Sprintf("%d things", failed)))))
				}
				p.Println()
			}
			if failed > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
}

func runDiagnosis(ctx context.Context, app *App) []diagnosis {
	var out []diagnosis
	add := func(name string, state checkState, detail, fix string) {
		labels := map[checkState]string{checkOK: "ok", checkWarn: "warn", checkFail: "fail", checkSkip: "skip"}
		out = append(out, diagnosis{Name: name, State: labels[state], Detail: detail, Fix: fix, state: state})
	}

	version := buildinfo.Version
	if latest, _ := cache.Load[string](update.CacheKey); latest != nil && update.Newer(version, latest.Value) {
		add("version", checkWarn, version+", "+latest.Value+" is available", "brew upgrade archyl")
	} else {
		add("version", checkOK, version, "")
	}

	sess, err := app.Session()
	if err != nil {
		add("profile", checkFail, err.Error(), "archyl login")
		return out
	}
	add("profile", checkOK, sess.Name+" · "+sess.Host, "")

	cred, err := sess.Credential()
	signedIn := err == nil
	if err == nil {
		method := "browser sign-in"
		if cred.APIKey != "" {
			method = "API key"
		}
		start := time.Now()
		user, meErr := sess.Client().Me(ctx)
		if meErr != nil {
			add("account", checkFail, meErr.Error(), "archyl login")
			signedIn = false
		} else {
			add("account", checkOK, firstNonEmpty(user.Email, user.ID)+" · "+method, "")
			latency := time.Since(start).Round(time.Millisecond)
			state := checkOK
			if latency > 1500*time.Millisecond {
				state = checkWarn
			}
			add("api", state, fmt.Sprintf("%s answered in %s", hostLabel(sess.Host), latency), "")
		}
	} else {
		add("account", checkFail, "not signed in", "archyl login")
	}

	git, err := app.Repo(ctx)
	if err != nil {
		add("repository", checkSkip, "not inside a git repository", "")
		return out
	}
	add("repository", checkOK, filepath.Base(git.Root)+" · "+firstNonEmpty(git.Branch(ctx), "no branch"), "")

	ref, err := app.Project(ctx)
	switch {
	case err != nil:
		add("project link", checkFail, "no "+repo.LinkFile, "archyl init")
	case signedIn:
		client, _ := app.Client()
		if project, perr := client.Project(ctx, ref.ID); perr != nil {
			add("project link", checkFail, ref.ID+": "+perr.Error(), "archyl init --force")
		} else {
			add("project link", checkOK, project.Name+projectSource(app), "")
			if _, derr := client.DiffDSL(ctx, ref.ID, api.DSLSource{}); api.IsUnsupported(derr) {
				add("server", checkWarn, hostLabel(sess.Host)+" predates some CLI commands (dsl diff, discover, tool calls)", "update the Archyl server")
			} else {
				add("server", checkOK, "supports every command", "")
			}
		}
	default:
		add("project link", checkOK, firstNonEmpty(ref.Name, ref.ID)+projectSource(app), "")
	}

	var configured []string
	for _, f := range []struct{ name, path, key string }{
		{"Claude Code", ".mcp.json", "mcpServers"},
		{"Cursor", ".cursor/mcp.json", "mcpServers"},
		{"VS Code", ".vscode/mcp.json", "servers"},
	} {
		if hasArchylServer(filepath.Join(git.Root, f.path), f.key) {
			configured = append(configured, f.name)
		}
	}
	if len(configured) == 0 {
		add("agents", checkWarn, "no coding agent is connected to Archyl in this repository", "archyl setup")
	} else {
		add("agents", checkOK, strings.Join(configured, ", "), "")
	}

	if guardInstalled(filepath.Join(git.Root, ".claude", "settings.json")) {
		add("guard", checkOK, "checks every Claude Code edit", "")
	} else {
		add("guard", checkWarn, "Claude Code edits are not checked against the rules", "archyl setup --agent claude-code")
	}

	if dir, err := git.HooksDir(ctx); err == nil {
		raw, err := os.ReadFile(filepath.Join(dir, "pre-push"))
		if err == nil && strings.Contains(string(raw), hookMarker) {
			add("pre-push", checkOK, "violations block git push", "")
		} else {
			add("pre-push", checkSkip, "not installed (optional)", "archyl hooks install")
		}
	}
	return out
}

func hasArchylServer(path, key string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var doc map[string]map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	_, ok := doc[key]["archyl"]
	return ok
}

func guardInstalled(path string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(raw), agents.GuardCommand)
}

// projectSource says where the project came from when it is not the link file.
func projectSource(app *App) string {
	switch {
	case app.project != "":
		return " (from --project)"
	case os.Getenv("ARCHYL_PROJECT_ID") != "":
		return " (from ARCHYL_PROJECT_ID)"
	}
	return ""
}
