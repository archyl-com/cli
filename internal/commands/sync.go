package commands

import (
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func newSyncCmd(app *App) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "sync [file]",
		Short: "Push archyl.yaml to the project (same as dsl push)",
		Long: `Import an architecture-as-code file (archyl.yaml by default, at the repository
root) into the project. Same as archyl dsl push: new elements are created,
existing ones updated, nothing deleted. In a terminal it shows the diff and
asks first; --yes skips the question.`,
		Example: `  archyl sync
  archyl sync docs/architecture/archyl.yaml --yes`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file := ""
			if len(args) == 1 {
				file = args[0]
			}
			return pushDSL(cmd.Context(), app, dslPath(cmd.Context(), app, file), yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "push without showing the diff and asking")
	return cmd
}

func dslCreated(p *ui.Printer, r *api.DSLImport) []string {
	var out []string
	for _, c := range []struct {
		n                int
		singular, plural string
	}{
		{r.SystemsCreated, "system", "systems"},
		{r.ContainersCreated, "container", "containers"},
		{r.ComponentsCreated, "component", "components"},
		{r.CodeElementsCreated, "code element", "code elements"},
		{r.RelationshipsCreated, "relationship", "relationships"},
		{r.TechnologiesCreated, "technology", "technologies"},
		{r.ADRsCreated, "ADR", "ADRs"},
		{r.DocsCreated, "doc", "docs"},
		{r.APIContractsCreated, "API contract", "API contracts"},
		{r.EventsCreated, "event channel", "event channels"},
		{r.EnvironmentsCreated, "environment", "environments"},
		{r.ReleasesCreated, "release", "releases"},
	} {
		if c.n > 0 {
			out = append(out, p.Bold(ui.Plural(c.n, c.singular, c.plural)))
		}
	}
	return out
}
