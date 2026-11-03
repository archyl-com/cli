package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

const maxPaths = 500

func newWhereCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "where [path...]",
		Short: "Show where files live in the architecture",
		Long: `Place files in the C4 model: the element each one belongs to, who owns it and
what depends on it. Directories expand to the files git knows about.`,
		Example: `  archyl where internal/billing/invoice.go
  archyl where src/checkout/
  archyl where --json . | jq '.files[].elements[0].name'`,
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveDefault
		},
		RunE: func(cmd *cobra.Command, args []string) error {
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

			if len(args) == 0 {
				args = []string{"."}
			}
			cwd, _ := os.Getwd()
			var paths []string
			for _, arg := range args {
				rel, err := git.Rel(cwd, arg)
				if err != nil {
					return err
				}
				if info, err := os.Stat(arg); err == nil && info.IsDir() {
					files, err := git.Files(ctx, rel)
					if err != nil {
						return err
					}
					paths = append(paths, files...)
					continue
				}
				paths = append(paths, rel)
			}
			if len(paths) == 0 {
				return fmt.Errorf("no file to place")
			}
			truncated := len(paths) > maxPaths
			if truncated {
				paths = paths[:maxPaths]
			}

			var result *api.LocateResult
			if err := p.Spin("Placing files in the architecture…", func() error {
				result, err = client.Locate(ctx, project.ID, paths, 1)
				return err
			}); err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(result)
			}
			if len(paths) <= 5 {
				renderWhereFiles(p, result)
			} else {
				renderWhereGrouped(p, result, len(paths))
			}
			if truncated {
				p.Println(p.Warning(fmt.Sprintf("Only the first %d files were placed.", maxPaths)))
			}
			return nil
		},
	}
}

func renderWhereFiles(p *ui.Printer, r *api.LocateResult) {
	touched := touchedByID(r)
	for _, f := range r.Files {
		p.Println()
		p.Println(p.Bold(f.Path))
		primary := f.Elements[0]
		element, known := touched[primary.ID]
		if known {
			p.Println("  " + breadcrumb(p, element.Path))
		} else {
			p.Println("  " + p.Element(primary.Type, primary.Name))
		}
		rows := [][2]string{{"match", matchLabel(p, primary.Match)}}
		if known {
			rows = append(rows, [2]string{"owners", ownersLine(p, element.Owners, element.TeamOwners)})
			if len(element.Dependents) > 0 {
				rows = append(rows, [2]string{"used by", elementList(p, element.Dependents, 4)})
			}
		}
		if len(f.Elements) > 1 {
			var also []string
			for _, e := range f.Elements[1:] {
				also = append(also, p.Element(e.Type, e.Name))
			}
			rows = append(rows, [2]string{"also", strings.Join(also, p.Muted(", "))})
		}
		p.Println(indent(p.KeyValues(rows), 4))
	}
	for _, path := range r.Unmatched {
		p.Println()
		p.Println(p.Bold(path))
		p.Println("  " + p.Muted("not in the model yet — no element declares this path or reads like its name"))
	}
	p.Println()
}

func renderWhereGrouped(p *ui.Printer, r *api.LocateResult, total int) {
	touched := touchedByID(r)
	counts, order := filesPerElement(r)

	p.Println()
	p.Println(p.Heading(fmt.Sprintf(" %s across %s", ui.Plural(total, "file", "files"), ui.Plural(len(order), "element", "elements"))))
	p.Println()
	for _, id := range order {
		element := touched[id]
		label := p.Element(element.Type, element.Name)
		if element.Path != "" && element.Path != element.Name {
			label = breadcrumb(p, element.Path)
		}
		p.Println(fmt.Sprintf("  %s  %s", label, p.Muted(ui.Plural(counts[id], "file", "files"))))
		p.Println("    " + p.Muted("owners ") + ownersLine(p, element.Owners, element.TeamOwners))
	}
	if n := len(r.Unmatched); n > 0 {
		p.Println()
		p.Println("  " + p.Muted(fmt.Sprintf("%s outside the model", ui.Plural(n, "file", "files"))))
	}
	p.Println()
}

func matchLabel(p *ui.Printer, match string) string {
	if match == "path" {
		return p.Success("declared path")
	}
	return p.Warning("name match") + p.Muted(" — the path reads like the element's name")
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}
