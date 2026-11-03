package prcomment

import (
	"fmt"
	"strings"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
)

// maxDiagramNodes keeps the diagram readable, and the comment under the
// providers' size limits.
const maxDiagramNodes = 30

// collapseAbove folds a diagram with more nodes than this into <details>.
const collapseAbove = 8

// Diagram is a change's blast radius drawn as a Mermaid flowchart.
type Diagram struct {
	Source string `json:"source"`
	Nodes  int    `json:"nodes"`
	// Omitted counts the dependents left out past maxDiagramNodes.
	Omitted int `json:"omitted,omitempty"`
}

// Mermaid draws the touched elements, highlighted, and what depends on them,
// dimmer the further away. Arrows follow the relationships, from the
// dependent to what it uses; a dotted one stands for a path whose middle is
// unknown. It works from the impact alone, without the model, and returns nil
// when nothing was touched.
func Mermaid(impact *api.BlastRadius) *Diagram {
	if impact == nil || len(impact.Elements) == 0 {
		return nil
	}
	var (
		nodes   []diagram.Node
		edges   []diagram.Edge
		hl      = diagram.Highlight{}
		seen    = map[diagram.Edge]bool{}
		omitted = map[string]bool{}
	)
	add := func(e api.Element, distance int) string {
		id := e.ID
		if id == "" {
			id = "name:" + e.Name
		}
		if d, ok := hl[id]; ok {
			hl[id] = min(d, distance)
			return id
		}
		if len(nodes) == maxDiagramNodes {
			omitted[id] = true
			return ""
		}
		nodes = append(nodes, diagram.Node{ID: id, Name: e.Name, Level: e.Type})
		hl[id] = distance
		return id
	}
	link := func(from, to, label string, indirect bool) {
		if from == "" || to == "" || from == to {
			return
		}
		e := diagram.Edge{From: from, To: to, Label: strings.ReplaceAll(label, "_", " "), Count: 1, Indirect: indirect}
		if !seen[e] {
			seen[e] = true
			edges = append(edges, e)
		}
	}

	touchedIDs := make([]string, len(impact.Elements))
	for i, t := range impact.Elements {
		touchedIDs[i] = add(t.Element, 0)
	}
	for i, t := range impact.Elements {
		touched := touchedIDs[i]
		// Names reached so far from this element, for resolving each hop's
		// target, which the API gives by name.
		reached := map[string]string{t.Name: touched}
		for _, d := range t.Dependents {
			n := add(d.Element, d.Distance)
			if n == "" {
				continue
			}
			if d.Distance <= 1 {
				link(n, touched, d.RelationshipType, false)
			} else if target := reached[hopTarget(d)]; target != "" {
				link(n, target, d.RelationshipType, false)
			} else {
				link(n, touched, "", true)
			}
			if _, ok := reached[d.Name]; !ok {
				reached[d.Name] = n
			}
		}
	}

	view := diagram.NewView("impact", "Impact", nodes, edges)
	source := diagram.MermaidWith(view, hl, diagram.MermaidOptions{Direction: "LR"})
	return &Diagram{Source: source, Nodes: len(nodes), Omitted: len(omitted)}
}

// hopTarget is the element a dependent reaches the change through, read from
// "Source uses Target".
func hopTarget(d api.ImpactedElement) string {
	if d.RelationshipType == "" {
		return ""
	}
	_, target, ok := strings.Cut(d.Via, " "+d.RelationshipType+" ")
	if !ok {
		return ""
	}
	return target
}

// Markdown embeds the diagram in a comment, folded when it is large.
func (d *Diagram) Markdown() string {
	if d == nil {
		return ""
	}
	fence := "```mermaid\n" + d.Source + "```\n"
	note := ""
	if d.Omitted > 0 {
		note = fmt.Sprintf("\n+%d more not drawn.\n", d.Omitted)
	}
	if d.Nodes <= collapseAbove {
		return "**Impact diagram**\n\n" + fence + note
	}
	return fmt.Sprintf("<details>\n<summary><b>Impact diagram</b> (%d elements)</summary>\n\n%s%s\n</details>\n", d.Nodes, fence, note)
}
