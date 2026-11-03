package commands

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

func newImpactCmd(app *App) *cobra.Command {
	var base string
	var depth int
	var showDiagram bool

	cmd := &cobra.Command{
		Use:   "impact",
		Short: "Show what your branch affects and who to tell",
		Long: `Compare the working tree with a base branch (committed, staged, unstaged and
untracked changes), place every changed file in the architecture, and walk the
relationships to find what depends on it — with the owners to notify.`,
		Example: `  archyl impact
  archyl impact --base origin/develop --depth 3
  archyl impact --diagram
  archyl impact --json | jq '.impact.notify'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			client, err := app.Client()
			if err != nil {
				return err
			}

			if base == "" {
				base = git.DefaultBase(ctx)
			}
			changes, err := git.Changes(ctx, base)
			if err != nil {
				return err
			}
			if len(changes) == 0 {
				if p.JSON {
					return p.WriteJSON(&api.LocateResult{Files: []api.FileLocation{}})
				}
				p.Println(p.Muted("No change against " + firstNonEmpty(base, "HEAD") + "."))
				return nil
			}

			paths := make([]string, 0, len(changes))
			for _, c := range changes {
				paths = append(paths, c.Path)
			}
			if len(paths) > maxPaths {
				paths = paths[:maxPaths]
			}

			var result *api.LocateResult
			if err := p.Spin(fmt.Sprintf("Tracing %s through the architecture…", ui.Plural(len(paths), "file", "files")), func() error {
				result, err = client.Locate(ctx, project.ID, paths, depth)
				return err
			}); err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(result)
			}
			renderImpact(p, result, changes, base)
			if showDiagram {
				return impactDiagram(cmd, app, project.ID, result)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch to compare with (default: origin's default branch)")
	cmd.Flags().IntVar(&depth, "depth", 2, "how many relationship hops to follow (1-6)")
	cmd.Flags().BoolVar(&showDiagram, "diagram", false, "draw the affected part of the architecture, touched elements highlighted")
	return cmd
}

// impactDiagram draws the smallest view holding what the change touches,
// its dependents fading with distance.
func impactDiagram(cmd *cobra.Command, app *App, projectID string, result *api.LocateResult) error {
	hl := diagram.FromImpact(result)
	if len(hl) == 0 {
		return nil
	}
	g, _, err := loadGraph(cmd.Context(), app, projectID)
	if err != nil {
		return err
	}
	return printDiagram(app, diagram.Around(g, hl), hl, formatAuto, "")
}

func renderImpact(p *ui.Printer, r *api.LocateResult, changes []repo.Change, base string) {
	t := p.Theme()
	touched := touchedByID(r)
	counts, order := filesPerElement(r)

	header := p.Brand() + p.Muted("  impact  ")
	if base != "" {
		header += p.Muted("vs " + base + " · ")
	}
	header += p.Muted(ui.Plural(len(changes), "changed file", "changed files"))
	p.Println()
	p.Println(header)

	if len(order) == 0 {
		p.Println()
		p.Println("  " + p.Muted("None of these files is in the model yet."))
		p.Println("  " + p.Hint("Check one with", "archyl where <file>"))
		p.Println()
		return
	}

	p.Println()
	p.Println(p.Heading(" Touched"))
	nameWidth := 0
	for _, id := range order {
		nameWidth = max(nameWidth, lipgloss.Width(touched[id].Name))
	}
	for _, id := range order {
		e := touched[id]
		name := lipgloss.NewStyle().Width(nameWidth + 2).Render(e.Name)
		glyph := lipgloss.NewStyle().Foreground(t.LevelColor(e.Type)).Render(ui.LevelGlyph(e.Type))
		parent := ""
		if i := strings.LastIndex(e.Path, " / "); i > 0 {
			parent = "in " + e.Path[:i]
		}
		p.Println(fmt.Sprintf("  %s %s%s  %s",
			glyph, p.Bold(name), p.Muted(fmt.Sprintf("%-9s", ui.Plural(counts[id], "file", "files"))), p.Muted(parent)))
	}

	var withDependents []api.TouchedElement
	for _, id := range order {
		if len(touched[id].Dependents) > 0 {
			withDependents = append(withDependents, touched[id])
		}
	}
	p.Println()
	p.Println(p.Heading(" Blast radius"))
	if len(withDependents) == 0 {
		p.Println("  " + p.Success("Nothing else depends on what you touched."))
	}
	for _, e := range withDependents {
		root := tree.Root(p.Element(e.Type, e.Name)).
			Enumerator(tree.RoundedEnumerator).
			EnumeratorStyle(lipgloss.NewStyle().Foreground(t.Subtle).PaddingRight(1))
		for _, d := range e.Dependents {
			hops := ui.Plural(d.Distance, "hop", "hops")
			line := p.Element(d.Type, d.Name) + "  " + p.Muted(relationVerb(d.RelationshipType)+" · "+hops)
			if len(d.Owners)+len(d.TeamOwners) > 0 {
				line += "  " + ownersLine(p, d.Owners, d.TeamOwners)
			}
			root.Child(line)
		}
		p.Println(indent(root.String(), 2))
	}

	p.Println()
	p.Println(p.Heading(" Notify"))
	if r.Impact != nil && len(r.Impact.Notify.Users)+len(r.Impact.Notify.Teams) > 0 {
		p.Println("  " + ownersLine(p, r.Impact.Notify.Users, r.Impact.Notify.Teams))
	} else {
		p.Println("  " + p.Muted("No owner is set on these elements."))
	}

	if n := len(r.Unmatched); n > 0 {
		p.Println()
		p.Println("  " + p.Muted(fmt.Sprintf("%s not in the model.", ui.Plural(n, "file is", "files are"))) +
			" " + p.Hint("Inspect with", "archyl where <file>"))
	}
	p.Println()
}

// relationVerb turns a relationship type ("reads_from") into words.
func relationVerb(relationshipType string) string {
	if relationshipType == "" {
		return "depends on"
	}
	return strings.ReplaceAll(relationshipType, "_", " ")
}
