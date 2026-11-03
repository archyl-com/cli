package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/history"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

// historyFetches bounds how many versions load at once.
const historyFetches = 6

// historyFlags are the options of archyl history.
type historyFlags struct {
	format string
	// formatSet is set when --format was given: the diagram alone is
	// written.
	formatSet bool
	output    string
	inside    bool
	markdown  bool
	play      bool
	interval  time.Duration
}

func newHistoryCmd(app *App) *cobra.Command {
	var flags historyFlags
	cmd := &cobra.Command{
		Use:   "history [from..to] [element]",
		Short: "How the architecture changed between two points",
		Long: `Show how the architecture changed between two points of its history: the
elements and relationships added, removed and modified (renamed, moved,
retyped, redescribed), as lists and as a diagram with the changes marked:
added in green with +, changed in amber with ~, removed dashed and dim
with −. An element removed with its children counts once.

A range is from..to, either side optional:

  v1.2.0..v2.0.0   between two releases
  v1.2.0..         from a release to now
  ..v2.0.0         from the oldest version kept to a release
  30d..            the last 30 days

Each point is one of:

  now              the model as it is
  12, rev12, r12   a version number
  v1.2.0           a release, as archyl release recorded it: the latest
                   version created at or before it went out
  2026-09-01       the end of that day
  30d, 2w, 12h     that long ago (also 3mo, 1y)

A single point means from it to now. Without a range, the history compares
the version before the latest with now.

Archyl records a version of the model when it is edited in the web app or
through the API, merging the edits one person makes within a few minutes,
and keeps the most recent ones.

The diagram shows the containers when they all sit in one system, else the
system context. Name an element to centre it on that element and list only
the changes in and around it; --inside draws what it contains instead.

In a terminal it prints a report. Piped, it prints Markdown for release
notes; --markdown adds a Mermaid diagram. --format and -o write the diagram
alone, as with archyl diagram. --play replays every version in the range
full screen, like the web app's time-lapse: space pauses, ← → step, + -
change the speed, q quits.`,
		Example: `  archyl history
  archyl history v1.2.0..v2.0.0
  archyl history 30d.. Billing
  archyl history v1.2.0.. --markdown >> RELEASE_NOTES.md
  archyl history 2026-09-01..now -o changes.png
  archyl history ..now --play
  archyl history 12..15 --json | jq '.summary'`,
		Args:              cobra.MaximumNArgs(2),
		ValidArgsFunction: completeHistory(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.formatSet = cmd.Flags().Changed("format")
			return runHistory(cmd.Context(), app, args, flags)
		},
	}
	cmd.Flags().StringVar(&flags.format, "format", formatAuto, "write the diagram alone: text, image, png, svg or mermaid")
	cmd.Flags().StringVarP(&flags.output, "output", "o", "", "write the diagram to a file; the extension picks the format")
	cmd.Flags().BoolVar(&flags.inside, "inside", false, "draw what the element contains instead of centring on it")
	cmd.Flags().BoolVar(&flags.markdown, "markdown", false, "print Markdown with a Mermaid diagram, for release notes")
	cmd.Flags().BoolVar(&flags.play, "play", false, "replay every version in the range full screen")
	cmd.Flags().DurationVar(&flags.interval, "interval", tui.DefaultFrameInterval, "how long --play shows each version")
	_ = cmd.RegisterFlagCompletionFunc("format", cobra.FixedCompletions(
		[]string{formatText, formatImage, formatPNG, formatSVG, formatMermaid}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

// completeHistory completes the element, after a range or in its place.
func completeHistory(app *App) cobra.CompletionFunc {
	elements := completeElements(app)
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 1 && (history.IsRange(args[0]) || history.LooksLikePoint(args[0])) {
			return elements(cmd, nil, toComplete)
		}
		return elements(cmd, args, toComplete)
	}
}

// historyArgs are the arguments as written: a range or a point, and an
// element.
type historyArgs struct {
	span    string
	element string
}

// splitHistoryArgs tells the range from the element. A lone argument that
// is neither a range nor reads as a point may be a release or an element:
// releases decides.
func splitHistoryArgs(args []string, releases []api.ReleaseDetail) historyArgs {
	switch len(args) {
	case 0:
		return historyArgs{}
	case 2:
		return historyArgs{span: args[0], element: args[1]}
	}
	arg := args[0]
	if history.IsRange(arg) || history.LooksLikePoint(arg) || history.IsRelease(arg, releases) {
		return historyArgs{span: arg}
	}
	return historyArgs{element: arg}
}

// historySources is what the command loads before resolving the range.
type historySources struct {
	project  *api.Project
	now      *api.Model
	versions []api.Version
	releases []api.ReleaseDetail
	// releasesErr is kept for a range that names a release.
	releasesErr error
}

func loadHistory(
	ctx context.Context,
	client *api.Client,
	projectID string,
) (*historySources, error) {
	var src historySources
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		src.project, err = client.Project(gctx, projectID)
		return err
	})
	g.Go(func() (err error) {
		src.now, err = client.ProjectModel(gctx, projectID)
		return err
	})
	g.Go(func() (err error) {
		src.versions, err = client.Versions(gctx, projectID)
		return err
	})
	g.Go(func() error {
		src.releases, src.releasesErr = client.Releases(gctx, projectID, api.ReleaseFilter{Limit: 100})
		return nil
	})
	err := g.Wait()
	return &src, err
}

