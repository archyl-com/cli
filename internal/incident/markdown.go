package incident

import (
	"fmt"
	"strings"
)

// Markdown renders the report as compact Markdown, for a pipe, a coding
// agent or an incident document.
func Markdown(r *Report) string {
	var b strings.Builder
	e := r.Element
	b.WriteString("# Incident: " + e.Name + "\n\n")
	b.WriteString(elementLine(e) + "\n\n")
	b.WriteString("- **Owners:** " + ownersText(r.Owners, r.OwnersFrom, r.OwnersUnknown) + "\n")
	if e.URL != "" {
		b.WriteString("- **Open:** " + e.URL + "\n")
	}

	b.WriteString("\n## Blast radius\n\n")
	writeImpactedMarkdown(&b, r)

	b.WriteString("\n## Recent changes\n\n")
	writeChangesMarkdown(&b, r)

	b.WriteString("\n## Runbooks and knowledge\n\n")
	writeKnowledgeMarkdown(&b, r)

	b.WriteString("\n## Who to tell\n\n")
	if r.Notify.Empty() && !r.IsUnavailable(SectionOwners) {
		b.WriteString("No owner is set on these elements.\n")
	}
	for _, name := range r.Notify.Labels() {
		b.WriteString("- " + name + "\n")
	}
	for i, caveat := range NotifyCaveats(r) {
		if i > 0 || !r.Notify.Empty() {
			b.WriteString("\n")
		}
		b.WriteString("_" + caveat + "_\n")
	}
	return b.String()
}

// NotifyCaveats say what "Who to tell" may be missing: owners that could
// not be fetched, and dependents beyond MaxNotified.
func NotifyCaveats(r *Report) []string {
	var out []string
	if r.IsUnavailable(SectionOwners) {
		out = append(out, "Incomplete: some owners could not be fetched.")
	}
	if r.NotifyUnlisted > 0 {
		out = append(out, fmt.Sprintf("Plus %s whose owners are not listed.", moreDependents(r.NotifyUnlisted)))
	}
	return out
}

// elementLine is the element's type, path and technologies on one line.
func elementLine(e Element) string {
	parts := []string{e.Type(), e.Path}
	if len(e.Technologies) > 0 {
		parts = append(parts, strings.Join(e.Technologies, ", "))
	}
	return strings.Join(parts, " · ")
}

func ownersText(o Owners, from string, unknown bool) string {
	if o.Empty() {
		return emptyText(unknown, "none set")
	}
	text := strings.Join(o.Labels(), ", ")
	if from != "" {
		text += " (from " + from + ")"
	}
	return text
}

// emptyText says why a section is empty: its data could not be fetched, or
// there is none.
func emptyText(unavailable bool, none string) string {
	if unavailable {
		return "_Unavailable._"
	}
	return none
}

func writeImpactedMarkdown(b *strings.Builder, r *Report) {
	if r.DependentCount() == 0 {
		b.WriteString("Nothing depends on it in the model.\n")
	} else {
		fmt.Fprintf(b, "**%s on it within %s.**\n\n", dependsCount(r.DependentCount()), hops(r.Depth))
	}
	for _, group := range ByDistance(r.Dependents) {
		fmt.Fprintf(b, "%s away:\n\n", hops(group[0].Distance))
		for _, d := range group {
			b.WriteString("- " + impactedMarkdown(d) + "\n")
		}
		b.WriteString("\n")
	}
	if r.MoreDependents > 0 {
		fmt.Fprintf(b, "And %d more.\n\n", r.MoreDependents)
	}
	if len(r.Dependencies) == 0 {
		b.WriteString("**It depends on:** nothing in the model.\n")
		return
	}
	b.WriteString("**It depends on:**\n\n")
	for _, d := range r.Dependencies {
		b.WriteString("- " + impactedMarkdown(d) + "\n")
	}
	if r.MoreDependencies > 0 {
		fmt.Fprintf(b, "- and %d more\n", r.MoreDependencies)
	}
}

func impactedMarkdown(d Impacted) string {
	line := d.Name + " (" + d.Type() + ")"
	if d.Via != "" {
		line += ", " + verb(d.Via)
	}
	return line + " · " + ownersText(d.Owners, d.OwnersFrom, d.OwnersUnknown)
}

