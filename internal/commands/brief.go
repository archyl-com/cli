package commands

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/graph"
)

// maxBriefElements bounds how many elements a brief details.
const maxBriefElements = 6

func newBriefCmd(app *App) *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "brief [path...]",
		Short: "What to know before editing these files",
		Long: `Brief yourself, or an agent, before touching code: for the given files (or
every file your branch changes), the elements they belong to, what those
elements use and what depends on them, the decisions that apply, and what
Archyl remembers about them — conventions, pitfalls, notes.

In a terminal it renders; piped or run by an agent it prints compact markdown.`,
		Example: `  archyl brief internal/payments/refund.go
  archyl brief            # every file the branch changes`,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			paths, err := briefPaths(ctx, app, args, base)
			if err != nil {
				return err
			}
			if len(paths) == 0 {
				return fmt.Errorf("no file to brief on: name files, or change some on this branch")
			}

			var brief *briefData
			if err := p.Spin("Gathering what to know…", func() error {
				brief, err = gatherBrief(ctx, client, project.ID, paths)
				return err
			}); err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(brief)
			}
			app.printMarkdown(brief.markdown())
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "with no paths, the ref to compare with")
	return cmd
}

func briefPaths(ctx context.Context, app *App, args []string, base string) ([]string, error) {
	git, err := app.Repo(ctx)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		base, err := resolveBase(ctx, git, base, ci.Detect(os.Getenv))
		if err != nil {
			return nil, err
		}
		changes, err := git.Changes(ctx, base)
		if err != nil {
			return nil, err
		}
		var paths []string
		for _, c := range changes {
			paths = append(paths, c.Path)
		}
		return paths, nil
	}
	cwd, _ := os.Getwd()
	var paths []string
	for _, arg := range args {
		rel, err := git.Rel(cwd, arg)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(arg); err == nil && info.IsDir() {
			files, err := git.Files(ctx, rel)
			if err != nil {
				return nil, err
			}
			paths = append(paths, files...)
			continue
		}
		paths = append(paths, rel)
	}
	if len(paths) > maxPaths {
		paths = paths[:maxPaths]
	}
	return paths, nil
}

// briefElement is one element a brief details.
type briefElement struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Level       string       `json:"level"`
	Path        string       `json:"path"`
	Description string       `json:"description,omitempty"`
	FilePath    string       `json:"filePath,omitempty"`
	Tech        []string     `json:"technologies,omitempty"`
	Owners      []string     `json:"owners,omitempty"`
	Uses        []string     `json:"uses,omitempty"`
	UsedBy      []string     `json:"usedBy,omitempty"`
	Dependents  int          `json:"dependents"`
	Decisions   []api.ADR    `json:"decisions,omitempty"`
	Memories    []api.Memory `json:"memories,omitempty"`
	Files       []string     `json:"files"`
}

type briefData struct {
	Elements  []*briefElement `json:"elements"`
	Unmatched []string        `json:"unmatched,omitempty"`
	Notify    []string        `json:"notify,omitempty"`
}