// errNoVersions explains where versions come from.
var errNoVersions = errors.New("no versions of this project's model yet: Archyl records one when the model is " +
	"edited in the web app or through the API. Open the version history in the web app to record the first one: " +
	"archyl open, then press t")

// resolveRange finds both ends of the range among the versions.
func resolveRange(span string, src *historySources) (history.Resolved, history.Resolved, error) {
	var from, to history.Point
	var err error
	switch {
	case span == "":
		from, to = history.DefaultRange(src.versions)
	case history.IsRange(span):
		from, to, err = history.ParseRange(span)
	default:
		from, err = history.ParsePoint(span)
		to = history.Point{Raw: "now", Kind: history.PointNow}
	}
	if err != nil {
		return history.Resolved{}, history.Resolved{}, err
	}
	sources := history.Sources{Versions: src.versions, Releases: src.releases, Now: time.Now()}
	resolve := func(p history.Point) (history.Resolved, error) {
		r, err := history.Resolve(p, sources)
		switch {
		case errors.Is(err, history.ErrNoVersions):
			return r, errNoVersions
		case err != nil && p.Kind == history.PointRelease && src.releasesErr != nil:
			return r, fmt.Errorf("list the releases to find %s: %w", p.Raw, src.releasesErr)
		}
		return r, err
	}
	fromAt, err := resolve(from)
	if err != nil {
		return fromAt, history.Resolved{}, err
	}
	toAt, err := resolve(to)
	if err != nil {
		return fromAt, toAt, err
	}
	if fromAt.Number() == toAt.Number() {
		return fromAt, toAt, fmt.Errorf("nothing to compare: both ends are %s; widen the range, such as archyl history %s..now",
			fromAt.String(), strconv.Itoa(max(fromAt.Number()-1, 1)))
	}
	return fromAt, toAt, nil
}

func runHistory(
	ctx context.Context,
	app *App,
	args []string,
	flags historyFlags,
) error {
	p := app.Printer
	if err := checkHistoryFlags(p, flags); err != nil {
		return err
	}
	ref, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	var src *historySources
	if err := p.Spin("Loading the history…", func() (err error) {
		src, err = loadHistory(ctx, client, ref.ID)
		return err
	}); err != nil {
		return err
	}
	if len(src.versions) == 0 {
		return errNoVersions
	}
	parsed := splitHistoryArgs(args, src.releases)
	from, to, err := resolveRange(parsed.span, src)
	if err != nil {
		return err
	}
	if flags.play {
		return playHistory(ctx, app, client, ref.ID, src, from, to, parsed.element, flags)
	}

	before, after, err := historyModels(ctx, p, client, ref.ID, src.now, from, to)
	if err != nil {
		return err
	}
	report, err := buildReport(before, after, from, to, parsed.element, flags.inside)
	if err != nil {
		return err
	}
	return printHistory(app, report, flags)
}

