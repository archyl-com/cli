package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/tour"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/weblink"
)

// tourChurnCommits is how far back the tour looks for where work happens.
const tourChurnCommits = 300

func newTourCmd(app *App) *cobra.Command {
	var stops int
	var markdown, noAI bool
	cmd := &cobra.Command{
		Use:   "tour [element]",
		Short: "A guided walk through the codebase, from its architecture",
		Long: `Walk a newcomer through the codebase in a few minutes: the big picture,
then the parts worth meeting first, in the order work flows through them.
With several systems of your own, the stops are the systems; with one, its
containers. Shared infrastructure (queues, caches, databases, storage) is
listed in the big picture, never a stop.

Each stop shows its role in the flows, its key parts, what it uses and what
uses it, who owns it, the files to read first, the decisions that apply, what
people and agents remembered about it, the rules that guard it and a diagram.
The tour ends with a piece of work to follow through the code.

The story (the big picture, each stop's role, the main flows and the piece of
work to follow) is written by Archyl's AI from the model, and counts against
your AI quota. The facts come from Archyl and your repository. --no-ai skips
the story.

Name an element to tour only that part: a system's containers, or a
container and its components.

In a terminal the tour is full screen: n or → for the next stop, p or ← to go
back, j k to scroll, o to open the stop in the web app, q to quit. Piped, it
prints Markdown for coding agents; --markdown adds a Mermaid diagram per stop,
for an ONBOARDING.md.`,
		Example: `  archyl tour
  archyl tour Payments
  archyl tour --markdown > ONBOARDING.md
  archyl tour --json | jq '.stops[].name'`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeElements(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			if stops < 1 || stops > 20 {
				return fmt.Errorf("--stops must be between 1 and 20")
			}
			snap, err := loadModel(ctx, app, ref.ID)
			if err != nil {
				return err
			}
			g := graph.New(&snap.Model)
			if len(g.Roots) == 0 {
				p.Println(p.Muted("The model of " + snap.Project.Name + " is empty, so there is nothing to tour yet."))
				p.Println(p.Hint("Fill it from your code, or push a DSL file:", "archyl init --new"))
				return nil
			}
			var focus *graph.Node
			if len(args) == 1 {
				if focus, _ = g.Find(args[0]); focus == nil {
					return fmt.Errorf("no element matches %q; find one with archyl search %s", args[0], args[0])
				}
			}

			t, nodes := tour.Plan(g, tour.Options{Project: snap.Project.Name, Focus: focus, MaxStops: stops})
			client, err := app.Client()
			if err != nil {
				return err
			}
			var (
				story    *tour.Narration
				storyErr error
			)
			label := "Preparing the tour and writing its story…"
			if noAI {
				label = "Preparing the tour…"
			}
			if err := p.Spin(label, func() error {
				var wg sync.WaitGroup
				if !noAI {
					prompt := tour.NarrationPrompt(t)
					wg.Go(func() { story, storyErr = narrateTour(ctx, client, ref.ID, prompt) })
				}
				enrichTour(ctx, app, client, ref.ID, t, g, nodes, snap.ADRs)
				wg.Wait()
				return nil
			}); err != nil {
				return err
			}
			if story != nil {
				t.Apply(story)
			}
			t.Sanitize()
			switch {
			case story != nil:
			case storyErr != nil:
				p.Eprintln(p.Bang() + " " + p.Muted("No story this time ("+storyErr.Error()+"); the tour shows the model's facts. --no-ai skips this step."))
			}

			switch {
			case p.JSON:
				return p.WriteJSON(t)
			case markdown || !p.Interactive():
				_, err := fmt.Fprint(p.Out, ui.Sanitize(tour.Markdown(t, g, tour.MarkdownOptions{Diagrams: markdown})))
				return err
			}
			appURL := app.appURL()
			view := tui.NewTour(tui.TourOptions{
				Tour:  t,
				Graph: g,
				Theme: p.Theme(),
				Link: func(id string) string {
					if n := g.Node(id); n != nil {
						return weblink.Element(appURL, ref.ID, n)
					}
					return ""
				},
				Open: auth.OpenBrowser,
			})
			_, err = tea.NewProgram(view, tea.WithContext(ctx)).Run()
			return err
		},
	}
	cmd.Flags().IntVar(&stops, "stops", tour.DefaultStops, "how many elements to visit (1 to 20)")
	cmd.Flags().BoolVar(&markdown, "markdown", false, "print Markdown with a diagram per stop, for an ONBOARDING.md")
	cmd.Flags().BoolVar(&noAI, "no-ai", false, "skip the story written by AI; show the model's facts only")
	return cmd
}

