package incident

import (
	"sort"

	"github.com/archyl-com/cli/internal/graph"
)

// Plan is what a report covers, decided from the model alone, so the
// command knows what to fetch before it fetches anything.
type Plan struct {
	Node             *graph.Node
	Depth            int
	Dependents       []graph.Hop
	MoreDependents   int
	Dependencies     []graph.Hop
	MoreDependencies int
	// Notified are the dependents whose owners are told, a longer list than
	// the one shown; NotifyUnlisted counts those beyond it.
	Notified       []graph.Hop
	NotifyUnlisted int
}

// NewPlan walks the model around n: who depends on it up to depth hops, and
// what it depends on directly. Each list keeps its closest elements.
func NewPlan(g *graph.Graph, n *graph.Node, depth int) Plan {
	depth = ClampDepth(depth)
	dependents, dependencies := BlastRadius(g, n, depth)
	p := Plan{Node: n, Depth: depth}
	p.Dependents, p.MoreDependents = capHops(dependents, MaxDependents)
	p.Notified, p.NotifyUnlisted = capHops(dependents, MaxNotified)
	p.Dependencies, p.MoreDependencies = capHops(dependencies, MaxDependencies)
	return p
}

// ClampDepth keeps a depth between one hop and MaxDepth.
func ClampDepth(depth int) int {
	return min(max(depth, 1), MaxDepth)
}

// BlastRadius lists who depends on n, up to depth hops, and what n depends
// on, one hop, closest first. A relationship of one of n's descendants
// counts as n's own: a container is in trouble when its components are.
// Neither list holds n's descendants or ancestors.
func BlastRadius(g *graph.Graph, n *graph.Node, depth int) (dependents, dependencies []graph.Hop) {
	seeds := subtree(n)
	skip := map[string]bool{}
	for _, s := range seeds {
		skip[s.ID] = true
	}
	for p := n.Parent; p != nil; p = p.Parent {
		skip[p.ID] = true
	}
	return walk(seeds, skip, depth, g.Incoming), walk(seeds, skip, 1, g.Outgoing)
}

// walk is a breadth-first search from the seeds along edges, never entering
// a skipped element.
func walk(
	seeds []*graph.Node,
	skip map[string]bool,
	depth int,
	edges func(id string) []graph.Edge,
) []graph.Hop {
	seen := make(map[string]bool, len(skip))
	for id := range skip {
		seen[id] = true
	}
	var out []graph.Hop
	frontier := seeds
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []*graph.Node
		for _, cur := range frontier {
			for _, e := range edges(cur.ID) {
				if seen[e.Other.ID] {
					continue
				}
				seen[e.Other.ID] = true
				out = append(out, graph.Hop{Node: e.Other, Distance: d, Via: e.Rel.Type})
				next = append(next, e.Other)
			}
		}
		frontier = next
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Distance != b.Distance {
			return a.Distance < b.Distance
		}
		if ra, rb := graph.LevelRank(a.Node.Level), graph.LevelRank(b.Node.Level); ra != rb {
			return ra < rb
		}
		return a.Node.Name < b.Node.Name
	})
	return out
}

// subtree is n and everything inside it.
func subtree(n *graph.Node) []*graph.Node {
	out := []*graph.Node{n}
	for _, c := range n.Children {
		out = append(out, subtree(c)...)
	}
	return out
}

func capHops(hops []graph.Hop, limit int) ([]graph.Hop, int) {
	if len(hops) <= limit {
		return hops, 0
	}
	return hops[:limit], len(hops) - limit
}

// OwnerTargets are the elements whose owners the report needs: the element,
// the dependencies shown, the dependents notified, and their ancestors, since
// an element without owners inherits those of its closest owned ancestor.
// Code elements have no owners of their own and are left out.
func (p Plan) OwnerTargets() []*graph.Node {
	var out []*graph.Node
	seen := map[string]bool{}
	add := func(n *graph.Node) {
		for cur := n; cur != nil; cur = cur.Parent {
			if !seen[cur.ID] && cur.Level != "code" {
				seen[cur.ID] = true
				out = append(out, cur)
			}
		}
	}
	add(p.Node)
	for _, h := range p.Notified {
		add(h.Node)
	}
	for _, h := range p.Dependencies {
		add(h.Node)
	}
	return out
}

// ChangeTargets are the elements whose recent changes matter: the element
// and what it depends on, since a failing dependency is often the cause.
func (p Plan) ChangeTargets() []*graph.Node {
	out := []*graph.Node{p.Node}
	for _, h := range p.Dependencies {
		out = append(out, h.Node)
	}
	return out
}

// ReleaseScopes are the elements releases are recorded against, closest
// first: the element's container, then its system.
func (p Plan) ReleaseScopes() []*graph.Node {
	var out []*graph.Node
	for cur := p.Node; cur != nil; cur = cur.Parent {
		if cur.Level == "container" || cur.Level == "system" {
			out = append(out, cur)
		}
	}
	return out
}

// DocTargets are the elements whose documentation applies: the element,
// then its ancestors, closest first. A runbook is often written for the
// whole system.
func (p Plan) DocTargets() []*graph.Node {
	var out []*graph.Node
	for cur := p.Node; cur != nil; cur = cur.Parent {
		out = append(out, cur)
	}
	return out
}