// checkHistoryFlags refuses combinations that cannot work.
func checkHistoryFlags(p *ui.Printer, flags historyFlags) error {
	drawing := flags.formatSet || flags.output != ""
	switch {
	case flags.play && (p.JSON || flags.markdown || drawing):
		return errors.New("--play replays full screen; it does not go with --json, --markdown, --format or -o")
	case flags.play && !p.Interactive():
		return errors.New("--play needs a terminal; drop it to print the changes: archyl history")
	case flags.markdown && drawing:
		return errors.New("--markdown prints the whole report; drop it to write the diagram alone with --format or -o")
	case flags.interval < 100*time.Millisecond:
		return errors.New("--interval must be at least 100ms, such as --interval 1s")
	}
	return nil
}

// historyModels loads the model at both ends of the range.
func historyModels(
	ctx context.Context,
	p *ui.Printer,
	client *api.Client,
	projectID string,
	now *api.Model,
	from, to history.Resolved,
) (*api.Model, *api.Model, error) {
	models := make([]*api.Model, 2)
	err := p.Spin("Loading both versions…", func() error {
		g, gctx := errgroup.WithContext(ctx)
		for i, at := range []history.Resolved{from, to} {
			g.Go(func() (err error) {
				models[i], err = modelAt(gctx, client, projectID, now, at)
				return err
			})
		}
		return g.Wait()
	})
	return models[0], models[1], err
}

// modelAt is the model at a point: now's, or a version's snapshot.
func modelAt(
	ctx context.Context,
	client *api.Client,
	projectID string,
	now *api.Model,
	at history.Resolved,
) (*api.Model, error) {
	if at.IsNow() {
		return now, nil
	}
	v, err := client.Version(ctx, projectID, at.Number())
	if err != nil {
		return nil, fmt.Errorf("load REV %d: %w", at.Number(), err)
	}
	return v.Model()
}

// buildReport compares the two models and draws the merged view.
func buildReport(
	before *api.Model,
	after *api.Model,
	from history.Resolved,
	to history.Resolved,
	element string,
	inside bool,
) (*history.Report, error) {
	// One end at least is a snapshot, and snapshots record no technologies:
	// comparing them would report every technology of now as new.
	d := history.Compare(before, after, history.Options{})
	scene := d.Scene()
	scope := history.DefaultScope(scene.Graph)
	report := &history.Report{From: from, To: to, Diff: d}
	if element != "" {
		n, err := findHistoryElement(scene.Graph, element)
		if err != nil {
			return nil, err
		}
		if inside && len(n.Children) == 0 {
			return nil, fmt.Errorf("%s holds no elements to draw inside it; drop --inside to centre the diagram on it", n.Name)
		}
		scope = history.Scope{ElementID: n.ID, Inside: inside}
		report.Scope = n.Name
		report.Diff = d.Within(scene.Graph, n.ID)
	} else if inside {
		return nil, errors.New("name an element: archyl history <range> <element> --inside")
	}
	report.Summary = report.Diff.Summary()
	report.View = scene.View(scope)
	report.View.Title += " · " + from.Label + " → " + to.Label
	return report, nil
}

// findHistoryElement finds an element on either side of the range.
func findHistoryElement(g *graph.Graph, ref string) (*graph.Node, error) {
	n, _ := g.Find(ref)
	if n == nil {
		return nil, fmt.Errorf("no element matches %q at either end of the range; find one with archyl search %s", ref, ref)
	}
	return n, nil
}

