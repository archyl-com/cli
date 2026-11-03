// Package diagram draws C4 views of a model: it picks the elements of a view,
// lays them out in layers and renders them as box-drawing text, PNG, SVG or
// Mermaid.
package diagram

import (
	"strings"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// Kinds of view, after the C4 diagrams they mirror.
const (
	KindContext   = "context"
	KindContainer = "container"
	KindComponent = "component"
)

// Node is a box of a view.
type Node struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Level string   `json:"level"`
	Kind  string   `json:"kind,omitempty"`
	Tech  []string `json:"technologies,omitempty"`
	// External is set on elements outside the view's scope, drawn for their
	// relationships with it, and on systems marked external.
	External bool `json:"external,omitempty"`
	// Children is how many elements the next view down would show.
	Children int `json:"children,omitempty"`
	// Parent names the element's parent when the view mixes several, as a
	// focus view does.
	Parent string `json:"parent,omitempty"`
	// Change is how the box differs from an earlier state, set by
	// MarkChanges.
	Change Change `json:"change,omitempty"`
}

// Edge is one arrow of a view: every relationship between the elements its
// ends stand for, merged.
type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
	// Both is set when relationships run both ways.
	Both bool `json:"bidirectional,omitempty"`
	// Indirect is set when the elements in between are unknown: the arrow
	// stands for a path, not one relationship.
	Indirect bool `json:"indirect,omitempty"`
	// Change is how the arrow differs from an earlier state, set by
	// MarkChanges.
	Change Change `json:"change,omitempty"`
}

// View is the set of boxes and arrows of one diagram.
type View struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	ScopeID string `json:"scopeId,omitempty"`
	// FocusID is the element a focus view is centred on.
	FocusID string `json:"focusId,omitempty"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`

	// members maps every model element to the node standing for it.
	members map[string]string
	// hops is how far each box of a focus view is from its centre.
	hops Highlight
}

// NewView makes a view from boxes and arrows built elsewhere, such as from
// an impact report without the model. Each box stands for itself.
func NewView(kind, title string, nodes []Node, edges []Edge) *View {
	v := &View{Kind: kind, Title: title, Nodes: nodes, Edges: edges, members: make(map[string]string, len(nodes))}
	for _, n := range nodes {
		v.members[n.ID] = n.ID
	}
	return v
}

// Highlight marks elements by distance from a change: 0 for the elements
// touched, then the hops to each dependent. Elements absent are unrelated.
type Highlight map[string]int

// Node returns the box with this ID.
func (v *View) Node(id string) *Node {
	for i := range v.Nodes {
		if v.Nodes[i].ID == id {
			return &v.Nodes[i]
		}
	}
	return nil
}

// Rep returns the box an element is drawn as: itself, the ancestor shown in
// the view, or "" when the view does not show it.
func (v *View) Rep(elementID string) string {
	return v.members[elementID]
}

// Lift maps an element highlight onto the boxes: a box takes the closest
// distance of the elements it stands for.
func (v *View) Lift(h Highlight) Highlight {
	if len(h) == 0 {
		return nil
	}
	out := Highlight{}
	for id, d := range h {
		rep := v.members[id]
		if rep == "" {
			continue
		}
		if cur, ok := out[rep]; !ok || d < cur {
			out[rep] = d
		}
	}
	return out
}

// For returns the view one level down from n: the system context for nil, a
// system's containers, a container's components. Elements without children
// show the view they sit in.
func For(g *graph.Graph, n *graph.Node) *View {
	return ForIncluding(g, n, nil)
}

// ForIncluding is For with the boxes standing for the extra elements added,
// even when no relationship links them to the view.
func ForIncluding(g *graph.Graph, n *graph.Node, extra []string) *View {
	for ; n != nil; n = n.Parent {
		if len(n.Children) == 0 {
			continue
		}
		switch n.Level {
		case "system":
			return build(g, KindContainer, n, extra)
		case "container":
			return build(g, KindComponent, n, extra)
		}
	}
	return build(g, KindContext, nil, extra)
}

