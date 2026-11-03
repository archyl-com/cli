package commands

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/dslfile"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

const dslFile = "archyl.yaml"

func newDSLCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dsl",
		Short: "Keep archyl.yaml and the model in step",
		Long: `Architecture as code: pull the project's model into archyl.yaml, see what
pushing the file would change, and push it.

Push adds and updates; it never deletes. What the model has and the file does
not shows up in the diff as "only in Archyl", and stays.`,
		Example: `  archyl dsl pull
  archyl dsl diff
  archyl dsl diff --exit-code    # in CI: fail when the file and the model differ
  archyl dsl push --yes`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newDSLPullCmd(app), newDSLDiffCmd(app), newDSLPushCmd(app))
	return cmd
}

// dslPath resolves the file: the flag, else archyl.yaml at the repository
// root, else in the working directory.
func dslPath(ctx context.Context, app *App, flag string) string {
	if flag != "" {
		return flag
	}
	if git, err := app.Repo(ctx); err == nil {
		return filepath.Join(git.Root, dslFile)
	}
	return dslFile
}

// readDSL reads the file and every file its include directives reach,
// resolved inside the repository.
func readDSL(ctx context.Context, app *App, file string) (api.DSLSource, error) {
	base := filepath.Dir(file)
	if git, err := app.Repo(ctx); err == nil {
		base = git.Root
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return api.DSLSource{}, err
	}
	src, err := dslfile.Load(base, abs)
	if err != nil {
		return api.DSLSource{}, err
	}
	if len(src.Files) > 0 {
		if err := requireIncludes(ctx, app); err != nil {
			return api.DSLSource{}, err
		}
	}
	return api.DSLSource{Content: src.Content, Path: src.Path, Files: src.Files}, nil
}

// requireIncludes refuses to send included files to a server that would
// drop them and import the root alone.
func requireIncludes(ctx context.Context, app *App) error {
	client, err := app.Client()
	if err != nil {
		return err
	}
	ok, err := client.DSLIncludesSupported(ctx)
	if err != nil {
		return fmt.Errorf("check that the server merges included files: %w", err)
	}
	if !ok {
		return fmt.Errorf("this Archyl server ignores the files archyl.yaml includes and would import it alone: " +
			"upgrade the server, or put the whole model in one archyl.yaml")
	}
	return nil
}

// dslLabel names what a push sends: the file, and how many it includes.
func dslLabel(path string, src api.DSLSource) string {
	label := filepath.Base(path)
	switch n := len(src.Files); n {
	case 0:
	case 1:
		label += " and 1 included file"
	default:
		label += fmt.Sprintf(" and %d included files", n)
	}
	return label
}

func addDSLFileFlag(cmd *cobra.Command, file *string) {
	cmd.Flags().StringVarP(file, "file", "f", "", "the DSL file (default: archyl.yaml at the repository root)")
	_ = cmd.MarkFlagFilename("file", "yaml", "yml")
}

func newDSLPullCmd(app *App) *cobra.Command {
	var file string
	var yes bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Write the project's model to archyl.yaml",
		Long: `Export the project's model (elements, relationships, technologies, decisions,
docs, contracts, environments and releases) to archyl.yaml. Use --file - to
print it instead.`,
		Example: `  archyl dsl pull
  archyl dsl pull --file docs/architecture/archyl.yaml
  archyl dsl pull --file - | less`,
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

			var content string
			if err := p.Spin("Exporting the model…", func() error {
				content, err = client.ExportDSL(ctx, project.ID)
				return err
			}); err != nil {
				return err
			}

			if file == "-" {
				_, err := fmt.Fprint(p.Out, content)
				return err
			}
			path := dslPath(ctx, app, file)
			existing, readErr := os.ReadFile(path)
			unchanged := readErr == nil && bytes.Equal(existing, []byte(content))
			if readErr == nil && !unchanged && !yes && p.Interactive() {
				overwrite := false
				if err := huh.NewConfirm().
					Title("Overwrite " + filepath.Base(path) + "?").
					Description("It differs from the model. Run archyl dsl diff first to see how.").
					Value(&overwrite).Run(); err != nil {
					return err
				}
				if !overwrite {
					return nil
				}
			}
			if !unchanged {
				if err := repo.RefuseLink(path); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
			}

			if p.JSON {
				return p.WriteJSON(map[string]any{"path": path, "bytes": len(content), "changed": !unchanged})
			}
			if unchanged {
				p.Println(p.Check() + " " + p.Bold(filepath.Base(path)) + p.Muted(" already matches the model"))
				return nil
			}
			p.Println(p.Check() + " Wrote " + p.Bold(relPath(path)) + p.Muted(fmt.Sprintf(" · %d lines", strings.Count(content, "\n"))))
			return nil
		},
	}
	addDSLFileFlag(cmd, &file)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "overwrite the file without asking")
	return cmd
}

