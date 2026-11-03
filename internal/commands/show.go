package commands

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

var adrRef = regexp.MustCompile(`(?i)^(?:adr)?[-\s#]?(\d+)$`)

// adrNumber reads "ADR-12", "adr 12", "#12" or "12".
func adrNumber(ref string) (int, bool) {
	m := adrRef.FindStringSubmatch(strings.TrimSpace(ref))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func newShowCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "show <element | ADR-n>",
		Short: "Show an element or a decision",
		Long: `Show an element with its description, technologies, relationships, linked
decisions and blast radius, or render a decision record. Elements are found by
name (exact, then partial) or ID; decisions by number.`,
		Example: `  archyl show "Invoicing Service"
  archyl show ADR-12`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeElements(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			if n, ok := adrNumber(args[0]); ok {
				return showADR(ctx, app, ref.ID, n)
			}
			return showElement(ctx, app, ref.ID, args[0])
		},
	}
}

func showADR(ctx context.Context, app *App, projectID string, number int) error {
	p := app.Printer
	client, err := app.Client()
	if err != nil {
		return err
	}
	var adrs []api.ADR
	if err := p.Spin("Loading decisions…", func() error {
		adrs, err = client.ADRs(ctx, projectID)
		return err
	}); err != nil {
		return err
	}
	for _, adr := range adrs {
		if adr.Number == number {
			if p.JSON {
				return p.WriteJSON(adr)
			}
			renderADR(p, adr)
			return nil
		}
	}
	return fmt.Errorf("no ADR-%d in this project", number)
}

func renderADR(p *ui.Printer, adr api.ADR) {
	var md strings.Builder
	fmt.Fprintf(&md, "# ADR-%d · %s\n\n", adr.Number, adr.Title)
	meta := "**" + strings.ToUpper(adr.Status) + "**"
	if !adr.Date.IsZero() {
		meta += " · " + adr.Date.Format("2 January 2006")
	}
	md.WriteString(meta + "\n\n")
	for _, section := range []struct{ title, body string }{
		{"Context", adr.Context}, {"Decision", adr.Decision}, {"Consequences", adr.Consequences},
	} {
		if strings.TrimSpace(section.body) != "" {
			fmt.Fprintf(&md, "## %s\n\n%s\n\n", section.title, section.body)
		}
	}
	if len(adr.Links) > 0 {
		var names []string
		for _, l := range adr.Links {
			names = append(names, "`"+firstNonEmpty(l.ElementName, l.ElementID)+"`")
		}
		md.WriteString("**Applies to:** " + strings.Join(names, ", ") + "\n")
	}
	if !p.Interactive() {
		p.Println(md.String())
		return
	}
	p.Println(tui.Markdown(md.String(), 100, p.Dark()))
	p.Println()
	p.Println("  " + p.Hint("Open it in the web app:", fmt.Sprintf("archyl open ADR-%d", adr.Number)))
}

func showElement(ctx context.Context, app *App, projectID, ref string) error {
	p := app.Printer
	snap, err := loadModel(ctx, app, projectID)
	if err != nil {
		return err
	}
	adrs := snap.ADRs
	g := graph.New(&snap.Model)
	n, others := g.Find(ref)
	if n == nil {
		return fmt.Errorf("no element matches %q", ref)
	}

	var linked []api.ADR
	for _, adr := range adrs {
		for _, l := range adr.Links {
			if l.ElementID == n.ID {
				linked = append(linked, adr)
				break
			}
		}
	}

	if p.JSON {
		return p.WriteJSON(elementJSON(g, n, linked))
	}
	renderElement(p, g, n, linked)
	if len(others) > 0 {
		var names []string
		for i, o := range others {
			if i == 3 {
				names = append(names, fmt.Sprintf("+%d", len(others)-3))
				break
			}
			names = append(names, p.Element(o.Level, o.Name))
		}
		p.Println("  " + p.Muted("Also matching: ") + strings.Join(names, p.Muted(", ")))
	}
	return nil
}

