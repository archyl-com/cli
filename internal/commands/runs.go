package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/runstate"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

func newRunsCmd(app *App) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:     "runs",
		Aliases: []string{"run"},
		Short:   "Follow and steer managed agent runs",
		Long: `List the project's managed agent runs, follow one live, and act on it: answer
the agent's question, review its plan, steer it, cancel it, or continue its
work in a new run on the same branch and pull request.

Runs are named by the first characters of their ID, or "last".`,
		Example: `  archyl runs
  archyl runs view             # pick one, follow it live
  archyl runs start "Add partial refunds" --profile backend
  archyl runs answer 3f2a9c4e "Keep the v1 endpoint"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listRuns(cmd.Context(), app, status)
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "only runs in this status (running, waiting_for_input, succeeded…)")

	view := &cobra.Command{
		Use:   "view [run]",
		Short: "Follow a run live and act on it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			run, err := pickRun(cmd.Context(), app, ref)
			if err != nil {
				return err
			}
			return followRun(cmd.Context(), app, run.ID)
		},
	}

	var profile string
	start := &cobra.Command{
		Use:   "start <task>",
		Short: "Start a managed agent run and follow it",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			profileID, err := pickProfile(ctx, app, client, profile)
			if err != nil {
				return err
			}
			run, err := client.StartRun(ctx, project.ID, profileID, strings.Join(args, " "))
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(run)
			}
			if !p.Interactive() {
				_, _ = fmt.Fprintln(p.Out, run.ID)
				return nil
			}
			return followRun(ctx, app, run.ID)
		},
	}
	start.Flags().StringVar(&profile, "profile", "", "agent profile, by name or ID (asked when there are several)")

	cmd.AddCommand(view, start,
		runActionCmd(app, "answer <run> <answer>", "Answer the question the agent is waiting on", 2,
			func(ctx context.Context, c *api.Client, run *api.Run, args []string) (string, error) {
				st, err := runState(ctx, c, run.ID)
				if err != nil {
					return "", err
				}
				if st.Pending() != "question" {
					return "", errors.New("the agent is not waiting on a question")
				}
				return "Answered", c.RespondToRun(ctx, run.ID, api.RunResponse{RequestID: st.Question.ID, Answer: strings.Join(args, " ")})
			}),
		runActionCmd(app, "approve <run>", "Approve the agent's plan, or a run held for approval", 0,
			func(ctx context.Context, c *api.Client, run *api.Run, _ []string) (string, error) {
				if run.Status == "awaiting_approval" {
					return "Run approved", c.ApproveRun(ctx, run.ID)
				}
				st, err := runState(ctx, c, run.ID)
				if err != nil {
					return "", err
				}
				if st.Pending() != "plan" {
					return "", errors.New("there is no plan waiting for review")
				}
				yes := true
				return "Plan approved", c.RespondToRun(ctx, run.ID, api.RunResponse{RequestID: st.Plan.ID, Approved: &yes})
			}),
		runActionCmd(app, "request-changes <run> <feedback>", "Send the agent's plan back with feedback", 2,
			func(ctx context.Context, c *api.Client, run *api.Run, args []string) (string, error) {
				st, err := runState(ctx, c, run.ID)
				if err != nil {
					return "", err
				}
				if st.Pending() != "plan" {
					return "", errors.New("there is no plan waiting for review")
				}
				no := false
				return "Changes requested", c.RespondToRun(ctx, run.ID, api.RunResponse{RequestID: st.Plan.ID, Approved: &no, Feedback: strings.Join(args, " ")})
			}),
		runActionCmd(app, "steer <run> <message>", "Tell a running agent something", 2,
			func(ctx context.Context, c *api.Client, run *api.Run, args []string) (string, error) {
				return "Sent to the agent", c.SteerRun(ctx, run.ID, strings.Join(args, " "))
			}),
		runActionCmd(app, "cancel <run>", "Stop a run", 0,
			func(ctx context.Context, c *api.Client, run *api.Run, _ []string) (string, error) {
				return "Run cancelled", c.CancelRun(ctx, run.ID)
			}),
		runActionCmd(app, "continue <run> <instructions>", "Continue an ended run's work in a new run", 2,
			func(ctx context.Context, c *api.Client, run *api.Run, args []string) (string, error) {
				next, err := c.ContinueRun(ctx, run.ID, strings.Join(args, " "))
				if err != nil {
					return "", err
				}
				return "Follow-up run " + tui.ShortRunID(next.ID) + " started", nil
			}),
	)
	return cmd
}

// runActionCmd builds a scriptable action on one run. minArgs counts the run
// reference: 2 means the run and some text.
func runActionCmd(app *App, use, short string, minArgs int,
	do func(ctx context.Context, c *api.Client, run *api.Run, args []string) (string, error),
) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MinimumNArgs(max(minArgs, 1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			run, err := pickRun(ctx, app, args[0])
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			msg, err := do(ctx, client, run, args[1:])
			if err != nil {
				return err
			}
			app.Printer.Println(app.Printer.Check() + " " + msg + app.Printer.Muted(" · run "+tui.ShortRunID(run.ID)))
			return nil
		},
	}
}

func runState(ctx context.Context, c *api.Client, id string) (*runstate.State, error) {
	events, err := c.RunEvents(ctx, id, -1)
	if err != nil {
		return nil, err
	}
	st := runstate.New()
	st.Apply(events)
	return st, nil
}

// pickRun resolves "last", an ID prefix, or, with no reference in a
// terminal, asks.
func pickRun(ctx context.Context, app *App, ref string) (*api.Run, error) {
	project, err := app.Project(ctx)
	if err != nil {
		return nil, err
	}
	client, err := app.Client()
	if err != nil {
		return nil, err
	}
	runs, err := client.Runs(ctx, project.ID, "", 100)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, errors.New("no agent run in this project yet — start one with archyl runs start \"<task>\"")
	}
	switch {
	case ref == "last":
		return &runs[0], nil
	case ref != "":
		var found []api.Run
		for _, r := range runs {
			if strings.HasPrefix(r.ID, ref) {
				found = append(found, r)
			}
		}
		switch len(found) {
		case 0:
			return nil, fmt.Errorf("no run starts with %q", ref)
		case 1:
			return &found[0], nil
		}
		return nil, fmt.Errorf("%q matches %d runs; give more characters", ref, len(found))
	}
	if !app.Printer.Interactive() {
		return nil, errors.New("name the run: an ID prefix, or last")
	}
	options := make([]huh.Option[int], 0, len(runs))
	for i, r := range runs {
		label := runStatusLabel(app.Printer, r.Status) + "  " + truncate(r.TaskText, 60) + app.Printer.Muted("  "+ago(r.CreatedAt))
		options = append(options, huh.NewOption(label, i))
	}
	choice := 0
	if err := huh.NewSelect[int]().Title("Which run?").Options(options...).Height(min(len(options)+2, 14)).Value(&choice).Run(); err != nil {
		return nil, err
	}
	return &runs[choice], nil
}

func pickProfile(ctx context.Context, app *App, client *api.Client, ref string) (string, error) {
	profiles, err := client.Profiles(ctx)
	if err != nil {
		return "", err
	}
	if len(profiles) == 0 {
		return "", errors.New("no agent profile in this organization — create one in the web app under Agents › Profiles")
	}
	if ref != "" {
		for _, pr := range profiles {
			if pr.ID == ref || strings.EqualFold(pr.Name, ref) {
				return pr.ID, nil
			}
		}
		return "", fmt.Errorf("no profile named %q", ref)
	}
	if len(profiles) == 1 {
		return profiles[0].ID, nil
	}
	if !app.Printer.Interactive() {
		return "", errors.New("several profiles exist; pass --profile")
	}
	options := make([]huh.Option[string], 0, len(profiles))
	for _, pr := range profiles {
		options = append(options, huh.NewOption(pr.Name+app.Printer.Muted("  "+truncate(pr.Description, 50)), pr.ID))
	}
	var chosen string
	err = huh.NewSelect[string]().Title("Which agent profile?").Options(options...).Value(&chosen).Run()
	return chosen, err
}

func followRun(ctx context.Context, app *App, runID string) error {
	client, err := app.Client()
	if err != nil {
		return err
	}
	for runID != "" {
		id := runID
		view := tui.NewRunView(app.Printer.Theme(), tui.RunActions{
			Run: func(ctx context.Context) (*api.Run, error) { return client.Run(ctx, id) },
			Events: func(ctx context.Context, since int64) ([]api.RunEvent, error) {
				return client.RunEvents(ctx, id, since)
			},
			Respond: func(ctx context.Context, r api.RunResponse) error { return client.RespondToRun(ctx, id, r) },
			Approve: func(ctx context.Context) error { return client.ApproveRun(ctx, id) },
			Steer:   func(ctx context.Context, m string) error { return client.SteerRun(ctx, id, m) },
			Cancel:  func(ctx context.Context) error { return client.CancelRun(ctx, id) },
			Continue: func(ctx context.Context, instructions string) (string, error) {
				next, err := client.ContinueRun(ctx, id, instructions)
				if err != nil {
					return "", err
				}
				return next.ID, nil
			},
			Open: auth.OpenBrowser,
		})
		if _, err := tea.NewProgram(view, tea.WithContext(ctx)).Run(); err != nil {
			return err
		}
		// A follow-up started from the view takes over the screen.
		runID = view.Next
	}
	return nil
}

func runStatusLabel(p *ui.Printer, status string) string {
	switch status {
	case "running":
		return p.Accent("◐ running ")
	case "waiting_for_input", "awaiting_approval":
		return p.Warning("● needs you")
	case "succeeded":
		return p.Success("✓ done    ")
	case "failed":
		return p.Danger("✗ failed  ")
	case "cancelled":
		return p.Muted("– cancelled")
	}
	return p.Muted("○ queued  ")
}

func listRuns(ctx context.Context, app *App, status string) error {
	p := app.Printer
	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	var runs []api.Run
	if err := p.Spin("Loading runs…", func() error {
		runs, err = client.Runs(ctx, project.ID, status, 30)
		return err
	}); err != nil {
		return err
	}
	if p.JSON {
		return p.WriteJSON(runs)
	}
	if len(runs) == 0 {
		p.Println(p.Muted("No agent run yet."))
		p.Println(p.Hint("Start one:", `archyl runs start "<task>"`))
		return nil
	}
	waiting := 0
	for _, r := range runs {
		if r.Status == "waiting_for_input" || r.Status == "awaiting_approval" {
			waiting++
		}
	}
	p.Println()
	for _, r := range runs {
		meta := []string{ago(r.CreatedAt)}
		if r.StartedAt != nil {
			end := time.Now()
			if r.EndedAt != nil {
				end = *r.EndedAt
			}
			meta = append(meta, end.Sub(*r.StartedAt).Round(time.Second).String())
		}
		if r.TotalCostCents > 0 {
			meta = append(meta, fmt.Sprintf("€%.2f", float64(r.TotalCostCents)/100))
		}
		line := fmt.Sprintf("  %s  %s  %s  %s", runStatusLabel(p, r.Status), p.Muted(tui.ShortRunID(r.ID)),
			truncate(r.TaskText, 56), p.Muted(strings.Join(meta, " · ")))
		if r.PRURL != "" {
			line += "  " + p.Accent("PR")
		}
		p.Println(line)
	}
	p.Println()
	if waiting > 0 {
		p.Println(p.Warning(fmt.Sprintf("  %s waiting on you. ", ui.Plural(waiting, "run is", "runs are"))) + p.Hint("Answer:", "archyl runs view"))
	} else {
		p.Println("  " + p.Hint("Follow one live:", "archyl runs view <id>"))
	}
	p.Println()
	return nil
}
