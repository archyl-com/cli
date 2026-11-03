package commands

import (
	"fmt"
	"os"

	"github.com/archyl-com/cli/internal/ui"
	"github.com/spf13/cobra"
)

func newContextCmd(app *App) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "context",
		Short: "Print the project's architecture context for AI agents",
		Long: `Print the project's architecture as markdown tuned for coding agents: systems,
containers, components, relationships, decisions and rules. Pipe it, or write
it to the file your agent reads.`,
		Example: `  archyl context > ARCHITECTURE.md
  archyl context -o .archyl/context.md`,
		Args: cobra.NoArgs,
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

			var markdown string
			if err := p.Spin("Building context…", func() error {
				markdown, err = client.AgentContext(ctx, project.ID)
				return err
			}); err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(map[string]string{"projectId": project.ID, "markdown": markdown})
			}
			if output == "" {
				_, err := fmt.Fprint(p.Out, ui.Sanitize(markdown))
				return err
			}
			if err := os.WriteFile(output, []byte(markdown), 0o644); err != nil {
				return err
			}
			p.Eprintln(p.Check() + " Wrote " + p.Bold(output) + p.Muted(fmt.Sprintf(" (%d KB)", len(markdown)/1024+1)))
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "write to a file instead of stdout")
	return cmd
}