func renderElement(p *ui.Printer, g *graph.Graph, n *graph.Node, adrs []api.ADR) {
	p.Println()
	p.Println("  " + breadcrumb(p, n.PathString()))
	p.Println()
	kind := n.Level
	if n.Kind != "" {
		kind = strings.ReplaceAll(n.Kind, "_", " ")
	}
	p.Println("  " + p.Element(n.Level, n.Name) + "  " + p.Level(kind))
	if n.Description != "" {
		p.Println()
		p.Println(indent(wrapText(n.Description, 88), 2))
	}

	var facts [][2]string
	if n.FilePath != "" {
		facts = append(facts, [2]string{"path", p.Accent(n.FilePath)})
	}
	if n.Signature != "" {
		facts = append(facts, [2]string{"signature", n.Signature})
	}
	if len(n.Tech) > 0 {
		facts = append(facts, [2]string{"tech", strings.Join(n.Tech, p.Muted(" · "))})
	}
	if len(n.Tags) > 0 {
		facts = append(facts, [2]string{"tags", p.Muted("#" + strings.Join(n.Tags, " #"))})
	}
	if len(n.Children) > 0 {
		var names []string
		for i, c := range n.Children {
			if i == 6 {
				names = append(names, p.Muted(fmt.Sprintf("+%d", len(n.Children)-6)))
				break
			}
			names = append(names, p.Element(c.Level, c.Name))
		}
		facts = append(facts, [2]string{"contains", strings.Join(names, p.Muted(", "))})
	}
	if len(facts) > 0 {
		p.Println()
		p.Println(indent(p.KeyValues(facts), 2))
	}

	edges := func(title, arrow string, list []graph.Edge) {
		if len(list) == 0 {
			return
		}
		p.Println()
		p.Println(p.Heading(fmt.Sprintf(" %s  %s", title, p.Muted(fmt.Sprint(len(list))))))
		for _, e := range list {
			line := "  " + p.Muted(arrow) + " " + p.Element(e.Other.Level, e.Other.Name) + "  " + p.Muted(relationVerb(e.Rel.Type))
			if e.Rel.Description != "" {
				line += p.Muted(" · " + e.Rel.Description)
			}
			p.Println(line)
		}
	}
	edges("Uses", "→", g.Outgoing(n.ID))
	edges("Used by", "←", g.Incoming(n.ID))

	if len(adrs) > 0 {
		p.Println()
		p.Println(p.Heading(" Decisions"))
		for _, adr := range adrs {
			p.Println(fmt.Sprintf("  %s %s  %s", p.Accent(fmt.Sprintf("ADR-%d", adr.Number)), adr.Title, p.Muted(adr.Status)))
		}
	}

	if hops := g.Dependents(n.ID, 3); len(hops) > 0 {
		far := 0
		for _, h := range hops {
			far = max(far, h.Distance)
		}
		p.Println()
		p.Println(p.Heading(" Blast radius"))
		p.Println("  " + p.Warning(fmt.Sprint(len(hops))) + p.Muted(fmt.Sprintf(" %s depend on it, up to %s away",
			pluralWord(len(hops), "element", "elements"), ui.Plural(far, "hop", "hops"))))
	}
	p.Println()
	p.Println("  " + p.Hint("Explore around it:", fmt.Sprintf("archyl map %q", n.Name)))
	p.Println()
}

func elementJSON(g *graph.Graph, n *graph.Node, adrs []api.ADR) map[string]any {
	rel := func(edges []graph.Edge) []map[string]string {
		out := make([]map[string]string, 0, len(edges))
		for _, e := range edges {
			out = append(out, map[string]string{"id": e.Other.ID, "name": e.Other.Name, "level": e.Other.Level, "type": e.Rel.Type})
		}
		return out
	}
	return map[string]any{
		"id": n.ID, "name": n.Name, "level": n.Level, "kind": n.Kind, "path": n.PathString(),
		"description": n.Description, "filePath": n.FilePath, "technologies": n.Tech, "tags": n.Tags,
		"uses": rel(g.Outgoing(n.ID)), "usedBy": rel(g.Incoming(n.ID)), "decisions": adrs,
		"dependents": len(g.Dependents(n.ID, 3)),
	}
}

func pluralWord(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func wrapText(s string, width int) string {
	words := strings.Fields(s)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() > 0 && cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(w)
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return strings.Join(lines, "\n")
}