func writeChangesMarkdown(b *strings.Builder, r *Report) {
	switch {
	case len(r.Changes) > 0:
		for _, c := range r.Changes {
			b.WriteString("- " + Ago(c.At, r.GeneratedAt) + " · " + changeText(c) + "\n")
		}
	default:
		b.WriteString(emptyText(r.IsUnavailable(SectionChanges), "No recent change to it or its dependencies in the model.") + "\n")
	}
	b.WriteString("\n" + releasesTitle(r) + "\n\n")
	if len(r.Releases) == 0 {
		b.WriteString(emptyText(r.IsUnavailable(SectionReleases), "No release recorded.") + "\n")
	}
	for _, rel := range r.Releases {
		line := "- " + releaseText(rel) + " · " + Ago(rel.At, r.GeneratedAt)
		if rel.URL != "" {
			line += " · " + rel.URL
		}
		b.WriteString(line + "\n")
	}
}

func releasesTitle(r *Report) string {
	if r.ReleasesOf != "" {
		return "**Releases of " + r.ReleasesOf + ":**"
	}
	return "**Latest releases in the project:**"
}

func writeKnowledgeMarkdown(b *strings.Builder, r *Report) {
	if len(r.Docs)+len(r.Decisions)+len(r.Notes) == 0 {
		none := "No documentation, decision or note is linked to it."
		if r.IsUnavailable(SectionDocs) || r.IsUnavailable(SectionNotes) {
			none += " Some sources were unavailable."
		}
		b.WriteString(none + "\n")
		return
	}
	for _, d := range r.Docs {
		line := "- " + mdLink(d.Title, d.URL)
		if d.Via != "" {
			line += " (on " + d.Via + ")"
		}
		b.WriteString(line + "\n")
	}
	for _, d := range r.Decisions {
		line := "- " + mdLink(fmt.Sprintf("ADR-%d %s", d.Number, d.Title), d.URL)
		if d.Status != "" {
			line += " (" + d.Status + ")"
		}
		b.WriteString(line + "\n")
	}
	for _, n := range r.Notes {
		b.WriteString("- " + n.Kind + ": " + oneLine(n.Text) + "\n")
	}
	for _, s := range []string{SectionDocs, SectionNotes} {
		if r.IsUnavailable(s) {
			b.WriteString("- " + s + ": _unavailable_\n")
		}
	}
}

func mdLink(text, url string) string {
	if url == "" {
		return text
	}
	return "[" + strings.NewReplacer("[", `\[`, "]", `\]`).Replace(text) + "](" + url + ")"
}

// ByDistance groups impacted elements by their distance, closest first. The
// input is expected sorted by distance, as a plan's lists are.
func ByDistance(list []Impacted) [][]Impacted {
	var out [][]Impacted
	for _, d := range list {
		if n := len(out); n > 0 && out[n-1][0].Distance == d.Distance {
			out[n-1] = append(out[n-1], d)
			continue
		}
		out = append(out, []Impacted{d})
	}
	return out
}

func changeText(c Change) string {
	who := firstNonEmpty(c.Who, "Someone")
	text := who + " " + c.Action + " " + c.Element
	if c.Type != "" {
		text += " (" + c.Type + ")"
	}
	return text
}

func releaseText(r Release) string {
	text := r.Version
	if r.Status != "" {
		text += " " + r.Status
	}
	if r.Environment != "" {
		text += " in " + r.Environment
	}
	return text
}

// verb turns a relationship type ("reads_from") into words.
func verb(relationshipType string) string {
	return strings.ReplaceAll(relationshipType, "_", " ")
}

func hops(n int) string {
	if n == 1 {
		return "1 hop"
	}
	return fmt.Sprintf("%d hops", n)
}

func dependsCount(n int) string {
	if n == 1 {
		return "1 element depends"
	}
	return fmt.Sprintf("%d elements depend", n)
}

func moreDependents(n int) string {
	if n == 1 {
		return "1 more dependent"
	}
	return fmt.Sprintf("%d more dependents", n)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
