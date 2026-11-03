package diagram

import (
	"sort"

	"github.com/archyl-com/cli/internal/graph"
)

// KindFocus is a view centred on one element of any level.
const KindFocus = "focus"

// MaxFocusDepth bounds how many hops a focus view walks.
const MaxFocusDepth = 3

// Focus returns the view centred on n: n and the elements its relationships
// reach within depth hops, both ways. The relationships of n's descendants
// count as n's own. An end deeper than n is drawn as its ancestor at n's
// level, so a component sees the components, containers and systems around
// it, never their code. Relationships with n's own ancestors are left out.
func Focus(g *graph.Graph, n *graph.Node, depth int) *View {
	depth = min(max(depth, 1), MaxFocusDepth)
	rank := graph.LevelRank(n.Level)
	ancestors := map[*graph.Node]bool{}
	for p := n.Parent; p != nil; p = p.Parent {
		ancestors[p] = true
	}
	rep := func(x *graph.Node) *graph.Node {
		if ancestors[x] {
			return nil
		}
		for cur := x; cur != nil; cur = cur.Parent {
			if cur == n {
				return n
			}
		}
		for x.Parent != nil && graph.LevelRank(x.Level) > rank {
			x = x.Parent
		}
		return x
	}

	type pair struct{ a, b string }
	var edges []Edge
	index := map[pair]int{}
	adjacent := map[string][]*graph.Node{}
	for _, x := range g.All() {
		for _, e := range g.Outgoing(x.ID) {
			src, dst := rep(x), rep(e.Other)
			if src == nil || dst == nil || src == dst {
				continue
			}
			if i, ok := index[pair{dst.ID, src.ID}]; ok {
				edges[i].Both = true
				edges[i].Count++
				continue
			}
			if i, ok := index[pair{src.ID, dst.ID}]; ok {
				edges[i].Count++
				continue
			}
			index[pair{src.ID, dst.ID}] = len(edges)
			edges = append(edges, Edge{From: src.ID, To: dst.ID, Label: relLabel(e.Rel), Count: 1})
			adjacent[src.ID] = append(adjacent[src.ID], dst)
			adjacent[dst.ID] = append(adjacent[dst.ID], src)
		}
	}

	hops := map[string]int{n.ID: 0}
	order := []*graph.Node{n}
	frontier := []*graph.Node{n}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []*graph.Node
		for _, cur := range frontier {
			for _, other := range adjacent[cur.ID] {
				if _, seen := hops[other.ID]; seen {
					continue
				}
				hops[other.ID] = d
				order = append(order, other)
				next = append(next, other)
			}
		}
		frontier = next
	}

	v := &View{Kind: KindFocus, Title: "Around " + n.Name, FocusID: n.ID, members: map[string]string{}, hops: Highlight{}}
	for _, x := range order {
		node := Node{
			ID: x.ID, Name: x.Name, Level: x.Level, Kind: x.Kind, Tech: x.Tech,
			External: x.External, Children: len(x.Children),
		}
		if x.Parent != nil && x.Parent != n.Parent {
			node.Parent = x.Parent.Name
		}
		v.Nodes = append(v.Nodes, node)
		v.hops[x.ID] = hops[x.ID]
	}
	for _, e := range edges {
		_, from := hops[e.From]
		_, to := hops[e.To]
		if from && to {
			v.Edges = append(v.Edges, e)
		}
	}
	for _, x := range g.All() {
		if r := rep(x); r != nil {
			if _, shown := hops[r.ID]; shown {
				v.members[x.ID] = r.ID
			}
		}
	}
	return v
}

// Hops is the highlight of a focus view: 0 for the element in the middle,
// then the hops to each box. Other views have none.
func (v *View) Hops() Highlight {
	return v.hops
}

// Trim keeps at most max boxes: the focus, then the changed boxes, then the
// boxes with the most relationships, and the arrows between the boxes kept. A view within the
// limit is returned as is.
func Trim(v *View, limit int) *View {
	if limit <= 0 || len(v.Nodes) <= limit {
		return v
	}
	degree := map[string]int{}
	for _, e := range v.Edges {
		degree[e.From] += e.Count
		degree[e.To] += e.Count
	}
	ranked := make([]Node, len(v.Nodes))
	copy(ranked, v.Nodes)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if (a.ID == v.FocusID) != (b.ID == v.FocusID) {
			return a.ID == v.FocusID
		}
		if (a.Change != "") != (b.Change != "") {
			return a.Change != ""
		}
		if ha, hb := v.hopsOf(a.ID), v.hopsOf(b.ID); ha != hb {
			return ha < hb
		}
		return degree[a.ID] > degree[b.ID]
	})
	keep := map[string]bool{}
	for _, n := range ranked[:limit] {
		keep[n.ID] = true
	}
	return v.only(keep)
}

// Drop removes the boxes drop selects, with their arrows.
func Drop(v *View, drop func(Node) bool) *View {
	keep := map[string]bool{}
	for _, n := range v.Nodes {
		if !drop(n) {
			keep[n.ID] = true
		}
	}
	if len(keep) == len(v.Nodes) {
		return v
	}
	return v.only(keep)
}

// only is the view reduced to the boxes in keep.
func (v *View) only(keep map[string]bool) *View {
	out := &View{Kind: v.Kind, Title: v.Title, ScopeID: v.ScopeID, FocusID: v.FocusID, members: map[string]string{}}
	for _, n := range v.Nodes {
		if keep[n.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range v.Edges {
		if keep[e.From] && keep[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	for element, rep := range v.members {
		if keep[rep] {
			out.members[element] = rep
		}
	}
	if v.hops != nil {
		out.hops = Highlight{}
		for id, d := range v.hops {
			if keep[id] {
				out.hops[id] = d
			}
		}
	}
	return out
}

// hopsOf is how far a box is from the focus; every box ties outside a focus
// view.
func (v *View) hopsOf(id string) int {
	if d, ok := v.hops[id]; ok {
		return d
	}
	return 0
}
