// Package graph indexes a C4 model for navigation: parents and children,
// relationships in both directions, paths and lookups by name.
package graph

import (
	"sort"
	"strings"

	"github.com/archyl-com/cli/internal/api"
)

// Node is one element of any C4 level.
type Node struct {
	ID          string
	Name        string
	Level       string // system, container, component, code
	Kind        string // the level's own type: software_system, api, service…
	Description string
	FilePath    string
	Signature   string
	External    bool
	Tags        []string
	Tech        []string
	Parent      *Node
	Children    []*Node
}

// Edge is a relationship seen from one end.
type Edge struct {
	Rel   api.Relationship
	Other *Node
}

// Graph is an indexed model.
type Graph struct {
	Roots []*Node
	nodes map[string]*Node
	order []*Node
	out   map[string][]Edge
	in    map[string][]Edge
}

// New indexes a model.
func New(m *api.Model) *Graph {
	g := &Graph{nodes: map[string]*Node{}, out: map[string][]Edge{}, in: map[string][]Edge{}}
	for _, s := range m.Systems {
		sys := g.add(&Node{ID: s.ID, Name: s.Name, Level: "system", Kind: s.Type, Description: s.Description,
			External: s.IsExternal, Tags: s.Tags, Tech: techNames(s.Technologies)}, nil)
		for _, c := range s.Containers {
			ctr := g.add(&Node{ID: c.ID, Name: c.Name, Level: "container", Kind: c.ContainerType,
				Description: c.Description, Tags: c.Tags, Tech: techNames(c.Technologies)}, sys)
			for _, cp := range c.Components {
				comp := g.add(&Node{ID: cp.ID, Name: cp.Name, Level: "component", Kind: cp.ComponentType,
					Description: cp.Description, FilePath: cp.FilePath, Tags: cp.Tags, Tech: techNames(cp.Technologies)}, ctr)
				for _, code := range cp.CodeElements {
					g.add(&Node{ID: code.ID, Name: code.Name, Level: "code", Kind: code.CodeType,
						Description: code.Description, FilePath: code.FilePath, Signature: code.Signature,
						Tags: code.Tags, Tech: techNames(code.Technologies)}, comp)
				}
			}
		}
	}
	for _, r := range m.Relationships {
		src, dst := g.nodes[r.SourceID], g.nodes[r.TargetID]
		if src == nil || dst == nil {
			continue
		}
		g.out[src.ID] = append(g.out[src.ID], Edge{Rel: r, Other: dst})
		g.in[dst.ID] = append(g.in[dst.ID], Edge{Rel: r, Other: src})
	}
	return g
}

func (g *Graph) add(n *Node, parent *Node) *Node {
	n.Parent = parent
	if parent == nil {
		g.Roots = append(g.Roots, n)
	} else {
		parent.Children = append(parent.Children, n)
	}
	g.nodes[n.ID] = n
	g.order = append(g.order, n)
	return n
}

func techNames(techs []api.Technology) []string {
	names := make([]string, 0, len(techs))
	for _, t := range techs {
		if t.Name != "" {
			names = append(names, t.Name)
		}
	}
	return names
}

// Node returns an element by ID.
func (g *Graph) Node(id string) *Node {
	return g.nodes[id]
}

// All returns every element in model order.
func (g *Graph) All() []*Node {
	return g.order
}

// Outgoing lists the relationships an element starts.
func (g *Graph) Outgoing(id string) []Edge {
	return g.out[id]
}

// Incoming lists the relationships pointing at an element.
func (g *Graph) Incoming(id string) []Edge {
	return g.in[id]
}

// Path is the chain from the root system down to n.
func (n *Node) Path() []*Node {
	var chain []*Node
	for cur := n; cur != nil; cur = cur.Parent {
		chain = append([]*Node{cur}, chain...)
	}
	return chain
}

// PathString renders the path as "Billing / API / Invoicing".
func (n *Node) PathString() string {
	parts := make([]string, 0, 4)
	for _, p := range n.Path() {
		parts = append(parts, p.Name)
	}
	return strings.Join(parts, " / ")
}

// Find resolves a reference: an ID, an exact name, then a name containing
// it. Less specific levels win ties, then shorter names.
func (g *Graph) Find(ref string) (*Node, []*Node) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if n := g.nodes[ref]; n != nil {
		return n, nil
	}
	lower := strings.ToLower(ref)
	var exact, partial []*Node
	for _, n := range g.order {
		name := strings.ToLower(n.Name)
		switch {
		case name == lower:
			exact = append(exact, n)
		case strings.Contains(name, lower):
			partial = append(partial, n)
		}
	}
	candidates := exact
	if len(candidates) == 0 {
		candidates = partial
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ri, rj := LevelRank(candidates[i].Level), LevelRank(candidates[j].Level)
		if ri != rj {
			return ri < rj
		}
		return len(candidates[i].Name) < len(candidates[j].Name)
	})
	return candidates[0], candidates[1:]
}

// Dependents walks incoming relationships up to depth hops.
func (g *Graph) Dependents(id string, depth int) []Hop {
	seen := map[string]bool{id: true}
	var out []Hop
	frontier := []string{id}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, cur := range frontier {
			for _, e := range g.in[cur] {
				if seen[e.Other.ID] {
					continue
				}
				seen[e.Other.ID] = true
				out = append(out, Hop{Node: e.Other, Distance: d, Via: e.Rel.Type})
				next = append(next, e.Other.ID)
			}
		}
		frontier = next
	}
	return out
}

// Hop is an element reached by walking relationships.
type Hop struct {
	Node     *Node
	Distance int
	Via      string
}

// Count tallies elements per level.
func (g *Graph) Count() map[string]int {
	counts := map[string]int{}
	for _, n := range g.order {
		counts[n.Level]++
	}
	return counts
}

// LevelRank orders levels from least to most specific.
func LevelRank(level string) int {
	switch level {
	case "system":
		return 0
	case "container":
		return 1
	case "component":
		return 2
	}
	return 3
}

// SelectType is the web app's name for a level in deep links.
func SelectType(level string) string {
	if level == "code" {
		return "code_element"
	}
	return level
}