// Around returns the smallest view holding every touched element of h, with
// the boxes of its dependents added.
func Around(g *graph.Graph, h Highlight) *View {
	var common []*graph.Node
	first := true
	for _, n := range g.All() {
		d, ok := h[n.ID]
		if !ok || d != 0 {
			continue
		}
		path := n.Path()
		if first {
			common, first = path, false
			continue
		}
		i := 0
		for i < len(common) && i < len(path) && common[i] == path[i] {
			i++
		}
		common = common[:i]
	}
	extra := make([]string, 0, len(h))
	for _, n := range g.All() {
		if _, ok := h[n.ID]; ok {
			extra = append(extra, n.ID)
		}
	}
	switch {
	case len(common) >= 2 && len(common[1].Children) > 0:
		return build(g, KindComponent, common[1], extra)
	case len(common) >= 1 && len(common[0].Children) > 0:
		return build(g, KindContainer, common[0], extra)
	}
	return build(g, KindContext, nil, extra)
}

// build assembles a view of the given kind over scope (nil for the context).
// Elements outside the scope are drawn as their system, or their container
// when they share the scope's system; extra adds the boxes of these elements
// even when no relationship links them to the scope.
func build(g *graph.Graph, kind string, scope *graph.Node, extra []string) *View {
	v := &View{Kind: kind, members: map[string]string{}}
	var inner []*graph.Node
	switch kind {
	case KindContext:
		v.Title = "System context"
		inner = g.Roots
	case KindContainer:
		v.Title = "Containers of " + scope.Name
		v.ScopeID = scope.ID
		inner = scope.Children
	case KindComponent:
		v.Title = "Components of " + scope.Name
		v.ScopeID = scope.ID
		inner = scope.Children
	}

	// rep is the element n is drawn as, nil when the view has no place for it.
	rep := func(n *graph.Node) *graph.Node {
		path := n.Path()
		root := path[0]
		switch kind {
		case KindContext:
			return root
		case KindContainer:
			if root != scope {
				return root
			}
			if len(path) < 2 {
				return nil
			}
			return path[1]
		default:
			sys := scope.Parent
			if root != sys {
				return root
			}
			if len(path) < 2 {
				return nil
			}
			if path[1] != scope {
				return path[1]
			}
			if len(path) < 3 {
				return nil
			}
			return path[2]
		}
	}

	shown := map[string]bool{}
	add := func(n *graph.Node, external bool) {
		if shown[n.ID] {
			return
		}
		shown[n.ID] = true
		v.Nodes = append(v.Nodes, Node{
			ID: n.ID, Name: n.Name, Level: n.Level, Kind: n.Kind, Tech: n.Tech,
			External: external || n.External, Children: len(n.Children),
		})
	}
	inScope := map[string]bool{}
	for _, n := range inner {
		add(n, false)
		inScope[n.ID] = true
	}
	for _, n := range g.All() {
		if r := rep(n); r != nil {
			v.members[n.ID] = r.ID
		}
	}

	type pair struct{ a, b string }
	index := map[pair]int{}
	for _, n := range g.All() {
		for _, e := range g.Outgoing(n.ID) {
			src, dst := rep(n), rep(e.Other)
			if src == nil || dst == nil || src == dst {
				continue
			}
			// An arrow between two outsiders says nothing about the scope.
			if !inScope[src.ID] && !inScope[dst.ID] {
				continue
			}
			for _, end := range []*graph.Node{src, dst} {
				if !shown[end.ID] {
					add(end, true)
				}
			}
			if i, ok := index[pair{dst.ID, src.ID}]; ok {
				v.Edges[i].Both = true
				v.Edges[i].Count++
				continue
			}
			if i, ok := index[pair{src.ID, dst.ID}]; ok {
				v.Edges[i].Count++
				continue
			}
			index[pair{src.ID, dst.ID}] = len(v.Edges)
			v.Edges = append(v.Edges, Edge{From: src.ID, To: dst.ID, Label: relLabel(e.Rel), Count: 1})
		}
	}
	for _, id := range extra {
		if n := g.Node(id); n != nil {
			if r := rep(n); r != nil && !shown[r.ID] {
				add(r, true)
			}
		}
	}
	return v
}

// relLabel is the words on an arrow: the description when short, else the
// relationship type.
func relLabel(r api.Relationship) string {
	d := strings.Join(strings.Fields(r.Description), " ")
	if d != "" && len([]rune(d)) <= 28 {
		return d
	}
	if r.Type != "" {
		return strings.ReplaceAll(r.Type, "_", " ")
	}
	return d
}
