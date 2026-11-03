package history

import (
	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
)

// Scene is both sides of a diff in one model: the model after the change,
// with what was removed put back where it was, each element and
// relationship tagged with its change.
type Scene struct {
	Model    *api.Model
	Graph    *graph.Graph
	Elements map[string]diagram.Change
	Links    []diagram.Link
}

// Scope picks the view of a scene to draw.
type Scope struct {
	// ElementID centres the view on an element; empty draws the whole.
	ElementID string
	// Inside draws the view inside the element instead: a system's
	// containers, a container's components.
	Inside bool
}

// tree is an element of the scene being assembled.
type tree struct {
	n    *graph.Node
	id   string
	kids []*tree
}

// Scene merges both sides of the diff.
func (d *Diff) Scene() *Scene {
	s := &Scene{Model: &api.Model{}, Elements: map[string]diagram.Change{}}
	index := map[string]*tree{}
	var roots []*tree
	var grow func(n *graph.Node) *tree
	grow = func(n *graph.Node) *tree {
		t := &tree{n: n, id: n.ID}
		index[t.id] = t
		for _, c := range n.Children {
			t.kids = append(t.kids, grow(c))
		}
		return t
	}
	for _, b := range d.after.Roots {
		roots = append(roots, grow(b))
	}
	modified := map[string]bool{}
	for _, e := range d.Elements {
		if e.Status == Modified {
			modified[e.ID] = true
		}
	}
	for _, b := range d.after.All() {
		switch {
		case d.back[b.ID] == nil:
			s.Elements[b.ID] = diagram.Added
		case modified[b.ID]:
			s.Elements[b.ID] = diagram.Changed
		}
	}
	// Removed elements go back under their parent: the match of the one
	// they had, or the parent removed with them, placed first.
	for _, a := range d.before.All() {
		if d.forward[a.ID] != nil {
			continue
		}
		t := &tree{n: a, id: d.merged[a.ID]}
		index[t.id] = t
		s.Elements[t.id] = diagram.Removed
		if a.Parent == nil {
			roots = append(roots, t)
			continue
		}
		parent := index[d.mergedID(a.Parent.ID)]
		parent.kids = append(parent.kids, t)
	}
	for _, t := range roots {
		s.Model.Systems = append(s.Model.Systems, toSystem(t))
	}

	for _, r := range d.kept {
		s.Model.Relationships = append(s.Model.Relationships, r)
		s.Links = append(s.Links, diagram.Link{From: r.SourceID, To: r.TargetID, Change: linkChange(d.relStatus[r.ID])})
	}
	for _, r := range d.gone {
		r.SourceID, r.TargetID = d.merged[r.SourceID], d.merged[r.TargetID]
		s.Model.Relationships = append(s.Model.Relationships, r)
		s.Links = append(s.Links, diagram.Link{From: r.SourceID, To: r.TargetID, Change: diagram.Removed})
	}
	s.Graph = graph.New(s.Model)
	return s
}

func linkChange(s Status) diagram.Change {
	switch s {
	case Added:
		return diagram.Added
	case Modified:
		return diagram.Changed
	}
	return ""
}

// View draws the scope of the scene with its changes marked. Outside a
// focus, every change shows: a changed element no arrow links to the view
// gets a box all the same. An element the scene lacks draws the context.
func (s *Scene) View(scope Scope) *diagram.View {
	changed := make([]string, 0, len(s.Elements))
	for _, n := range s.Graph.All() {
		if s.Elements[n.ID] != "" {
			changed = append(changed, n.ID)
		}
	}
	var v *diagram.View
	switch n := s.Graph.Node(scope.ElementID); {
	case n == nil:
		v = diagram.ForIncluding(s.Graph, nil, changed)
	case scope.Inside:
		v = diagram.ForIncluding(s.Graph, n, changed)
	default:
		v = diagram.Focus(s.Graph, n, 1)
	}
	v.MarkChanges(s.Elements, s.Links)
	return v
}

// DefaultScope is the view a history opens on, as the web app's time-lapse
// does: the containers when they all sit in one system, else the system
// context.
func DefaultScope(g *graph.Graph) Scope {
	var owner *graph.Node
	for _, n := range g.Roots {
		if len(n.Children) == 0 {
			continue
		}
		if owner != nil {
			return Scope{}
		}
		owner = n
	}
	if owner == nil {
		return Scope{}
	}
	return Scope{ElementID: owner.ID, Inside: true}
}

func toSystem(t *tree) api.System {
	n := t.n
	s := api.System{ID: t.id, Name: n.Name, Description: n.Description, Type: n.Kind, IsExternal: n.External,
		Tags: n.Tags, Technologies: technologies(n.Tech)}
	for _, k := range t.kids {
		s.Containers = append(s.Containers, toContainer(k))
	}
	return s
}

func toContainer(t *tree) api.Container {
	n := t.n
	c := api.Container{ID: t.id, Name: n.Name, Description: n.Description, ContainerType: n.Kind,
		Tags: n.Tags, Technologies: technologies(n.Tech)}
	for _, k := range t.kids {
		c.Components = append(c.Components, toComponent(k))
	}
	return c
}

func toComponent(t *tree) api.Component {
	n := t.n
	c := api.Component{ID: t.id, Name: n.Name, Description: n.Description, ComponentType: n.Kind,
		FilePath: n.FilePath, Tags: n.Tags, Technologies: technologies(n.Tech)}
	for _, k := range t.kids {
		code := k.n
		c.CodeElements = append(c.CodeElements, api.CodeElement{ID: k.id, Name: code.Name, Description: code.Description,
			CodeType: code.Kind, FilePath: code.FilePath, Signature: code.Signature, Tags: code.Tags,
			Technologies: technologies(code.Tech)})
	}
	return c
}

func technologies(names []string) []api.Technology {
	if len(names) == 0 {
		return nil
	}
	out := make([]api.Technology, len(names))
	for i, name := range names {
		out[i] = api.Technology{Name: name}
	}
	return out
}
