package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
)

func newSessionCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Drive harness work sessions by hand or from scripts",
		Long: `Low-level access to the work-session protocol. Most of the time ` + "`archyl wrap`" + `
does all of this for you.`,
	}

	var all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List the project's work sessions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			sessions, err := client.Sessions(ctx, project.ID, !all)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(sessions)
			}
			if len(sessions) == 0 {
				p.Println(p.Muted("No active session."))
				return nil
			}
			for _, s := range sessions {
				p.Println(sessionLine(p, s) + p.Muted("  "+shortID(s.ID)+"  "+s.Status))
			}
			return nil
		},
	}
	list.Flags().BoolVar(&all, "all", false, "include ended sessions")

	var task, agent string
	var exclusive bool
	start := &cobra.Command{
		Use:     "start",
		Short:   "Open a work session and print its briefing",
		Example: `  id=$(archyl session start --task "Migrate invoices to v2" --agent ci --json | jq -r .session.id)`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			result, err := client.StartSession(ctx, project.ID, api.StartSessionRequest{AgentName: agent, Task: task, Exclusive: exclusive})
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(result)
			}
			renderBriefing(p, result)
			_, _ = fmt.Fprintln(p.Out, result.Session.ID)
			return nil
		},
	}
	start.Flags().StringVarP(&task, "task", "t", "", "what the work is about (required)")
	start.Flags().StringVar(&agent, "agent", "archyl-cli", "agent name recorded on the session")
	start.Flags().BoolVar(&exclusive, "exclusive", false, "refuse on lease conflicts")
	_ = start.MarkFlagRequired("task")

	heartbeat := &cobra.Command{
		Use:   "heartbeat <session-id>",
		Short: "Keep a session alive",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := app.Client()
			if err != nil {
				return err
			}
			return client.Heartbeat(cmd.Context(), args[0])
		},
	}

	var summary string
	var decisions, followUps []string
	var changeRequest bool
	finish := &cobra.Command{
		Use:   "finish [session-id]",
		Short: "Close a session with its outcome",
		Long: `Close a session. The ID defaults to $ARCHYL_SESSION_ID. Decisions become
memory replayed to future sessions; put situational findings in --follow-up.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := app.Printer
			id := os.Getenv("ARCHYL_SESSION_ID")
			if len(args) == 1 {
				id = args[0]
			}
			if id == "" {
				return fmt.Errorf("pass a session ID or set ARCHYL_SESSION_ID")
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			result, err := client.FinishSession(cmd.Context(), id, api.FinishSessionRequest{
				Summary: summary, Decisions: decisions, FollowUps: followUps, CreateChangeRequest: changeRequest,
			})
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(result)
			}
			line := p.Check() + " Session closed"
			if result.ChangeRequestNumber != nil {
				line += p.Muted(" · ") + p.Accent(fmt.Sprintf("change request #%d", *result.ChangeRequestNumber))
			}
			p.Println(line)
			return nil
		},
	}
	finish.Flags().StringVarP(&summary, "summary", "s", "", "what was done (required)")
	finish.Flags().StringArrayVar(&decisions, "decision", nil, "an ADR-worthy decision (repeatable)")
	finish.Flags().StringArrayVar(&followUps, "follow-up", nil, "something left for later (repeatable)")
	finish.Flags().BoolVar(&changeRequest, "change-request", false, "open a draft Architecture Change Request")
	_ = finish.MarkFlagRequired("summary")

	cancel := &cobra.Command{
		Use:   "cancel <session-id>",
		Short: "End a session without an outcome",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := app.Client()
			if err != nil {
				return err
			}
			if err := client.CancelSession(cmd.Context(), args[0]); err != nil {
				return err
			}
			app.Printer.Println(app.Printer.Check() + " Session cancelled")
			return nil
		},
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {

		if app.Printer.JSON {

			list.SetContext(cmd.Context())
			return list.RunE(list, args)

		}

		return cmd.Help()

	}

	cmd.AddCommand(list, start, heartbeat, finish, cancel)
	return cmd
}
