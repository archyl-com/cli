package commands

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/incident"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/weblink"
)

const (
	// incidentTimeout bounds the whole gathering: at 3 a.m. a section that
	// does not answer is worth less than the screen.
	incidentTimeout = 8 * time.Second
	// incidentConcurrency caps the API calls in flight.
	incidentConcurrency = 8
	// incidentHistory is how many changes are read per element.
	incidentHistory = 5
	// incidentMemories is how many memories are read before ranking.
	incidentMemories = 8
	// incidentDiagramBoxes keeps the diagram small enough to read at a glance.
	incidentDiagramBoxes = 12
	// incidentNarrowBox is the narrowest box diagram.Text draws before it
	// falls back to a list.
	incidentNarrowBox = 20
)

func newIncidentCmd(app *App) *cobra.Command {
	var depth int
	var slack, copyMessage, noDiagram bool
	cmd := &cobra.Command{
		Use:   "incident <element>",
		Short: "Everything to act on an element during an incident",
		Long: `One fast, read-only screen for an element in trouble, of any level: who owns
it, who depends on it (grouped by distance, with their owners) and what it
depends on, the latest changes and releases, the documentation, decisions
and pitfalls linked to it or its ancestors, and everyone to tell.

No AI is involved, so it is quick and the same every time. The API calls run
together under a short timeout; a section whose call fails says so, and the
rest still shows.

--slack prints a ready-to-paste Slack message instead, with plain names and
emails to ping; --copy puts it on the clipboard. Piped, the report is
Markdown; --json gives it to scripts.`,
		Example: `  archyl incident "Orders API"
  archyl incident Payments --depth 3
  archyl incident "Orders API" --slack --copy
  archyl incident "Orders API" --json | jq -r '.notify.teams[].name'`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeElements(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			if depth < 1 || depth > incident.MaxDepth {
				return fmt.Errorf("--depth must be between 1 and %d", incident.MaxDepth)
			}
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			snap, err := loadModel(ctx, app, ref.ID)
			if err != nil {
				return err
			}
			g := graph.New(&snap.Model)
			n, _ := g.Find(args[0])
			if n == nil {
				return fmt.Errorf("no element matches %q; find it with archyl search %q", args[0], args[0])
			}
			client, err := app.Client()
			if err != nil {
				return err
			}

			plan := incident.NewPlan(g, n, depth)
			var facts incident.Facts
			if err := p.Spin("Gathering what you need on "+n.Name+"…", func() error {
				facts = gatherIncident(ctx, client, ref.ID, plan)
				return nil
			}); err != nil {
				return err
			}
			report := incident.Build(incident.Input{
				Plan:  plan,
				ADRs:  snap.ADRs,
				Facts: facts,
				Links: incidentLinks(app.appURL(), ref.ID),
				Now:   time.Now(),
			})

			switch {
			case slack || copyMessage:
				return deliverSlack(p, incident.Slack(report), slack, copyMessage, clipboard.WriteAll)
			case p.JSON:
				return p.WriteJSON(report)
			case !p.Interactive():
				_, err := io.WriteString(p.Out, ui.Sanitize(incident.Markdown(report)))
				return err
			}
			var picture string
			if !noDiagram {
				picture = incidentDiagram(p, g, n)
			}
			incident.Render(p, report, picture)
			return nil
		},
	}
	cmd.Flags().IntVar(&depth, "depth", incident.DefaultDepth, fmt.Sprintf("how many hops of dependents to follow (1-%d)", incident.MaxDepth))
	cmd.Flags().BoolVar(&slack, "slack", false, "print only a Slack message to paste in the incident channel")
	cmd.Flags().BoolVar(&copyMessage, "copy", false, "copy the Slack message to the clipboard")
	cmd.Flags().BoolVar(&noDiagram, "no-diagram", false, "leave out the diagram around the element")
	return cmd
}

// deliverSlack prints the Slack message, copies it, or both. Copying alone
// says so on stderr, so stdout stays empty.
func deliverSlack(
	p *ui.Printer,
	message string,
	printIt, copyIt bool,
	copyText func(string) error,
) error {
	if copyIt {
		if err := copyText(message); err != nil {
			return fmt.Errorf("copy the Slack message: %w; print it instead with archyl incident <element> --slack", err)
		}
		p.Eprintln(p.Check() + " " + p.Muted("Slack message copied to the clipboard."))
	}
	if printIt {
		_, err := io.WriteString(p.Out, ui.Sanitize(message))
		return err
	}
	return nil
}