// printHistory writes the report in the form asked for.
func printHistory(
	app *App,
	report *history.Report,
	flags historyFlags,
) error {
	p := app.Printer
	switch {
	case p.JSON:
		return p.WriteJSON(report)
	case flags.formatSet || flags.output != "":
		drawing := diagram.Trim(report.View, history.MaxBoxes)
		if strings.EqualFold(flags.format, formatText) && flags.output == "" {
			p.Println(p.Brand() + p.Muted("  history  ") + p.Bold(ui.StripControl(drawing.Title)))
			printHistoryDiagram(p, drawing)
			return nil
		}
		return printDiagram(app, drawing, nil, flags.format, flags.output)
	case flags.markdown || !p.Interactive():
		_, err := fmt.Fprint(p.Out, ui.Sanitize(history.Markdown(report, history.MarkdownOptions{Diagram: flags.markdown})))
		return err
	}
	printHistoryReport(p, report)
	return nil
}

// printHistoryReport is the terminal report: header, counts, diagram,
// legend and the changes by kind.
func printHistoryReport(p *ui.Printer, r *history.Report) {
	title := r.From.String() + " → " + r.To.String()
	if r.Scope != "" {
		title = r.Scope + "  " + title
	}
	p.Println(p.Brand() + p.Muted("  history  ") + p.Bold(ui.StripControl(title)))
	p.Println("  " + p.Muted(history.Tally(r.Summary)))
	if r.Diff.Empty() {
		p.Println()
		p.Println("  " + p.Muted("No architecture changes between "+ui.StripControl(r.From.Label)+" and "+ui.StripControl(r.To.Label)+"."))
		p.Println("  " + p.Hint("Look further back:", "archyl history 30d.."))
		return
	}
	// The drawing opens with a blank margin of its own.
	if drawing := diagram.Trim(r.View, history.MaxBoxes); len(drawing.Nodes) > 0 {
		printHistoryDiagram(p, drawing)
	}
	p.Println()
	printElementChanges(p, r.Diff.Elements)
	printRelationshipChanges(p, r.Diff.Relationships)
}

func printHistoryDiagram(p *ui.Printer, v *diagram.View) {
	p.Println(diagram.Text(v, diagram.TextOptions{Theme: p.Theme(), Color: true, Width: p.Width()}))
	p.Println()
	p.Println("  " + changeSign(p, diagram.Added) + p.Muted(" added   ") +
		changeSign(p, diagram.Changed) + p.Muted(" changed   ") +
		changeSign(p, diagram.Removed) + p.Muted(" removed"))
}

// changeSign is the sign of a change in its colour.
func changeSign(p *ui.Printer, c diagram.Change) string {
	switch c {
	case diagram.Added:
		return p.Success(c.Badge())
	case diagram.Changed:
		return p.Warning(c.Badge())
	}
	return p.Muted(c.Badge())
}

var statusSigns = map[history.Status]diagram.Change{
	history.Added:    diagram.Added,
	history.Removed:  diagram.Removed,
	history.Modified: diagram.Changed,
}

func printElementChanges(p *ui.Printer, elements []history.Element) {
	sections := []struct {
		title  string
		status history.Status
	}{{"Added", history.Added}, {"Removed", history.Removed}, {"Modified", history.Modified}}
	for _, s := range sections {
		first := true
		for _, e := range elements {
			if e.Status != s.status {
				continue
			}
			if first {
				p.Println(p.Heading(s.title))
				first = false
			}
			detail := history.Where(e)
			if f := history.Folded(e); f != "" {
				detail += " · " + f
			}
			p.Println("    " + changeSign(p, statusSigns[e.Status]) + " " +
				p.Element(e.Level, ui.StripControl(e.Name)) + "  " + p.Muted(ui.StripControl(detail)))
			if len(e.Changes) > 0 {
				p.Println("      " + p.Muted(ui.StripControl(history.Describe(e.Changes))))
			}
		}
		if !first {
			p.Println()
		}
	}
}

