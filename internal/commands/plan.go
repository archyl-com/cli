package commands

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
)

func newPlanCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "plan <task>",
		Short: "Plan a task against the documented architecture",
		Long: `Get an implementation plan grounded in the architecture: the steps, the
elements each one touches, the decisions and rules to respect, and the risks.
Agents: run it before any non-trivial change.`,
		Example: `  archyl plan "Add partial refunds to the payments flow"`,
		Args:    cobra.MinimumNArgs(1),
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
			var plan *api.WorkPlan
			if err := p.Spin("Planning against the architecture…", func() error {
				plan, err = client.Plan(ctx, project.ID, strings.Join(args, " "))
				return err
			}); err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(plan)
			}
			app.printMarkdown(plan.Markdown)
			if p.Interactive() {
				p.Println("  " + p.Hint("Run it under the harness:", `archyl wrap --task "`+truncate(plan.Task, 40)+`" -- claude`))
				p.Println()
			}
			return nil
		},
	}
}
