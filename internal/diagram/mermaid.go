package diagram

import (
	"fmt"
	"strings"
)

// MermaidOptions shapes a Mermaid flowchart.
type MermaidOptions struct {
	// Direction is the flowchart's: TB (the default), LR, BT or RL.
	Direction string
}

// Mermaid renders a view as a C4-flavoured Mermaid flowchart, top to bottom,
// for pull request comments and Markdown. Elements of h are classed
// "touched" (distance 0), "impacted" (one hop) or "distant" (further), the
// rest "muted"; without a highlight every element keeps its level's colour.
// Indirect arrows are dotted. A view marked with changes classes its boxes
// "added", "removed" or "changed", prefixes their names with the change's
// sign, dots removed arrows and colours changed ones. The output has no
// fences.
func Mermaid(v *View, h Highlight) string {
	return MermaidWith(v, h, MermaidOptions{})
}

// MermaidWith is Mermaid with options.
func MermaidWith(v *View, h Highlight, opts MermaidOptions) string {
	lifted := v.Lift(h)
	direction := opts.Direction
	if direction == "" {
		direction = "TB"
	}
	ids := make(map[string]string, len(v.Nodes))
	classes := map[string][]string{}
	var used []string
	for i, n := range v.Nodes {
		ids[n.ID] = fmt.Sprintf("n%d", i+1)
		class := mermaidClass(n, lifted)
		if _, seen := classes[class]; !seen {
			used = append(used, class)
		}
		classes[class] = append(classes[class], ids[n.ID])
	}

	var b strings.Builder
	b.WriteString("flowchart " + direction + "\n")
	for _, class := range mermaidClassOrder {
		if _, ok := classes[class]; ok {
			b.WriteString("  classDef " + class + " " + mermaidClassDefs[class] + "\n")
		}
	}
	for _, n := range v.Nodes {
		name := mermaidText(n.Name)
		if badge := n.Change.Badge(); badge != "" {
			name = badge + " " + name
		}
		label := "<b>" + name + "</b><br/>" + mermaidText(boxSubtitle(n))
		open, closing := "[\"", "\"]"
		if strings.Contains(strings.ToLower(n.Kind), "database") || strings.Contains(strings.ToLower(n.Kind), "datastore") {
			open, closing = "[(\"", "\")]"
		}
		fmt.Fprintf(&b, "  %s%s%s%s\n", ids[n.ID], open, label, closing)
	}
	for _, e := range v.Edges {
		arrow := "-->"
		switch {
		case e.Indirect, e.Change == Removed:
			arrow = "-.->"
		case e.Both:
			arrow = "<-->"
		}
		label := e.Label
		if e.Count > 1 && !e.Both {
			label += fmt.Sprintf(" ×%d", e.Count)
		}
		if label != "" {
			fmt.Fprintf(&b, "  %s %s|\"%s\"| %s\n", ids[e.From], arrow, mermaidText(label), ids[e.To])
		} else {
			fmt.Fprintf(&b, "  %s %s %s\n", ids[e.From], arrow, ids[e.To])
		}
	}

	for _, class := range used {
		fmt.Fprintf(&b, "  class %s %s\n", strings.Join(classes[class], ","), class)
	}
	for i, e := range v.Edges {
		if style, ok := mermaidLinkStyles[e.Change]; ok {
			fmt.Fprintf(&b, "  linkStyle %d %s\n", i, style)
		}
	}
	if len(lifted) > 0 {
		// Accent the arrows into what the change touches.
		for i, e := range v.Edges {
			_, from := lifted[e.From]
			if d, ok := lifted[e.To]; from && ok && d == 0 && !e.Indirect {
				fmt.Fprintf(&b, "  linkStyle %d stroke:#4f46e5,stroke-width:2px\n", i)
			}
		}
	}
	return b.String()
}

// Mermaid classes, in the order their definitions are written.
var (
	mermaidClassOrder = []string{"system", "container", "component", "code", "external", "touched", "impacted", "distant", "muted", "added", "changed", "removed"}
	mermaidClassDefs  = map[string]string{
		"system":    "fill:#f5f3ff,stroke:#7c3aed,color:#18181b",
		"container": "fill:#f0f9ff,stroke:#0284c7,color:#18181b",
		"component": "fill:#f0fdfa,stroke:#0d9488,color:#18181b",
		"code":      "fill:#fff7ed,stroke:#c2410c,color:#18181b",
		"external":  "fill:#fafafa,stroke:#71717a,color:#3f3f46,stroke-dasharray:5 4",
		"touched":   "fill:#eef2ff,stroke:#4f46e5,stroke-width:3px,color:#18181b",
		"impacted":  "fill:#f5f5ff,stroke:#818cf8,stroke-width:2px,color:#18181b",
		"distant":   "fill:#fafafa,stroke:#a5b4fc,color:#52525b,stroke-dasharray:4 3",
		"muted":     "fill:#fafafa,stroke:#d4d4d8,color:#a1a1aa",
		"added":     "fill:#f0fdf4,stroke:#16a34a,stroke-width:3px,color:#18181b",
		"changed":   "fill:#fffbeb,stroke:#d97706,stroke-width:3px,color:#18181b",
		"removed":   "fill:#fafafa,stroke:#a1a1aa,color:#a1a1aa,stroke-dasharray:5 4",
	}
	mermaidLinkStyles = map[Change]string{
		Added:   "stroke:#16a34a,stroke-width:2px",
		Changed: "stroke:#d97706,stroke-width:2px",
		Removed: "stroke:#a1a1aa",
	}
)

// mermaidClass is a node's class: its change, else its highlight when there
// is one, else its level, or external.
func mermaidClass(n Node, lifted Highlight) string {
	if n.Change != "" {
		return string(n.Change)
	}
	if len(lifted) > 0 {
		d, ok := lifted[n.ID]
		switch {
		case !ok:
			return "muted"
		case d == 0:
			return "touched"
		case d == 1:
			return "impacted"
		}
		return "distant"
	}
	if n.External {
		return "external"
	}
	if _, ok := mermaidClassDefs[n.Level]; ok {
		return n.Level
	}
	return "container"
}

// mermaidText makes text safe inside a quoted Mermaid label: quotes, pipes,
// backticks and angle brackets as entities, on one line.
func mermaidText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.NewReplacer(
		`"`, "#quot;",
		"<", "#lt;",
		">", "#gt;",
		"|", "#124;",
		"`", "#96;",
		"#", "#35;",
	).Replace(s)
}