// tourStoryTimeout bounds how long the tour waits for its story.
const tourStoryTimeout = 2 * time.Minute

// narrateTour asks Archyl's AI for the tour's story.
func narrateTour(ctx context.Context, client *api.Client, projectID, prompt string) (*tour.Narration, error) {
	ctx, cancel := context.WithTimeout(ctx, tourStoryTimeout)
	defer cancel()
	var reply strings.Builder
	messages := []api.ChatMessage{{Role: "user", Content: prompt}}
	if err := client.Chat(ctx, projectID, messages, func(chunk string) { reply.WriteString(chunk) }); err != nil {
		return nil, err
	}
	return tour.ParseNarration(reply.String())
}

// enrichTour adds what the model alone does not hold: files from the working
// tree, and owners, decisions, notes and rules from the API. Each part is
// best effort; a stop without it still makes sense.
func enrichTour(
	ctx context.Context,
	app *App,
	client *api.Client,
	projectID string,
	t *tour.Tour,
	g *graph.Graph,
	nodes []*graph.Node,
	adrs []api.ADR,
) {
	var r tour.Repo
	if git, err := app.Repo(ctx); err == nil {
		r.Files, _ = git.Files(ctx)
		r.Churn, _ = git.Churn(ctx, tourChurnCommits)
	}
	rules, _ := client.Rules(ctx, projectID)

	byID := make(map[string]*graph.Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	wide := wideDecisions(adrs, nodes)

	var wg sync.WaitGroup
	for i := range t.Stops {
		s := &t.Stops[i]
		if s.Kind == tour.KindOverview && s.ElementID == "" {
			s.Files = tour.Overview(r)
			s.Decisions = pickDecisions(adrs, func(adr api.ADR) bool { return wide[adr.Number] })
			continue
		}
		n := byID[s.ElementID]
		if n == nil && s.Kind == tour.KindOverview {
			n = graphNode(nodes, s.ElementID)
		}
		if n == nil {
			continue
		}
		s.Files = stopFiles(g, n, s.KeyParts, r)
		s.Decisions = tourDecisions(adrs, n, wide)
		first := ""
		if len(s.Files) > 0 {
			first = s.Files[0].Path
		}
		s.Rules = tourRules(rules, first, n)
		wg.Go(func() {
			owners, teams, err := client.ElementOwners(ctx, n.Level, n.ID)
			if err != nil {
				return
			}
			s.Owners = ownerLabels(owners, teams)
		})
		wg.Go(func() {
			memories, err := client.Recall(ctx, projectID, api.RecallOptions{Element: n.ID, Limit: 6})
			if err != nil {
				return
			}
			sort.SliceStable(memories, func(i, j int) bool { return memoryRank(memories[i].Kind) < memoryRank(memories[j].Kind) })
			for _, m := range memories[:min(len(memories), 3)] {
				text := m.Content
				if text == "" {
					text = m.Title
				}
				s.Notes = append(s.Notes, tour.Note{Kind: m.Kind, Text: text})
			}
		})
	}
	wg.Wait()
	addEntryPoints(t)
}

// ownerLabels names owners for people: teams first, as "<name> team" unless
// the name already says it, then people by name, or by email when they have
// none.
func ownerLabels(owners []api.Owner, teams []api.TeamOwner) []string {
	var out []string
	for _, team := range teams {
		name := strings.TrimSpace(team.Name)
		if name == "" {
			name = team.Slug
		}
		if name == "" {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), "team") {
			name += " team"
		}
		out = append(out, name)
	}
	for _, o := range owners {
		name := strings.TrimSpace(o.FirstName + " " + o.LastName)
		if name == "" {
			name = o.Email
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// addEntryPoints points the big picture of a whole model at where each stop
// starts, after its own introductory files.
func addEntryPoints(t *tour.Tour) {
	const limit = 6
	if len(t.Stops) == 0 || t.Stops[0].Kind != tour.KindOverview || t.Stops[0].ElementID != "" {
		return
	}
	overview := &t.Stops[0]
	for _, s := range t.Stops[1:] {
		if len(overview.Files) >= limit {
			return
		}
		for _, f := range s.Files {
			if strings.HasSuffix(f.Reason, tour.ReasonEntry) {
				overview.Files = append(overview.Files, tour.File{Path: f.Path, Reason: "where " + s.Name + " starts"})
				break
			}
		}
	}
}

// stopFiles are the files to read first for a stop. A system has no folder
// of its own, so its files come from its key parts, a couple each.
func stopFiles(g *graph.Graph, n *graph.Node, parts []tour.Part, r tour.Repo) []tour.File {
	const limit = 4
	if n.Level != "system" || len(parts) == 0 {
		return tour.StartHere(n, r, limit)
	}
	var out []tour.File
	for _, p := range parts {
		part := g.Node(p.ID)
		if part == nil {
			continue
		}
		for _, f := range tour.StartHere(part, r, 2) {
			if len(out) == limit {
				return out
			}
			f.Reason = p.Name + " · " + f.Reason
			out = append(out, f)
		}
	}
	return out
}

// graphNode finds the focus among the stops' nodes or their ancestors.
func graphNode(nodes []*graph.Node, id string) *graph.Node {
	for _, n := range nodes {
		for cur := n; cur != nil; cur = cur.Parent {
			if cur.ID == id {
				return cur
			}
		}
	}
	return nil
}

// wideDecisionStops is how many stops a decision must apply to before it
// counts as project-wide: shown once in the big picture, not on every stop.
const wideDecisionStops = 3

// wideDecisions are the decisions that apply to many of the stops.
func wideDecisions(adrs []api.ADR, stops []*graph.Node) map[int]bool {
	wide := map[int]bool{}
	if len(stops) < wideDecisionStops {
		return wide
	}
	for _, adr := range adrs {
		count := 0
		for _, n := range stops {
			if linkedInside(adr, n) {
				count++
			}
		}
		if count >= wideDecisionStops {
			wide[adr.Number] = true
		}
	}
	return wide
}

// tourDecisions are the decisions linked to the element or anything inside
// it, accepted first, without the project-wide ones.
func tourDecisions(adrs []api.ADR, n *graph.Node, wide map[int]bool) []tour.Decision {
	return pickDecisions(adrs, func(adr api.ADR) bool { return !wide[adr.Number] && linkedInside(adr, n) })
}

// pickDecisions keeps the decisions keep selects: accepted first, newest
// first, at most three.
func pickDecisions(adrs []api.ADR, keep func(api.ADR) bool) []tour.Decision {
	var out []tour.Decision
	for _, adr := range adrs {
		if keep(adr) {
			out = append(out, tour.Decision{Number: adr.Number, Title: adr.Title, Status: adr.Status})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := strings.EqualFold(out[i].Status, "accepted"), strings.EqualFold(out[j].Status, "accepted")
		if ai != aj {
			return ai
		}
		return out[i].Number > out[j].Number
	})
	return out[:min(len(out), 3)]
}

// linkedInside reports whether a decision is linked to n or anything in it.
func linkedInside(adr api.ADR, n *graph.Node) bool {
	inside := map[string]bool{}
	var mark func(m *graph.Node)
	mark = func(m *graph.Node) {
		inside[m.ID] = true
		for _, c := range m.Children {
			mark(c)
		}
	}
	mark(n)
	for _, l := range adr.Links {
		if inside[l.ElementID] {
			return true
		}
	}
	return false
}

// tourRules are the rules aimed at the element, the way why finds them for a
// file.
func tourRules(rules []api.RuleDetail, file string, n *graph.Node) []tour.Rule {
	lineage := map[string]string{}
	for _, a := range n.Path() {
		lineage[a.ID] = a.Name
	}
	var out []tour.Rule
	found, _ := whyRules(rules, file, lineage)
	for _, r := range found {
		out = append(out, tour.Rule{Name: r.Name, Severity: r.Severity})
	}
	return out[:min(len(out), 3)]
}
