package history

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

// Describe puts the changes of a modification in words: renamed from "A",
// moved from Shop to Billing, type api → service, description changed.
func Describe(changes []FieldChange) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		parts = append(parts, describeField(c))
	}
	return strings.Join(parts, "; ")
}

func describeField(c FieldChange) string {
	switch c.Field {
	case FieldName:
		return "renamed from " + strconv.Quote(c.Before)
	case FieldParent:
		return "moved from " + orTop(c.Before) + " to " + orTop(c.After)
	case FieldDescription:
		return "description changed"
	case FieldExternal:
		if c.After == "yes" {
			return "now external"
		}
		return "no longer external"
	case FieldSource:
		return "now from " + c.After + " (was " + c.Before + ")"
	case FieldTarget:
		return "now to " + c.After + " (was " + c.Before + ")"
	}
	return c.Field + " " + orNone(c.Before) + " → " + orNone(c.After)
}

func orTop(path string) string {
	if path == "" {
		return "the top level"
	}
	return path
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return strings.ReplaceAll(s, "_", " ")
}

// Where places an element: its level, and its parent's path when it has
// one.
func Where(e Element) string {
	if parent := strings.TrimSuffix(e.Path, " / "+e.Name); parent != e.Path {
		return e.Level + " in " + parent
	}
	return e.Level
}

// Folded is the tail of an element added or removed with its children.
func Folded(e Element) string {
	switch e.Descendants {
	case 0:
		return ""
	case 1:
		return "and its child"
	}
	return "and its " + strconv.Itoa(e.Descendants) + " children"
}

// Tally is the summary in words: "3 added, 1 removed, 2 modified;
// relationships: 4 added".
func Tally(s Summary) string {
	counts := func(added, removed, modified int) string {
		var parts []string
		for _, c := range []struct {
			n    int
			word string
		}{{added, "added"}, {removed, "removed"}, {modified, "modified"}} {
			if c.n > 0 {
				parts = append(parts, strconv.Itoa(c.n)+" "+c.word)
			}
		}
		return strings.Join(parts, ", ")
	}
	var out []string
	if e := counts(s.Added, s.Removed, s.Modified); e != "" {
		out = append(out, "elements: "+e)
	}
	if r := counts(s.RelationshipsAdded, s.RelationshipsRemoved, s.RelationshipsModified); r != "" {
		out = append(out, "relationships: "+r)
	}
	if len(out) == 0 {
		return "no changes"
	}
	return strings.Join(out, "; ")
}

// Report is a comparison ready to print.
type Report struct {
	From Resolved `json:"from"`
	To   Resolved `json:"to"`
	// Scope names the element the report is limited to, if any.
	Scope   string        `json:"scope,omitempty"`
	Summary Summary       `json:"summary"`
	Diff    *Diff         `json:"changes"`
	View    *diagram.View `json:"diagram"`
}

// Title is the report's heading: "Architecture changes v1.2.0 → now".
func (r *Report) Title() string {
	title := "Architecture changes"
	if r.Scope != "" {
		title += " in " + r.Scope + ","
	}
	return title + " " + r.From.Label + " → " + r.To.Label
}

// MarkdownOptions shape the Markdown of a report.
type MarkdownOptions struct {
	// Diagram adds a Mermaid diagram with the changes marked.
	Diagram bool
}

// Markdown renders the report for release notes and coding agents. Names
// from the model are stripped of control characters.
func Markdown(r *Report, opts MarkdownOptions) string {
	var b strings.Builder
	clean := ui.StripControl
	fmt.Fprintf(&b, "## %s\n\n", clean(r.Title()))
	fmt.Fprintf(&b, "%s → %s · %s\n", clean(r.From.String()), clean(r.To.String()), Tally(r.Summary))
	if r.Diff.Empty() {
		b.WriteString("\nNo architecture changes.\n")
		return b.String()
	}

	sections := []struct {
		title  string
		status Status
	}{{"Added", Added}, {"Removed", Removed}, {"Modified", Modified}}
	for _, s := range sections {
		var lines []string
		for _, e := range r.Diff.Elements {
			if e.Status != s.status {
				continue
			}
			line := "- **" + clean(e.Name) + "** (" + clean(Where(e)) + ")"
			if f := Folded(e); f != "" {
				line += " " + f
			}
			if len(e.Changes) > 0 {
				line += ": " + clean(Describe(e.Changes))
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", s.title, strings.Join(lines, "\n"))
		}
	}

	var rels []string
	implied := 0
	for _, rel := range r.Diff.Relationships {
		if rel.WithEnd {
			implied++
			continue
		}
		line := "- " + string(rel.Status) + ": " + clean(rel.Source) + " → " + clean(rel.Target)
		if rel.Label != "" {
			line += " (" + clean(rel.Label) + ")"
		}
		if len(rel.Changes) > 0 {
			line += ": " + clean(Describe(rel.Changes))
		}
		rels = append(rels, line)
	}
	if implied > 0 {
		rels = append(rels, "- "+Implied(implied))
	}
	if len(rels) > 0 {
		fmt.Fprintf(&b, "\n### Relationships\n\n%s\n", strings.Join(rels, "\n"))
	}
	if opts.Diagram && r.View != nil && len(r.View.Nodes) > 0 {
		fmt.Fprintf(&b, "\n```mermaid\n%s```\n", diagram.Mermaid(r.View, nil))
	}
	return b.String()
}

// Implied counts the relationships removed with their ends.
func Implied(n int) string {
	if n == 1 {
		return "and 1 relationship removed with the elements above"
	}
	return "and " + strconv.Itoa(n) + " relationships removed with the elements above"
}

// Within keeps the changes of the diff at or inside the element, or around
// it: the element's ancestors added or removed, and the relationships with
// an end inside it. The scene's graph places the elements.
func (d *Diff) Within(g *graph.Graph, id string) *Diff {
	inside := func(elementID string) bool {
		for n := g.Node(elementID); n != nil; n = n.Parent {
			if n.ID == id {
				return true
			}
		}
		return false
	}
	above := func(elementID string) bool {
		n := g.Node(id)
		for p := n; p != nil; p = p.Parent {
			if p.ID == elementID {
				return true
			}
		}
		return false
	}
	out := *d
	out.Elements, out.Relationships = nil, nil
	for _, e := range d.Elements {
		if inside(e.ID) || (e.Status != Modified && above(e.ID)) {
			out.Elements = append(out.Elements, e)
		}
	}
	for _, r := range d.Relationships {
		if inside(r.SourceID) || inside(r.TargetID) {
			out.Relationships = append(out.Relationships, r)
		}
	}
	return &out
}
