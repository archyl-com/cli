package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

func newWrapCmd(app *App) *cobra.Command {
	var task, agentName string
	var exclusive, changeRequest, noChangeRequest bool

	cmd := &cobra.Command{
		Use:   "wrap [flags] -- <agent> [args...]",
		Short: "Run a coding agent inside an Archyl work session",
		Long: `Open a work session for the task, brief the agent with the relevant
architecture, keep the session alive while it works, then attribute the files
it changed and close the session — optionally opening a draft Architecture
Change Request for a human to review.

Claude Code receives the briefing as an appended system prompt. Every agent
gets ARCHYL_SESSION_ID and ARCHYL_BRIEFING_FILE in its environment, and the
Guard hook reports each edit to the session.`,
		Example: `  archyl wrap -- claude
  archyl wrap --task "Add PDF export to invoices" -- claude
  archyl wrap --agent codex --exclusive -- codex "fix the flaky checkout test"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer

			git, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			if _, err := app.Client(); err != nil {
				return err
			}
			if agentName == "" {
				agentName = filepath.Base(args[0])
			}
			if task == "" {
				task, err = askTask(p, args)
				if err != nil {
					return err
				}
			}

			code, err := runHarnessed(ctx, app, git, project, harnessRun{
				Task:          task,
				AgentName:     agentName,
				Args:          args,
				Exclusive:     exclusive,
				ChangeRequest: changeRequest,
				AskFinish:     !changeRequest && !noChangeRequest,
			})
			if err != nil {
				return err
			}
			return exitWith(code)
		},
	}
	cmd.Flags().StringVarP(&task, "task", "t", "", "what the agent is about to do (asked when omitted)")
	cmd.Flags().StringVar(&agentName, "agent", "", "agent name recorded on the session (default: the command name)")
	cmd.Flags().BoolVar(&exclusive, "exclusive", false, "refuse to start when another session holds a lease on the same elements")
	cmd.Flags().BoolVar(&changeRequest, "change-request", false, "open a draft Architecture Change Request without asking")
	cmd.Flags().BoolVar(&noChangeRequest, "no-change-request", false, "never ask to open a change request")
	return cmd
}

func askTask(p *ui.Printer, args []string) (string, error) {
	// The agent's flags say nothing about the work.
	var words []string
	for _, arg := range args[1:] {
		if !strings.HasPrefix(arg, "-") {
			words = append(words, arg)
		}
	}
	fallback := strings.Join(words, " ")
	if fallback == "" && len(args) > 1 {
		fallback = args[0] + " session"
	}
	if !p.Interactive() {
		if fallback == "" {
			return "", errors.New("pass --task to describe the work")
		}
		return fallback, nil
	}
	task := fallback
	err := huh.NewInput().
		Title("What is the agent going to do?").
		Description("Archyl uses it to pick the relevant architecture and lease the elements.").
		Placeholder("Add PDF export to invoices").
		Value(&task).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("describe the task in a few words")
			}
			return nil
		}).
		Run()
	return strings.TrimSpace(task), err
}

func renderBriefing(p *ui.Printer, r *api.StartSessionResult) {
	gate := p.Success("● clear")
	switch r.Session.Gate {
	case "warn":
		gate = p.Warning("● proceed with care")
	case "deny":
		gate = p.Danger("● denied")
	}

	var leased []string
	for _, l := range r.Session.Leases {
		leased = append(leased, p.Element(l.ElementType, l.ElementName))
	}
	rows := [][2]string{{"gate", gate}}
	if len(leased) > 0 {
		rows = append(rows, [2]string{"working on", strings.Join(leased, p.Muted(", "))})
	}
	if r.Context != nil {
		if n := len(r.Context.ADRs); n > 0 {
			var adrs []string
			for i, a := range r.Context.ADRs {
				if i == 3 {
					adrs = append(adrs, p.Muted(fmt.Sprintf("+%d", n-3)))
					break
				}
				adrs = append(adrs, fmt.Sprintf("ADR-%d %s", a.Number, p.Muted(truncate(a.Title, 30))))
			}
			rows = append(rows, [2]string{"decisions", strings.Join(adrs, p.Muted(" · "))})
		}
		if n := len(r.Context.Guardrails); n > 0 {
			rows = append(rows, [2]string{"guardrails", ui.Plural(n, "rule applies", "rules apply")})
		}
	}
	for _, c := range r.Conflicts {
		rows = append(rows, [2]string{"conflict", p.Warning(c.AgentName) + p.Muted(" holds ") + c.ElementName + p.Muted(" — “"+truncate(c.Task, 30)+"”")})
	}
	for _, reason := range r.Session.GateReasons {
		rows = append(rows, [2]string{"", p.Muted(reason)})
	}

	title := p.Brand() + p.Muted("  work session ") + p.Muted(shortID(r.Session.ID))
	p.Eprintln(p.Panel(title + "\n\n" + p.KeyValues(rows)))
}

func askFinish(p *ui.Printer, finish *api.FinishSessionRequest, changes []repo.Change, took time.Duration) error {
	if len(changes) == 0 {
		return nil
	}
	return huh.NewForm(huh.NewGroup(
		huh.NewText().
			Title(fmt.Sprintf("Session summary  %s", p.Muted(ui.Plural(len(changes), "file", "files")+" changed in "+took.Round(time.Minute).String()))).
			Description("What changed, for the people and agents who come next.").
			Value(&finish.Summary),
		huh.NewConfirm().
			Title("Open a draft Architecture Change Request?").
			Description("A human reviews how the model should catch up with this change.").
			Value(&finish.CreateChangeRequest),
	)).Run()
}

func defaultSummary(agent, task string, changes []repo.Change, exitCode int) string {
	summary := fmt.Sprintf("%s worked on: %s. %s changed.", agent, task, ui.Plural(len(changes), "file", "files"))
	if exitCode != 0 {
		summary += fmt.Sprintf(" The agent exited with code %d.", exitCode)
	}
	return summary
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func exitWith(code int) error {
	if code == 0 {
		return nil
	}
	return &ExitError{Code: code}
}
