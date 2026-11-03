package commands

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/radar"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/report"
	"github.com/archyl-com/cli/internal/ui"
)

// maxManifests matches the API's limit per check.
const maxManifests = 50

func newRadarCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "radar",
		Short: "Show the technology radar: adopt, trial, assess, hold",
		Long: `Show the organization's technologies by governance ring, with how much of the
architecture uses each. ` + "`archyl radar check`" + ` compares this repository's dependencies
with it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			client, err := app.Client()
			if err != nil {
				return err
			}
			var techs []api.RadarTechnology
			if err := p.Spin("Loading the radar…", func() error {
				techs, err = client.Radar(ctx)
				return err
			}); err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(techs)
			}
			renderRadar(p, techs)
			return nil
		},
	}
	cmd.AddCommand(newRadarCheckCmd(app))
	return cmd
}

func ringColor(t ui.Theme, ring string) lipgloss.Style {
	c := t.Muted
	switch ring {
	case "adopt":
		c = t.Success
	case "trial":
		c = t.Accent
	case "assess":
		c = t.Warning
	case "hold":
		c = t.Danger
	}
	return lipgloss.NewStyle().Foreground(c)
}

func renderRadar(p *ui.Printer, techs []api.RadarTechnology) {
	t := p.Theme()
	byRing := map[string][]api.RadarTechnology{}
	for _, tech := range techs {
		byRing[tech.RadarRing] = append(byRing[tech.RadarRing], tech)
	}
	for _, list := range byRing {
		sort.Slice(list, func(i, j int) bool {
			ui, uj := list[i].ElementCount+list[i].RelationshipCount, list[j].ElementCount+list[j].RelationshipCount
			if ui != uj {
				return ui > uj
			}
			return list[i].Name < list[j].Name
		})
	}

	const colWidth = 26
	var bodies [][]string
	for _, ring := range []string{"adopt", "trial", "assess", "hold"} {
		style := ringColor(t, ring)
		lines := []string{style.Bold(true).Render(strings.ToUpper(ring)) + p.Muted(fmt.Sprintf("  %d", len(byRing[ring]))), ""}
		if len(byRing[ring]) == 0 {
			lines = append(lines, p.Muted("—"))
		}
		for i, tech := range byRing[ring] {
			if i == 12 {
				lines = append(lines, p.Muted(fmt.Sprintf("+%d more", len(byRing[ring])-12)))
				break
			}
			uses := tech.ElementCount + tech.RelationshipCount
			name := truncate(tech.Name, colWidth-6)
			line := style.Render("● ") + name
			if uses > 0 {
				line += p.Muted(fmt.Sprintf(" %d", uses))
			}
			lines = append(lines, line)
			if ring == "hold" && tech.RadarNote != "" {
				lines = append(lines, "  "+p.Muted(truncate(tech.RadarNote, colWidth-8)))
			}
		}
		bodies = append(bodies, lines)
	}
	height := 0
	for _, lines := range bodies {
		height = max(height, len(lines))
	}
	var columns []string
	for _, lines := range bodies {
		columns = append(columns, lipgloss.NewStyle().Width(colWidth).Height(height).PaddingLeft(2).Render(strings.Join(lines, "\n")))
	}

	p.Println()
	p.Println("  " + p.Brand() + p.Muted("  technology radar"))
	p.Println()
	p.Println(lipgloss.JoinHorizontal(lipgloss.Top, columns...))
	if n := len(byRing[""]); n > 0 {
		var names []string
		for i, tech := range byRing[""] {
			if i == 10 {
				names = append(names, fmt.Sprintf("+%d", n-10))
				break
			}
			names = append(names, tech.Name)
		}
		p.Println(p.Muted(fmt.Sprintf("  Not classified (%d): ", n)) + strings.Join(names, p.Muted(", ")))
	}
	p.Println()
	p.Println("  " + p.Hint("Check this repository's dependencies:", "archyl radar check"))
	p.Println()
}

func newRadarCheckCmd(app *App) *cobra.Command {
	var failOnFlag, format, output string
	var changedOnly bool
	var base string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check this repository's dependencies against the radar",
		Long: `Read the repository's dependency manifests (go.mod, package.json, pom.xml,
