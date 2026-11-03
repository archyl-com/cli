package incident

import (
	"fmt"
	"strings"

	"github.com/archyl-com/cli/internal/ui"
)

// Render prints the report for a person at a terminal. diagram, when not
// empty, is drawn under the blast radius.
func Render(p *ui.Printer, r *Report, diagram string) {
	renderHeader(p, r)
	renderBlastRadius(p, r)
	if diagram != "" {
		p.Println()
		p.Println(diagram)
	}
	renderChanges(p, r)
	renderKnowledge(p, r)
	renderNotify(p, r)
	p.Println()
}

func renderHeader(p *ui.Printer, r *Report) {
	e := r.Element
	p.Println()
	p.Println(p.Brand() + p.Muted("  incident"))
	p.Println()
	p.Println("  " + p.Element(e.Level, e.Name) + "  " + p.Level(e.Type()) + p.Muted("  "+e.Path))
	var facts [][2]string
	if len(e.Technologies) > 0 {
		facts = append(facts, [2]string{"tech", strings.Join(e.Technologies, p.Muted(" · "))})
	}
	facts = append(facts, [2]string{"owners", ownersTerminal(p, r.Owners, r.OwnersFrom, r.OwnersUnknown)})
	if e.URL != "" {
		facts = append(facts, [2]string{"open", p.Muted(e.URL)})
	}
	p.Println()
	p.Println(indent(p.KeyValues(facts), 2))
}

func ownersTerminal(p *ui.Printer, o Owners, from string, unknown bool) string {
	if o.Empty() {
		if unknown {
			return p.Warning("unavailable")
		}
		return p.Warning("none set")
	}
	names := make([]string, 0, len(o.Teams)+len(o.People))
	for _, t := range o.Teams {
		names = append(names, p.Accent(t.Label()))
	}
	for _, person := range o.People {
		names = append(names, firstNonEmpty(person.Name, person.Email))
	}
	text := strings.Join(names, p.Muted(", "))
	if from != "" {
		text += p.Muted("  from " + from)
	}
	return text
}

func renderBlastRadius(p *ui.Printer, r *Report) {
	p.Println()
	if r.DependentCount() == 0 {
		p.Println(p.Heading(" Blast radius"))
		p.Println("  " + p.Success("Nothing depends on it in the model."))
	} else {
		p.Println(p.Heading(" Blast radius") + "  " + p.Warning(dependsCount(r.DependentCount())) +
			p.Muted(" on it within "+hops(r.Depth)))
	}
	for _, group := range ByDistance(r.Dependents) {
		p.Println("  " + p.Muted(hops(group[0].Distance)))
		for _, d := range group {
			p.Println("    " + impactedTerminal(p, d))
		}
	}
	if r.MoreDependents > 0 {
		p.Println("    " + p.Muted(fmt.Sprintf("and %d more", r.MoreDependents)))
	}
	p.Println()
	p.Println(p.Heading(" Depends on"))
	if len(r.Dependencies) == 0 {
		p.Println("  " + p.Muted("Nothing in the model."))
	}
	for _, d := range r.Dependencies {
		p.Println("  " + p.Muted("→ ") + impactedTerminal(p, d))
	}
	if r.MoreDependencies > 0 {
		p.Println("    " + p.Muted(fmt.Sprintf("and %d more", r.MoreDependencies)))
	}
}

func impactedTerminal(p *ui.Printer, d Impacted) string {
	line := p.Element(d.Level, d.Name)
	if d.Via != "" {
		line += "  " + p.Muted(verb(d.Via))
	}
	return line + "  " + ownersTerminal(p, d.Owners, d.OwnersFrom, d.OwnersUnknown)
}

func renderChanges(p *ui.Printer, r *Report) {
	p.Println()
	p.Println(p.Heading(" Recent changes"))
	if len(r.Changes) == 0 {
		p.Println("  " + unavailableOr(p, r.IsUnavailable(SectionChanges), "No recent change to it or its dependencies."))
	}
	for _, c := range r.Changes {
		p.Println(fmt.Sprintf("  %s  %s", p.Muted(fmt.Sprintf("%-11s", Ago(c.At, r.GeneratedAt))), changeText(c)))
	}
	title := "Latest releases in the project"
	if r.ReleasesOf != "" {
		title = "Releases of " + r.ReleasesOf
	}
	p.Println()
	p.Println(p.Heading(" " + title))
	if len(r.Releases) == 0 {
		p.Println("  " + unavailableOr(p, r.IsUnavailable(SectionReleases), "No release recorded."))
	}
	for _, rel := range r.Releases {
		version := p.Bold(rel.Version)
		if rel.Production {
			version = p.Warning(rel.Version)
		}
		line := fmt.Sprintf("  %s  %s", p.Muted(fmt.Sprintf("%-11s", Ago(rel.At, r.GeneratedAt))), version)
		if rest := strings.TrimPrefix(releaseText(rel), rel.Version); rest != "" {
			line += p.Muted(rest)
		}
		p.Println(line)
	}
}

func renderKnowledge(p *ui.Printer, r *Report) {
	p.Println()
	p.Println(p.Heading(" Runbooks and knowledge"))
	if len(r.Docs)+len(r.Decisions)+len(r.Notes) == 0 && len(r.Unavailable) == 0 {
		p.Println("  " + p.Muted("No documentation, decision or note is linked to it."))
	}
	for _, d := range r.Docs {
		line := "  " + p.Accent("doc") + "  " + d.Title
		if d.Via != "" {
			line += p.Muted("  on " + d.Via)
		}
		p.Println(line)
		if d.URL != "" {
			p.Println("       " + p.Muted(d.URL))
		}
	}
	for _, d := range r.Decisions {
		p.Println(fmt.Sprintf("  %s  %s  %s", p.Accent(fmt.Sprintf("ADR-%d", d.Number)), d.Title, p.Muted(d.Status)))
	}
	for _, n := range r.Notes {
		kind := p.Muted(n.Kind)
		if n.Kind == "pitfall" {
			kind = p.Warning(n.Kind)
		}
		p.Println("  " + kind + "  " + oneLine(n.Text))
	}
	if r.IsUnavailable(SectionDocs) {
		p.Println("  " + p.Muted("documentation ") + p.Warning("unavailable"))
	}
	if r.IsUnavailable(SectionNotes) {
		p.Println("  " + p.Muted("notes ") + p.Warning("unavailable"))
	}
}

func renderNotify(p *ui.Printer, r *Report) {
	p.Println()
	p.Println(p.Heading(" Who to tell"))
	if r.Notify.Empty() && !r.IsUnavailable(SectionOwners) {
		p.Println("  " + p.Muted("No owner is set on these elements."))
	}
	for _, t := range r.Notify.Teams {
		p.Println("  " + p.Accent(t.Label()))
	}
	for _, person := range r.Notify.People {
		line := "  " + firstNonEmpty(person.Name, person.Email)
		if person.Name != "" && person.Email != "" {
			line += "  " + p.Muted(person.Email)
		}
		p.Println(line)
	}
	for _, caveat := range NotifyCaveats(r) {
		p.Println("  " + p.Warning(caveat))
	}
}

func unavailableOr(p *ui.Printer, unavailable bool, none string) string {
	if unavailable {
		return p.Warning("Unavailable.")
	}
	return p.Muted(none)
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}