func gatherBrief(ctx context.Context, client *api.Client, projectID string, paths []string) (*briefData, error) {
	var (
		located *api.LocateResult
		model   *api.Model
		adrs    []api.ADR
		errs    = make([]error, 2)
		wg      sync.WaitGroup
	)
	wg.Go(func() { located, errs[0] = client.Locate(ctx, projectID, paths, 2) })
	wg.Go(func() { model, errs[1] = client.Model(ctx, projectID) })
	wg.Go(func() { adrs, _ = client.ADRs(ctx, projectID) })
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}

	g := graph.New(model)
	out := &briefData{Unmatched: located.Unmatched}
	byID := map[string]*briefElement{}
	for _, f := range located.Files {
		if len(f.Elements) == 0 {
			continue
		}
		id := f.Elements[0].ID
		if e := byID[id]; e != nil {
			e.Files = append(e.Files, f.Path)
			continue
		}
		n := g.Node(id)
		if n == nil {
			continue
		}
		e := &briefElement{ID: n.ID, Name: n.Name, Level: n.Level, Path: n.PathString(), Description: n.Description,
			FilePath: n.FilePath, Tech: n.Tech, Files: []string{f.Path}, Dependents: len(g.Dependents(n.ID, 3))}
		for _, edge := range g.Outgoing(n.ID) {
			e.Uses = append(e.Uses, fmt.Sprintf("%s (%s)", edge.Other.Name, relationVerb(edge.Rel.Type)))
		}
		for _, edge := range g.Incoming(n.ID) {
			e.UsedBy = append(e.UsedBy, fmt.Sprintf("%s (%s)", edge.Other.Name, relationVerb(edge.Rel.Type)))
		}
		// Decisions linked to the element or any of its ancestors apply to it.
		lineage := map[string]bool{}
		for _, a := range n.Path() {
			lineage[a.ID] = true
		}
		for _, adr := range adrs {
			for _, l := range adr.Links {
				if lineage[l.ElementID] {
					e.Decisions = append(e.Decisions, adr)
					break
				}
			}
		}
		byID[id] = e
		out.Elements = append(out.Elements, e)
	}
	sort.SliceStable(out.Elements, func(i, j int) bool { return len(out.Elements[i].Files) > len(out.Elements[j].Files) })
	if len(out.Elements) > maxBriefElements {
		out.Elements = out.Elements[:maxBriefElements]
	}

	if located.Impact != nil {
		for _, t := range located.Impact.Notify.Teams {
			out.Notify = append(out.Notify, "@"+firstNonEmpty(t.Slug, t.Name))
		}
		for _, u := range located.Impact.Notify.Users {
			out.Notify = append(out.Notify, u.Email)
		}
		for _, touched := range located.Impact.Elements {
			if e := byID[touched.ID]; e != nil {
				for _, o := range touched.TeamOwners {
					e.Owners = append(e.Owners, "@"+firstNonEmpty(o.Slug, o.Name))
				}
				for _, o := range touched.Owners {
					e.Owners = append(e.Owners, o.Email)
				}
			}
		}
	}

	// Memories are per element; fetch them side by side.
	var mu sync.Mutex
	var mwg sync.WaitGroup
	for _, e := range out.Elements {
		mwg.Go(func() {
			memories, err := client.Recall(ctx, projectID, api.RecallOptions{Element: e.ID, Limit: 5})
			if err == nil {
				mu.Lock()
				e.Memories = memories
				mu.Unlock()
			}
		})
	}
	mwg.Wait()
	return out, nil
}

func (b *briefData) markdown() string {
	var s strings.Builder
	files := 0
	for _, e := range b.Elements {
		files += len(e.Files)
	}
	fmt.Fprintf(&s, "# Architecture brief\n\n")
	if len(b.Elements) == 0 {
		s.WriteString("None of these files belongs to a documented element yet.\n")
	}
	for _, e := range b.Elements {
		fmt.Fprintf(&s, "## %s (%s)\n\n", e.Name, e.Level)
		fmt.Fprintf(&s, "`%s`", e.Path)
		if e.FilePath != "" {
			fmt.Fprintf(&s, " — declared at `%s`", e.FilePath)
		}
		s.WriteString("\n\n")
		if e.Description != "" {
			s.WriteString(e.Description + "\n\n")
		}
		list := func(label string, items []string) {
			if len(items) > 0 {
				fmt.Fprintf(&s, "- **%s:** %s\n", label, strings.Join(items, ", "))
			}
		}
		list("Files", wrapCode(e.Files, 8))
		list("Technologies", e.Tech)
		list("Owners", e.Owners)
		list("Uses", e.Uses)
		list("Used by", e.UsedBy)
		if e.Dependents > 0 {
			fmt.Fprintf(&s, "- **Blast radius:** %d element(s) depend on it within 3 hops\n", e.Dependents)
		}
		if len(e.Decisions) > 0 {
			s.WriteString("\n**Decisions to respect**\n\n")
			for _, adr := range e.Decisions {
				fmt.Fprintf(&s, "- ADR-%d %s (%s)", adr.Number, adr.Title, adr.Status)
				if d := strings.TrimSpace(adr.Decision); d != "" {
					fmt.Fprintf(&s, ": %s", truncate(d, 200))
				}
				s.WriteString("\n")
			}
		}
		if len(e.Memories) > 0 {
			s.WriteString("\n**What Archyl remembers**\n\n")
			s.WriteString(memoriesMarkdown(e.Memories))
		}
		s.WriteString("\n")
	}
	if len(b.Unmatched) > 0 {
		fmt.Fprintf(&s, "Not in the model: %s\n\n", strings.Join(wrapCode(b.Unmatched, 10), ", "))
	}
	if len(b.Notify) > 0 {
		fmt.Fprintf(&s, "Tell before merging: %s\n", strings.Join(b.Notify, ", "))
	}
	return strings.TrimRight(s.String(), "\n")
}

// wrapCode formats paths as code spans, eliding past limit.
func wrapCode(items []string, limit int) []string {
	out := make([]string, 0, min(len(items), limit+1))
	for i, it := range items {
		if i == limit {
			out = append(out, fmt.Sprintf("+%d more", len(items)-limit))
			break
		}
		out = append(out, "`"+it+"`")
	}
	return out
}