func newDSLDiffCmd(app *App) *cobra.Command {
	var file string
	var exitCode bool
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what pushing archyl.yaml would change",
		Long: `Compare archyl.yaml with the project's model, without writing anything:
elements and relationships push would add (+) or change (~), grouped by C4
level, and those only the model has (−), which push leaves in place.

With --exit-code it exits 1 when there is any difference, for CI.`,
		Example: `  archyl dsl diff
  archyl dsl diff --exit-code
  archyl dsl diff --json | jq '.changes[] | select(.action == "added")'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			path := dslPath(ctx, app, file)
			src, err := readDSL(ctx, app, path)
			if err != nil {
				return err
			}
			diff, err := diffDSL(ctx, app, src)
			if err != nil {
				return err
			}

			if p.JSON {
				if err := p.WriteJSON(diff); err != nil {
					return err
				}
			} else {
				renderDSLDiff(p, dslLabel(path, src), diff)
			}
			if exitCode && !diff.InSync {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	addDSLFileFlag(cmd, &file)
	cmd.Flags().BoolVar(&exitCode, "exit-code", false, "exit 1 when the file and the model differ")
	return cmd
}

func diffDSL(ctx context.Context, app *App, src api.DSLSource) (*api.DSLDiff, error) {
	project, err := app.Project(ctx)
	if err != nil {
		return nil, err
	}
	client, err := app.Client()
	if err != nil {
		return nil, err
	}
	var diff *api.DSLDiff
	err = app.Printer.Spin("Comparing with the model…", func() error {
		diff, err = client.DiffDSL(ctx, project.ID, src)
		return err
	})
	if api.IsStatus(err, http.StatusNotFound) {
		return nil, fmt.Errorf("this Archyl server cannot preview a push yet: %w", err)
	}
	return diff, err
}

func newDSLPushCmd(app *App) *cobra.Command {
	var file string
	var yes bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Import archyl.yaml into the project",
		Long: `Import archyl.yaml into the project: new elements and relationships are
created, existing ones (matched by name) updated. Nothing is deleted.

In a terminal it shows the diff and asks first; --yes skips the question.`,
		Example: `  archyl dsl push
  archyl dsl push --yes --file docs/architecture/archyl.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return pushDSL(cmd.Context(), app, dslPath(cmd.Context(), app, file), yes)
		},
	}
	addDSLFileFlag(cmd, &file)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "push without showing the diff and asking")
	return cmd
}

// pushDSL imports the file. In a terminal it previews the change and asks
// first, unless yes is set; scripts and --json push straight away.
func pushDSL(ctx context.Context, app *App, path string, yes bool) error {
	p := app.Printer
	src, err := readDSL(ctx, app, path)
	if err != nil {
		return err
	}
	label := dslLabel(path, src)
	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}

	if !yes && p.Interactive() {
		diff, err := diffDSL(ctx, app, src)
		switch {
		case api.IsStatus(err, http.StatusNotFound):
			p.Eprintln(p.Bang() + " " + p.Muted("This server cannot preview the push; it will import the file as is."))
		case err != nil:
			return err
		case diff.InSync:
			renderDSLDiff(p, label, diff)
			return nil
		default:
			renderDSLDiff(p, label, diff)
			p.Println()
		}
		push := false
		if err := huh.NewConfirm().
			Title("Push " + label + " to the project?").
			Value(&push).Run(); err != nil {
			return err
		}
		if !push {
			return nil
		}
	}

	var result *api.DSLImport
	if err := p.Spin("Pushing "+label+"…", func() error {
		result, err = client.SyncDSL(ctx, project.ID, src)
		return err
	}); err != nil {
		return err
	}
	app.InvalidateModel(project.ID)

	if p.JSON {
		return p.WriteJSON(result)
	}
	created := dslCreated(p, result)
	if len(created) == 0 {
		p.Println(p.Check() + " Pushed " + p.Bold(label) + p.Muted(" — nothing new to create"))
	} else {
		p.Println(p.Check() + " Pushed " + p.Bold(label) + p.Muted(" · created ") + strings.Join(created, p.Muted(", ")))
	}
	for _, w := range result.Warnings {
		p.Println("  " + p.Bang() + " " + p.Muted(w))
	}
	return nil
}

