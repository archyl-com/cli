package incident

import (
	"sort"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// Facts is what the command fetched for a plan. Each section is best
// effort: an error with nothing fetched makes the section unavailable, an
// error with partial data keeps what arrived.
type Facts struct {
	// Owners holds the owners of each element looked up, by ID.
	Owners map[string]Owners
	// OwnersFailed holds the elements whose lookup failed: their owners are
	// unknown, so they are neither "none" nor inherited.
	OwnersFailed map[string]bool
	// OwnersErr is the last lookup failure, for any element.
	OwnersErr error

	Changes    []api.HistoryEntry
	ChangesErr error

	// Releases holds the releases recorded against each release scope, by
	// element ID; ProjectReleases are the project's latest.
	Releases        map[string][]api.ReleaseDetail
	ProjectReleases []api.ReleaseDetail
	ReleasesErr     error

	// Docs holds the documentation linked to each doc target, by element ID.
	Docs    map[string][]api.Doc
	DocsErr error

	Memories    []api.Memory
	MemoriesErr error
}

// Links builds web app links. Any of them may be nil.
type Links struct {
	Element func(n *graph.Node) string
	Doc     func(id string) string
	ADR     func(id string) string
}

// Input is everything Build needs.
type Input struct {
	Plan  Plan
	ADRs  []api.ADR
	Facts Facts
	Links Links
	Now   time.Time
}

// Build assembles the report.
func Build(in Input) *Report {
	n := in.Plan.Node
	f := in.Facts
	r := &Report{
		Element:          newElement(n, in.Links.Element),
		Depth:            in.Plan.Depth,
		Dependents:       impacted(in.Plan.Dependents, f, in.Links.Element),
		MoreDependents:   in.Plan.MoreDependents,
		Dependencies:     impacted(in.Plan.Dependencies, f, in.Links.Element),
		MoreDependencies: in.Plan.MoreDependencies,
		Changes:          changes(f.Changes),
		Docs:             docs(in.Plan.DocTargets(), f.Docs, in.Links.Doc),
		Decisions:        decisions(in.ADRs, n, in.Links.ADR),
		Notes:            notes(f.Memories),
		NotifyUnlisted:   in.Plan.NotifyUnlisted,
		Unavailable:      []string{},
		GeneratedAt:      in.Now,
	}
	r.Owners, r.OwnersFrom, r.OwnersUnknown = resolveOwners(n, f)
	r.Releases, r.ReleasesOf = releases(in.Plan.ReleaseScopes(), f.Releases, f.ProjectReleases)
	r.Notify = notify(r.Owners, impacted(in.Plan.Notified, f, nil))

	for _, s := range []struct {
		section string
		err     error
		empty   bool
	}{
		// Any failed lookup leaves "Who to tell" incomplete, even when the
		// element's own owners arrived.
		{SectionOwners, f.OwnersErr, true},
		{SectionChanges, f.ChangesErr, len(r.Changes) == 0},
		{SectionReleases, f.ReleasesErr, len(r.Releases) == 0},
		{SectionDocs, f.DocsErr, len(r.Docs) == 0},
		{SectionNotes, f.MemoriesErr, len(r.Notes) == 0},
	} {
		if s.err != nil && s.empty {
			r.Unavailable = append(r.Unavailable, s.section)
		}
	}
	return r
}

// resolveOwners finds who owns n: its own owners, or those of its closest
// owned ancestor, whose name it returns. It stops at the first element whose
// lookup failed and reports the owners unknown: skipping it would page the
// ancestor's team for an element that may well have owners of its own.
func resolveOwners(n *graph.Node, f Facts) (owners Owners, from string, unknown bool) {
	none := Owners{Teams: []Team{}, People: []Person{}}
	for cur := n; cur != nil; cur = cur.Parent {
		if f.OwnersFailed[cur.ID] {
			return none, "", true
		}
		o, ok := f.Owners[cur.ID]
		if !ok || o.Empty() {
			continue
		}
		if cur == n {
			return o, "", false
		}
		return o, clean(cur.Name), false
	}
	return none, "", false
}

func impacted(hops []graph.Hop, f Facts, link func(*graph.Node) string) []Impacted {
	out := make([]Impacted, 0, len(hops))
	for _, h := range hops {
		o, from, unknown := resolveOwners(h.Node, f)
		out = append(out, Impacted{
			Element:       newElement(h.Node, link),
			Distance:      h.Distance,
			Via:           clean(h.Via),
			Owners:        o,
			OwnersFrom:    from,
			OwnersUnknown: unknown,
		})
	}
	return out
}

// notify is every owner of the element and of its dependents, each once,
// in the order met: the element's own owners come first.
func notify(own Owners, dependents []Impacted) Owners {
	out := Owners{Teams: []Team{}, People: []Person{}}
	seenTeams := map[string]bool{}
	seenPeople := map[string]bool{}
	add := func(o Owners) {
		for _, t := range o.Teams {
			key := strings.ToLower(firstNonEmpty(t.Slug, t.Name))
			if !seenTeams[key] {
				seenTeams[key] = true
				out.Teams = append(out.Teams, t)
			}
		}
		for _, p := range o.People {
			if key := p.key(); !seenPeople[key] {
				seenPeople[key] = true
				out.People = append(out.People, p)
			}
		}
	}
	add(own)
	for _, d := range dependents {
		add(d.Owners)
	}
	return out
}

var actionVerbs = map[string]string{
	"create": "created", "update": "updated", "delete": "deleted",
	"open": "opened", "close": "closed", "reopen": "reopened", "merge": "merged",
}

// changes merges the history of several elements: each change once,
// latest first.
func changes(entries []api.HistoryEntry) []Change {
	seen := map[string]bool{}
	var unique []api.HistoryEntry
	for _, h := range entries {
		key := firstNonEmpty(h.ID, h.EntityID+h.CreatedAt.String())
		if !seen[key] {
			seen[key] = true
			unique = append(unique, h)
		}
	}
	sort.SliceStable(unique, func(i, j int) bool { return unique[i].CreatedAt.After(unique[j].CreatedAt) })
	out := make([]Change, 0, min(len(unique), MaxChanges))
	for _, h := range unique[:min(len(unique), MaxChanges)] {
		out = append(out, Change{
			At:      h.CreatedAt,
			Who:     clean(h.UserName),
			Action:  clean(firstNonEmpty(actionVerbs[h.Action], h.Action)),
			Element: clean(h.EntityName),
			Type:    clean(strings.ReplaceAll(h.EntityType, "_", " ")),
		})
	}
	return out
}

// releases picks the releases of the closest scope that has any, else the
// project's latest, and names the scope.
func releases(
	scopes []*graph.Node,
	byScope map[string][]api.ReleaseDetail,
	project []api.ReleaseDetail,
) ([]Release, string) {
	for _, s := range scopes {
		if list := byScope[s.ID]; len(list) > 0 {
			return toReleases(list), clean(s.Name)
		}
	}
	return toReleases(project), ""
}

func toReleases(list []api.ReleaseDetail) []Release {
	sorted := append([]api.ReleaseDetail(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].When().After(sorted[j].When()) })
	out := make([]Release, 0, min(len(sorted), MaxReleases))
	for _, r := range sorted[:min(len(sorted), MaxReleases)] {
		rel := Release{Version: clean(r.Version), Status: clean(r.Status), At: r.When(), URL: clean(r.SourceURL)}
		if r.Environment != nil {
			rel.Environment = clean(r.Environment.Name)
			rel.Production = r.Environment.IsProduction
		}
		out = append(out, rel)
	}
	return out
}

