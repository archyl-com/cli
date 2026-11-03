package commands

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/tui"
)

func newAgentsCmd(app *App) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Watch the agents at work on the project, live",
		Long: `A live view of every work session on the project: which agent, on what task,
on which elements, and which files it changed. Elements leased by more than
one agent are flagged, so two agents do not collide on the same code.

Piped or with --json, it prints the sessions once.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}

			if !p.Interactive() {
				sessions, err := client.Sessions(ctx, ref.ID, !all)
				if err != nil {
					return err
				}
				if p.JSON {
					return p.WriteJSON(sessions)
				}
				if len(sessions) == 0 {
					_, _ = fmt.Fprintln(p.Out, "No agent at work.")
				}
				for _, s := range sessions {
					p.Println(sessionLine(p, s))
				}
				return nil
			}

			name := ref.Name
			if name == "" {
				if project, err := client.Project(ctx, ref.ID); err == nil {
					name = project.Name
				}
			}
			view := tui.NewAgents(tui.AgentsOptions{
				Theme:   p.Theme(),
				Project: name,
				Sessions: func(ctx context.Context, showAll bool) ([]api.Session, error) {
					return client.Sessions(ctx, ref.ID, !showAll)
				},
				Files: client.SessionFiles,
			})
			_, err = tea.NewProgram(view, tea.WithContext(ctx)).Run()
			return err
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include recently ended sessions")
	return cmd
}
