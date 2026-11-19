package commands

import (
	"bytes"
	"context"
	"errors"
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

// dslLabel names what a push sends: the file, and how many files go with it
// (those it includes, and those its docs and API contracts name).
func dslLabel(path string, src api.DSLSource) string {
	label := filepath.Base(path)
	switch n := len(src.Files); n {
	case 0:
	case 1:
		label += " and 1 other file"
	default:
		label += fmt.Sprintf(" and %d other files", n)
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
	var exitCode, prune bool
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Show what pushing archyl.yaml would change",
		Long: `Compare archyl.yaml with the project's model, without writing anything:
elements and relationships push would add (+) or change (~), grouped by C4
level, and those only the model has (−), which push leaves in place.

With --prune it marks, among those, what archyl dsl push --prune would delete.
With --exit-code it exits 1 when there is any difference, for CI.`,
		Example: `  archyl dsl diff
  archyl dsl diff --prune
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
			src.Prune = prune
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
	cmd.Flags().BoolVar(&prune, "prune", false, "mark what dsl push --prune would delete")
	return cmd
}

// errPruneUnsupported is returned when the server does not know how to prune.
var errPruneUnsupported = errors.New("this Archyl server cannot prune yet; update it, or push without --prune")

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
	if err == nil && src.Prune && diff.Prune == nil {
		return nil, errPruneUnsupported
	}
	return diff, err
}

func newDSLPushCmd(app *App) *cobra.Command {
	var file string
	var opts pushOptions
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Import archyl.yaml into the project",
		Long: `Import archyl.yaml into the project: new elements and relationships are
created, existing ones (matched by name) updated. Nothing is deleted.

With --prune, what the project has and the file no longer declares is deleted
too, kind by kind: systems, containers, components, code, relationships,
decisions, docs and API contracts. A kind the file does not declare at all is
left alone, so a file with no docs section never deletes a doc. Deleting an
element deletes its children and its relationships. The server refuses a prune
that would delete a large share of a kind, which is what a truncated file looks
like; --force lets it through. archyl dsl diff --prune shows it without writing.

In a terminal it shows the diff and asks first; --yes skips the question.`,
		Example: `  archyl dsl push
  archyl dsl push --yes --file docs/architecture/archyl.yaml
  archyl dsl push --prune --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return pushDSL(cmd.Context(), app, dslPath(cmd.Context(), app, file), opts)
		},
	}
	addDSLFileFlag(cmd, &file)
	addPushFlags(cmd, &opts)
	return cmd
}

// pushOptions are the flags of a push.
type pushOptions struct {
	yes   bool
	prune bool
	force bool
}

func addPushFlags(cmd *cobra.Command, opts *pushOptions) {
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "push without showing the diff and asking")
	cmd.Flags().BoolVar(&opts.prune, "prune", false, "delete what the project has and the file no longer declares")
	cmd.Flags().BoolVar(&opts.force, "force", false, "with --prune, delete even a large share of the project")
}

// pushDSL imports the file. In a terminal it previews the change and asks
// first, unless yes is set; scripts and --json push straight away.
func pushDSL(ctx context.Context, app *App, path string, opts pushOptions) error {
	p := app.Printer
	if opts.force && !opts.prune {
		return errors.New("--force only applies to --prune")
	}
	src, err := readDSL(ctx, app, path)
	if err != nil {
		return err
	}
	src.Prune, src.Force = opts.prune, opts.force
	label := dslLabel(path, src)
	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}

	if !opts.yes && p.Interactive() {
		question := "Push " + label + " to the project?"
		diff, err := diffDSL(ctx, app, src)
		switch {
		case api.IsStatus(err, http.StatusNotFound) && !opts.prune:
			p.Eprintln(p.Bang() + " " + p.Muted("This server cannot preview the push; it will import the file as is."))
		case err != nil:
			return err
		case diff.InSync:
			renderDSLDiff(p, label, diff)
			return nil
		default:
			renderDSLDiff(p, label, diff)
			p.Println()
			if diff.Prune != nil {
				if len(diff.Prune.Refused) > 0 && !opts.force {
					return pruneRefused(diff.Prune.Refused)
				}
				if deleted := len(diff.Prune.Entries); deleted > 0 {
					question = "Push " + label + " and delete " + ui.Plural(deleted, "object", "objects") + "?"
				}
			}
		}
		push := false
		if err := huh.NewConfirm().
			Title(question).
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
		if api.IsStatus(err, http.StatusConflict) && opts.prune && !opts.force {
			return fmt.Errorf("%w\nnothing was pushed; rerun with --force if the file is right", err)
		}
		return err
	}
	app.InvalidateModel(project.ID)
	// A server that does not know the option imported the file and deleted
	// nothing: say so rather than let the push pass for a prune.
	unpruned := opts.prune && !result.PruneRan

	if p.JSON {
		if err := p.WriteJSON(result); err != nil {
			return err
		}
		if unpruned {
			return errPruneUnsupported
		}
		return nil
	}
	created := dslCreated(p, result)
	summary := p.Muted(" — nothing new to create")
	if len(created) > 0 {
		summary = p.Muted(" · created ") + strings.Join(created, p.Muted(", "))
	}
	if deleted := len(result.Pruned); deleted > 0 {
		summary += p.Muted(" · deleted ") + p.Bold(ui.Plural(deleted, "object", "objects"))
	}
	p.Println(p.Check() + " Pushed " + p.Bold(label) + summary)
	for _, entry := range result.Pruned {
		p.Println("  " + p.Danger("− ") + p.Muted(dslKind(entry.ElementType)+" ") + entry.ElementName)
	}
	for _, w := range result.Warnings {
		p.Println("  " + p.Bang() + " " + p.Muted(w))
	}
	if unpruned {
		return fmt.Errorf("the file was pushed, nothing was deleted: %w", errPruneUnsupported)
	}
	return nil
}

// pruneRefused is the error of a prune the server would refuse.
func pruneRefused(reasons []string) error {
	return fmt.Errorf(
		"the prune would delete %s, which the server refuses: check the file, or rerun with --force",
		strings.Join(reasons, ", "),
	)
}

// dslKind names a kind of object the way a person reads it.
func dslKind(kind string) string {
	switch kind {
	case "adr":
		return "decision"
	case "api_contract":
		return "API contract"
	}
	return kind
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
	{"adr", "Decisions"},
	{"doc", "Docs"},
	{"api_contract", "API contracts"},
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

	pruned := map[string]bool{}
	if diff.Prune != nil {
		for _, entry := range diff.Prune.Entries {
			pruned[entry.ElementType+"\x00"+entry.ElementName] = true
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
				note := p.Muted("  only in Archyl, push keeps it")
				if pruned[c.ElementType+"\x00"+name] {
					note = p.Danger("  only in Archyl, push --prune deletes it")
				}
				lines = append(lines, "  "+p.Danger("− ")+dslName(p, s.level, name)+note)
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
	if len(pruned) > 0 {
		parts = append(parts, p.Danger(fmt.Sprintf("%d to delete with --prune", len(pruned))))
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