requirements.txt, Cargo.toml and more), match each dependency to the radar,
and fail when one is on hold. Output follows the CI like ` + "`archyl check`" + `.`,
		Example: `  archyl radar check
  archyl radar check --changed --fail-on assess
  archyl radar check --format sarif -o radar.sarif`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			env := ci.Detect(os.Getenv)
			format = resolveFormat(format, p.JSON, env)
			failRank := map[string]int{"hold": 4, "assess": 3, "never": 99}[failOnFlag]
			if failRank == 0 {
				return fmt.Errorf("--fail-on must be hold, assess or never")
			}

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

			var paths []string
			if changedOnly {
				if base, err = resolveBase(ctx, git, base, env); err != nil {
					return err
				}
				changes, err := git.Changes(ctx, base)
				if err != nil {
					return err
				}
				for _, c := range changes {
					if c.Status != "deleted" {
						paths = append(paths, c.Path)
					}
				}
			} else if paths, err = git.Files(ctx); err != nil {
				return err
			}

			var manifests []api.Manifest
			contents := map[string]string{}
			for _, path := range paths {
				if !radar.IsManifest(path) || radar.Skip(path) || len(manifests) == maxManifests {
					continue
				}
				raw, reason := repo.ReadForUpload(git.Root, path, 1<<20)
				if reason != "" {
					continue
				}
				manifests = append(manifests, api.Manifest{Path: path, Content: string(raw)})
				contents[path] = string(raw)
			}
			if len(manifests) == 0 {
				if p.JSON {
					return p.WriteJSON(&api.DependencyCheck{})
				}
				p.Println(p.Muted("No dependency manifest to check."))
				return nil
			}

			var result *api.DependencyCheck
			if err := p.Spin(fmt.Sprintf("Checking %s against the radar…", ui.Plural(len(manifests), "manifest", "manifests")), func() error {
				result, err = client.CheckDependencies(ctx, project.ID, manifests)
				return err
			}); err != nil {
				return err
			}

			violations := radar.Violations(result.Dependencies, func(file, name string) int {
				return radar.DeclarationLine(contents[file], name)
			})
			names := map[string]string{}
			for _, r := range radar.Rules() {
				names[r.ID] = r.Name
			}

			switch format {
			case "json":
				if err := p.WriteJSON(result); err != nil {
					return err
				}
			case "sarif":
				if err := withOutput(p, output, func(w io.Writer) error { return report.SARIF(w, violations, radar.Rules(), buildinfo.Version) }); err != nil {
					return err
				}
			case "gitlab":
				if output == "" {
					output = "gl-code-quality-radar.json"
				}
				if err := withOutput(p, output, func(w io.Writer) error { return report.GitLabCodeQuality(w, violations, names) }); err != nil {
					return err
				}
				renderRadarCheck(p, result, len(manifests))
			case "github":
				if err := report.GitHubAnnotations(p.Out, violations, names); err != nil {
					return err
				}
				renderRadarCheck(p, result, len(manifests))
			default:
				renderRadarCheck(p, result, len(manifests))
			}

			for _, d := range result.Dependencies {
				if radar.Rank(d.Ring()) >= failRank {
					return &ExitError{Code: 1}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&failOnFlag, "fail-on", "hold", "ring that fails the check: hold, assess or never")
	cmd.Flags().BoolVar(&changedOnly, "changed", false, "only check manifests changed against the base")
	cmd.Flags().StringVar(&base, "base", "", "ref to compare with, with --changed")
	cmd.Flags().StringVar(&format, "format", "auto", "pretty, json, github, gitlab or sarif")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the gitlab or sarif report to a file")
	return cmd
}

func renderRadarCheck(p *ui.Printer, r *api.DependencyCheck, manifests int) {
	t := p.Theme()
	flagged := append([]api.CheckedDependency(nil), r.Dependencies...)
	sort.SliceStable(flagged, func(i, j int) bool { return radar.Rank(flagged[i].Ring()) > radar.Rank(flagged[j].Ring()) })

	p.Println()
	for _, d := range flagged {
		ring := d.Ring()
		if radar.Rank(ring) < 3 {
			break
		}
		badge := ringColor(t, ring).Bold(true).Render(fmt.Sprintf("%-7s", strings.ToUpper(ring)))
		line := "  " + badge + " " + p.Bold(d.Name)
		if d.Version != "" {
			line += p.Muted(" " + d.Version)
		}
		line += "  " + p.Accent(d.FilePath)
		if d.IsDev {
			line += p.Muted(" (dev)")
		}
		p.Println(line)
		if d.Technology.RadarNote != "" {
			p.Println("          " + p.Success("→ ") + d.Technology.RadarNote)
		}
	}

	s := r.Summary
	parts := []string{
		ringColor(t, "adopt").Render(fmt.Sprintf("%d adopt", s.Adopt)),
		ringColor(t, "trial").Render(fmt.Sprintf("%d trial", s.Trial)),
		ringColor(t, "assess").Render(fmt.Sprintf("%d assess", s.Assess)),
		ringColor(t, "hold").Render(fmt.Sprintf("%d hold", s.Hold)),
		p.Muted(fmt.Sprintf("%d unclassified", s.Unclassified)),
	}
	verdict := p.Check() + " " + p.Bold("Nothing on hold")
	if s.Hold > 0 {
		verdict = p.Cross() + " " + p.Bold(ui.Plural(s.Hold, "dependency on hold", "dependencies on hold"))
	}
	if s.Hold > 0 || s.Assess > 0 {
		p.Println()
	}
	p.Println(verdict + p.Muted(fmt.Sprintf(" · %s in %s · ", ui.Plural(s.Total, "dependency", "dependencies"), ui.Plural(manifests, "manifest", "manifests"))) +
		strings.Join(parts, p.Muted(" · ")))
}
