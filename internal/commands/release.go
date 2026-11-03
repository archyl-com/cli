package commands

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ci"
)

func newReleaseCmd(app *App) *cobra.Command {
	var req api.ReleaseRequest

	cmd := &cobra.Command{
		Use:   "release [version]",
		Short: "Record a release of this project",
		Long: `Record a release in Archyl, to correlate architecture changes with what
shipped and feed the DORA metrics. The version defaults to the tag being built
in CI, then to the tag on HEAD; the link defaults to the CI run.`,
		Example: `  archyl release v2.4.0 --environment production
  archyl release --status deployed --changelog "$(git log --oneline v2.3.0..HEAD)"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			env := ci.Detect(os.Getenv)

			if len(args) == 1 {
				req.Version = args[0]
			}
			if req.Version == "" {
				req.Version = env.Tag
			}
			if req.Version == "" {
				if git, err := app.Repo(ctx); err == nil {
					req.Version = git.TagAtHead(ctx)
				}
			}
			if req.Version == "" {
				return errors.New("no version: pass one, or run on a tagged commit")
			}
			if req.SourceURL == "" {
				req.SourceURL = env.BuildURL
			}
			req.Source = "api"
			if env.Provider == ci.GitHub {
				req.Source = "github_action"
			}

			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			release, err := client.IngestRelease(ctx, project.ID, req)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(release)
			}
			line := p.Check() + " Recorded release " + p.Bold(req.Version)
			if req.Environment != "" {
				line += p.Muted(" to ") + req.Environment
			}
			p.Println(line + p.Muted(" · "+firstNonEmpty(release.Status, req.Status)))
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Status, "status", "deployed", "release status")
	cmd.Flags().StringVar(&req.Environment, "environment", "", "environment it shipped to")
	cmd.Flags().StringVar(&req.Changelog, "changelog", "", "release notes")
	cmd.Flags().StringVar(&req.SystemID, "system", "", "system ID the release belongs to")
	cmd.Flags().StringVar(&req.ContainerID, "container", "", "container ID the release belongs to")
	cmd.Flags().StringVar(&req.SourceURL, "url", "", "link to the release or pipeline (default: the CI run)")
	return cmd
}