// docs lists the documentation of the element, then of its ancestors, each
// page once.
func docs(targets []*graph.Node, byElement map[string][]api.Doc, link func(string) string) []Doc {
	seen := map[string]bool{}
	var out []Doc
	for i, t := range targets {
		for _, d := range byElement[t.ID] {
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			doc := Doc{Title: clean(d.Title)}
			if link != nil {
				doc.URL = link(d.ID)
			}
			if i > 0 {
				doc.Via = clean(t.Name)
			}
			out = append(out, doc)
		}
	}
	if out == nil {
		return []Doc{}
	}
	return out[:min(len(out), MaxDocs)]
}

// decisions lists the decisions linked to the element, to anything inside
// it or to its ancestors; accepted ones first, then the latest.
func decisions(adrs []api.ADR, n *graph.Node, link func(string) string) []Decision {
	related := map[string]bool{}
	for _, s := range subtree(n) {
		related[s.ID] = true
	}
	for p := n.Parent; p != nil; p = p.Parent {
		related[p.ID] = true
	}
	out := []Decision{}
	for _, adr := range adrs {
		for _, l := range adr.Links {
			if !related[l.ElementID] {
				continue
			}
			d := Decision{Number: adr.Number, Title: clean(adr.Title), Status: clean(adr.Status)}
			if link != nil {
				d.URL = link(adr.ID)
			}
			out = append(out, d)
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := strings.EqualFold(out[i].Status, "accepted"), strings.EqualFold(out[j].Status, "accepted")
		if ai != aj {
			return ai
		}
		return out[i].Number > out[j].Number
	})
	return out[:min(len(out), MaxDecisions)]
}

// noteRank puts pitfalls first: at 3 a.m. the trap matters more than the
// convention.
func noteRank(kind string) int {
	switch kind {
	case "pitfall":
		return 0
	case "note":
		return 1
	case "convention":
		return 2
	}
	return 3
}

func notes(memories []api.Memory) []Note {
	out := []Note{}
	for _, m := range memories {
		text := clean(firstNonEmpty(m.Content, m.Title))
		if text != "" {
			out = append(out, Note{Kind: clean(m.Kind), Text: text})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return noteRank(out[i].Kind) < noteRank(out[j].Kind) })
	return out[:min(len(out), MaxNotes)]
}
