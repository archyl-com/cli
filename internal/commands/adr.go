package commands

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
)

func newADRCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "adr",
		Aliases: []string{"adrs", "decisions"},
		Short:   "List and record architecture decisions",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listADRs(cmd, app)
		},
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List the project's decisions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listADRs(cmd, app)
		},
	})

	var req api.ADRRequest
	newCmd := &cobra.Command{
		Use:   "new [title]",
		Short: "Record a decision",
		Long: `Record an Architecture Decision Record. In a terminal, a form asks for what the
flags do not give; in scripts, pass at least the title.`,
		Example: `  archyl adr new
  archyl adr new "Use slog for structured logging" --status accepted \
    --context "Three logging libraries in use" --decision "slog everywhere"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			if len(args) == 1 {
				req.Title = args[0]
			}
			if p.Interactive() {
				if req.Status == "" {
					req.Status = "proposed"
				}
				form := huh.NewForm(
					huh.NewGroup(
						huh.NewInput().Title("Title").Placeholder("Use slog for structured logging").Value(&req.Title).
							Validate(func(s string) error {
								if strings.TrimSpace(s) == "" {
									return errors.New("a decision needs a title")
								}
								return nil
							}),
						huh.NewSelect[string]().Title("Status").Options(
							huh.NewOption("Proposed", "proposed"),
							huh.NewOption("Accepted", "accepted"),
						).Value(&req.Status),
					),
					huh.NewGroup(
						huh.NewText().Title("Context").Description("What forces are at play? What problem are we solving?").Value(&req.Context),
						huh.NewText().Title("Decision").Description("What did we decide?").Value(&req.Decision),
						huh.NewText().Title("Consequences").Description("What becomes easier, harder, or must now be true?").Value(&req.Consequences),
					),
				)
				if err := form.Run(); err != nil {
					return err
				}
			}
			if strings.TrimSpace(req.Title) == "" {
				return errors.New("pass a title")
			}

			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			adr, err := client.CreateADR(ctx, project.ID, req)
			if err != nil {
				return err
			}
			app.InvalidateModel(project.ID)
			if p.JSON {
				return p.WriteJSON(adr)
			}
			p.Println(p.Check() + " Recorded " + p.Accent(fmt.Sprintf("ADR-%d", adr.Number)) + " " + p.Bold(adr.Title) + p.Muted(" · "+adr.Status))
			p.Println(p.Hint("Read it back:", fmt.Sprintf("archyl show ADR-%d", adr.Number)))
			return nil
		},
	}
	newCmd.Flags().StringVar(&req.Status, "status", "", "proposed or accepted (default proposed)")
	newCmd.Flags().StringVar(&req.Context, "context", "", "why a decision is needed")
	newCmd.Flags().StringVar(&req.Decision, "decision", "", "what was decided")
	newCmd.Flags().StringVar(&req.Consequences, "consequences", "", "what follows from it")
	newCmd.Flags().StringSliceVar(&req.Tags, "tag", nil, "tag (repeatable)")
	cmd.AddCommand(newCmd)
	return cmd
}

func listADRs(cmd *cobra.Command, app *App) error {
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
	var adrs []api.ADR
	if err := p.Spin("Loading decisions…", func() error {
		adrs, err = client.ADRs(ctx, project.ID)
		return err
	}); err != nil {
		return err
	}
	if p.JSON {
		return p.WriteJSON(adrs)
	}
	if len(adrs) == 0 {
		p.Println(p.Muted("No decision recorded yet."))
		p.Println(p.Hint("Record the first one:", "archyl adr new"))
		return nil
	}
	for _, adr := range adrs {
		status := adr.Status
		switch status {
		case "accepted":
			status = p.Success(status)
		case "proposed":
			status = p.Accent(status)
		default:
			status = p.Muted(status)
		}
		p.Println(fmt.Sprintf("  %s  %s  %s", p.Accent(fmt.Sprintf("ADR-%-3d", adr.Number)), adr.Title, status))
	}
	p.Println()
	p.Println(p.Hint("Read one:", "archyl show ADR-<n>"))
	return nil
}