// gatherIncident fetches what the plan needs, every call at once under one
// timeout. Each section is best effort: a failed call leaves its error in
// the facts and the report says the section is unavailable.
func gatherIncident(
	ctx context.Context,
	client *api.Client,
	projectID string,
	plan incident.Plan,
) incident.Facts {
	ctx, cancel := context.WithTimeout(ctx, incidentTimeout)
	defer cancel()

	f := incident.Facts{
		Owners:       map[string]incident.Owners{},
		OwnersFailed: map[string]bool{},
		Releases:     map[string][]api.ReleaseDetail{},
		Docs:         map[string][]api.Doc{},
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	// Owner lookups have slots of their own: up to a hundred of them must not
	// queue the other sections past the timeout.
	ownerSlots := make(chan struct{}, incidentConcurrency)
	slots := make(chan struct{}, incidentConcurrency)
	run := func(
		pool chan struct{},
		call func() (record func(), err error),
		failed func(err error),
	) {
		wg.Go(func() {
			pool <- struct{}{}
			record, err := call()
			<-pool
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed(err)
				return
			}
			record()
		})
	}
	failedTo := func(errOut *error) func(error) {
		return func(err error) { *errOut = err }
	}

	for _, n := range plan.OwnerTargets() {
		run(ownerSlots, func() (func(), error) {
			people, teams, err := client.ElementOwners(ctx, n.Level, n.ID)
			return func() { f.Owners[n.ID] = incident.NewOwners(people, teams) }, err
		}, func(err error) {
			f.OwnersErr = err
			f.OwnersFailed[n.ID] = true
		})
	}
	for _, n := range plan.ChangeTargets() {
		run(slots, func() (func(), error) {
			history, err := client.History(ctx, projectID, n.ID, incidentHistory)
			return func() { f.Changes = append(f.Changes, history...) }, err
		}, failedTo(&f.ChangesErr))
	}
	for _, n := range plan.ReleaseScopes() {
		filter := api.ReleaseFilter{Limit: incident.MaxReleases}
		if n.Level == "container" {
			filter.ContainerID = n.ID
		} else {
			filter.SystemID = n.ID
		}
		run(slots, func() (func(), error) {
			list, err := client.Releases(ctx, projectID, filter)
			return func() { f.Releases[n.ID] = list }, err
		}, failedTo(&f.ReleasesErr))
	}
	run(slots, func() (func(), error) {
		list, err := client.Releases(ctx, projectID, api.ReleaseFilter{Limit: incident.MaxReleases})
		return func() { f.ProjectReleases = list }, err
	}, failedTo(&f.ReleasesErr))
	for _, n := range plan.DocTargets() {
		run(slots, func() (func(), error) {
			docs, err := client.ElementDocs(ctx, n.Level, n.ID)
			return func() { f.Docs[n.ID] = docs }, err
		}, failedTo(&f.DocsErr))
	}
	run(slots, func() (func(), error) {
		memories, err := client.Recall(ctx, projectID, api.RecallOptions{Element: plan.Node.ID, Limit: incidentMemories})
		return func() { f.Memories = memories }, err
	}, failedTo(&f.MemoriesErr))
	wg.Wait()
	return f
}

// incidentLinks points every part of the report at the web app.
func incidentLinks(appURL, projectID string) incident.Links {
	if appURL == "" {
		return incident.Links{}
	}
	return incident.Links{
		Element: func(n *graph.Node) string { return weblink.Element(appURL, projectID, n) },
		Doc: func(id string) string {
			return weblink.SearchResult(appURL, api.SearchResult{Type: "documentation", ID: id, ProjectID: projectID})
		},
		ADR: func(id string) string { return weblink.ADR(appURL, projectID, id) },
	}
}

// incidentDiagram draws the element and its neighbours, centred, or nothing
// when even narrow boxes would not fit the terminal: a list of arrows adds
// nothing to the blast radius above it.
func incidentDiagram(p *ui.Printer, g *graph.Graph, n *graph.Node) string {
	v := diagram.Trim(diagram.Focus(g, n, 1), incidentDiagramBoxes)
	if len(v.Nodes) < 2 {
		return ""
	}
	width := p.Width()
	if diagram.ArrangeText(v, incidentNarrowBox).Width > width {
		return ""
	}
	drawing := diagram.Text(v, diagram.TextOptions{Theme: p.Theme(), Color: true, Highlight: v.Hops(), Width: width})
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, drawing)
}