func printRelationshipChanges(p *ui.Printer, rels []history.Relationship) {
	var lines []string
	implied := 0
	for _, r := range rels {
		if r.WithEnd {
			implied++
			continue
		}
		line := "    " + changeSign(p, statusSigns[r.Status]) + " " + ui.StripControl(r.Source) + " → " + ui.StripControl(r.Target)
		if r.Label != "" {
			line += "  " + p.Muted(ui.StripControl(r.Label))
		}
		lines = append(lines, line)
		if len(r.Changes) > 0 {
			lines = append(lines, "      "+p.Muted(ui.StripControl(history.Describe(r.Changes))))
		}
	}
	if implied > 0 {
		lines = append(lines, "    "+p.Muted(history.Implied(implied)))
	}
	if len(lines) == 0 {
		return
	}
	p.Println(p.Heading("Relationships"))
	for _, l := range lines {
		p.Println(l)
	}
}

// playHistory replays the versions of the range full screen.
func playHistory(
	ctx context.Context,
	app *App,
	client *api.Client,
	projectID string,
	src *historySources,
	from, to history.Resolved,
	element string,
	flags historyFlags,
) error {
	opts, err := replayOptions(ctx, app.Printer, client, projectID, src, from, to, element, flags.inside)
	if err != nil {
		return err
	}
	opts.Interval = flags.interval
	_, err = tea.NewProgram(tui.NewHistory(opts), tea.WithContext(ctx)).Run()
	return err
}

// replayOptions loads the versions of the range and turns them into the
// replay's frames, all drawn at the scope of the range's last state.
func replayOptions(
	ctx context.Context,
	p *ui.Printer,
	client *api.Client,
	projectID string,
	src *historySources,
	from, to history.Resolved,
	element string,
	inside bool,
) (tui.HistoryOptions, error) {
	if from.IsNow() || (!to.IsNow() && to.Number() < from.Number()) {
		return tui.HistoryOptions{}, errors.New("--play runs forward in time: put the earlier point first, such as archyl history 30d..now --play")
	}
	picked, total := history.Pick(src.versions, from, to)
	states, err := loadStates(ctx, p, client, projectID, picked)
	if err != nil {
		return tui.HistoryOptions{}, err
	}
	if to.IsNow() {
		states = append(states, history.State{Model: src.now})
	}
	scene := history.Compare(states[0].Model, states[len(states)-1].Model, history.Options{}).Scene()
	scope := history.DefaultScope(scene.Graph)
	if element != "" {
		n, err := findHistoryElement(scene.Graph, element)
		if err != nil {
			return tui.HistoryOptions{}, err
		}
		scope = history.Scope{ElementID: n.ID, Inside: inside}
	}
	opts := tui.HistoryOptions{
		Project: ui.StripControl(src.project.Name),
		Frames:  history.Replay(states, scope),
		Theme:   p.Theme(),
	}
	if total > len(picked) {
		opts.Note = fmt.Sprintf("%d versions in the range: playing the first and the latest %d", total, len(picked)-1)
	}
	return opts, nil
}

// loadStates fetches the snapshots of the versions, a few at a time.
func loadStates(
	ctx context.Context,
	p *ui.Printer,
	client *api.Client,
	projectID string,
	versions []api.Version,
) ([]history.State, error) {
	states := make([]history.State, len(versions))
	label := "Loading " + strconv.Itoa(len(versions)) + " " + pluralWord(len(versions), "version", "versions") + "…"
	err := p.Spin(label, func() error {
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(historyFetches)
		for i, v := range versions {
			g.Go(func() error {
				full, err := client.Version(gctx, projectID, v.Number)
				if err != nil {
					return fmt.Errorf("load REV %d: %w", v.Number, err)
				}
				m, err := full.Model()
				if err != nil {
					return err
				}
				states[i] = history.State{Version: full, Model: m}
				return nil
			})
		}
		return g.Wait()
	})
	return states, err
}
