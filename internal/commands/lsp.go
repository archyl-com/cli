package commands

import (
	"context"
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/lsp"
	"github.com/archyl-com/cli/internal/repo"
)

func newLSPCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "lsp",
		Short: "Architecture diagnostics and hovers in your editor (language server)",
		Long: `Run a Language Server over stdio for any editor with an LSP client.

  diagnostics   the conformance rules, run on each file you open or save
  hover         the file's element: owners, dependents, decisions
  code actions  "Ask Archyl why" and "Open in Archyl"

The editor starts it; it uses this machine's archyl session and the
repository's .archyl.json. When Archyl cannot be reached it logs one warning
and shows no diagnostics. Editor setup: docs/lsp.md.`,
		Example: `  # The editor runs it; to try it by hand, send an initialize request on stdin
  archyl lsp`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			server := lsp.NewServer(lsp.Options{
				Version: buildinfo.Version,
				Open: func(ctx context.Context, root string) (lsp.Backend, string, error) {
					return openLSPWorkspace(ctx, app, root)
				},
			})
			return server.Serve(cmd.Context(), os.Stdin, os.Stdout)
		},
	}
}

// openLSPWorkspace resolves the editor's workspace to a repository and its
// project, the way every other command does from the working directory.
func openLSPWorkspace(ctx context.Context, app *App, root string) (lsp.Backend, string, error) {
	git, err := repo.Open(ctx, root)
	if err != nil {
		return nil, "", err
	}
	projectID := firstNonEmpty(app.project, os.Getenv("ARCHYL_PROJECT_ID"))
	if projectID == "" {
		link, err := repo.ReadLink(git.Root)
		if errors.Is(err, repo.ErrNotLinked) {
			return nil, "", errors.New("this repository is not linked to an Archyl project: run archyl init")
		}
		if err != nil {
			return nil, "", err
		}
		projectID = link.ProjectID
	}
	client, err := app.Client()
	if err != nil {
		return nil, "", err
	}
	return &lsp.APIBackend{
		Client:    client,
		ProjectID: projectID,
		Branch:    git.Branch(ctx),
		AppURL:    app.appURL(),
	}, git.Root, nil
}