// dslSection is one group of the diff, in C4 order.
type dslSection struct {
	level, title string
}

var dslSections = []dslSection{
	{"system", "Systems"},
	{"container", "Containers"},
	{"component", "Components"},
	{"code", "Code"},
	{"relationship", "Relationships"},
}

// renderDSLDiff prints the diff grouped by C4 level: one line per element,
// with the fields push would change under it.
func renderDSLDiff(p *ui.Printer, file string, diff *api.DSLDiff) {
	if diff.InSync {
		p.Println(p.Check() + " " + p.Bold(file) + p.Muted(" matches the model — nothing to push"))
		printDSLWarnings(p, diff.Warnings)
		return
	}

	var added, changed, only int
	seenChanged := map[string]bool{}
	for _, c := range diff.Changes {
		switch c.Action {
		case "added":
			added++
		case "removed":
			only++
		case "modified":
			if key := c.ElementType + "\x00" + c.ElementName; !seenChanged[key] {
				seenChanged[key] = true
				changed++
			}
		}
	}

	known := map[string]bool{}
	sections := append([]dslSection(nil), dslSections...)
	for _, s := range sections {
		known[s.level] = true
	}
	for _, c := range diff.Changes {
		if !known[c.ElementType] {
			known[c.ElementType] = true
			sections = append(sections, dslSection{c.ElementType, capitalize(c.ElementType)})
		}
	}

	for _, s := range sections {
		var lines []string
		printed := map[string]int{}
		for _, c := range diff.Changes {
			if c.ElementType != s.level {
				continue
			}
			name := c.ElementName
			switch c.Action {
			case "added":
				line := "  " + p.Success("+ ") + dslName(p, s.level, name)
				if c.DSLValue != "" {
					line += p.Muted("  " + c.DSLValue)
				}
				lines = append(lines, line)
			case "removed":
				line := "  " + p.Danger("− ") + dslName(p, s.level, name) + p.Muted("  only in Archyl, push keeps it")
				lines = append(lines, line)
			case "modified":
				if _, ok := printed[name]; !ok {
					printed[name] = len(lines)
					lines = append(lines, "  "+p.Warning("~ ")+dslName(p, s.level, name))
				}
				lines = append(lines, "      "+p.Muted(c.Field+"  ")+dslChange(p, c.ModelValue, c.DSLValue))
			}
		}
		if len(lines) == 0 {
			continue
		}
		p.Println(p.Heading(s.title))
		for _, l := range lines {
			p.Println(l)
		}
		p.Println()
	}

	var parts []string
	if added > 0 {
		parts = append(parts, p.Success(fmt.Sprintf("%d to add", added)))
	}
	if changed > 0 {
		parts = append(parts, p.Warning(fmt.Sprintf("%d to change", changed)))
	}
	if only > 0 {
		parts = append(parts, p.Muted(fmt.Sprintf("%d only in Archyl", only)))
	}
	p.Println("  " + p.Bold(file) + p.Muted(" vs the model: ") + strings.Join(parts, p.Muted(" · ")))
	printDSLWarnings(p, diff.Warnings)
}

func printDSLWarnings(p *ui.Printer, warnings []string) {
	for _, w := range warnings {
		p.Println("  " + p.Bang() + " " + p.Muted(w))
	}
}

func dslName(p *ui.Printer, level, name string) string {
	if level == "relationship" {
		return p.Bold(name)
	}
	return p.Element(level, name)
}

// dslChange renders "model → file" for one field, quoting long text.
func dslChange(p *ui.Printer, from, to string) string {
	show := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if s == "" {
			return p.Muted("(empty)")
		}
		if r := []rune(s); len(r) > 60 {
			s = string(r[:59]) + "…"
		}
		return s
	}
	return p.Danger(show(from)) + p.Muted(" → ") + p.Success(show(to))
}

// relPath shows path relative to the working directory when it is under it.
func relPath(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
