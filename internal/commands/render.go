package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

var levels = []string{"system", "container", "component", "code"}

// breadcrumb draws an element path ("Billing / API / Invoicing") with each
// segment marked by its C4 level, which follows from its depth.
func breadcrumb(p *ui.Printer, path string) string {
	parts := strings.Split(path, " / ")
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		level := "code"
		if i < len(levels) {
			level = levels[i]
		}
		out = append(out, p.Element(level, part))
	}
	return strings.Join(out, p.Muted("  ›  "))
}

// ownersLine lists user and team owners compactly.
func ownersLine(p *ui.Printer, users []api.Owner, teams []api.TeamOwner) string {
	var names []string
	for _, t := range teams {
		names = append(names, p.Accent("@"+firstNonEmpty(t.Slug, t.Name)))
	}
	for _, u := range users {
		names = append(names, u.Email)
	}
	if len(names) == 0 {
		return p.Muted("no owner")
	}
	return strings.Join(names, p.Muted(" · "))
}

// elementList renders "▣ Checkout, ▣ Admin (+2)".
func elementList(p *ui.Printer, elements []api.ImpactedElement, limit int) string {
	var parts []string
	for i, e := range elements {
		if i == limit {
			parts = append(parts, p.Muted(fmt.Sprintf("+%d", len(elements)-limit)))
			break
		}
		parts = append(parts, p.Element(e.Type, e.Name))
	}
	return strings.Join(parts, p.Muted(", "))
}

// touchedByID indexes the blast radius by element ID.
func touchedByID(r *api.LocateResult) map[string]api.TouchedElement {
	out := map[string]api.TouchedElement{}
	if r.Impact == nil {
		return out
	}
	for _, e := range r.Impact.Elements {
		out[e.ID] = e
	}
	return out
}

// filesPerElement counts files by their most specific element.
func filesPerElement(r *api.LocateResult) (map[string]int, []string) {
	counts := map[string]int{}
	var order []string
	for _, f := range r.Files {
		if len(f.Elements) == 0 {
			continue
		}
		id := f.Elements[0].ID
		if counts[id] == 0 {
			order = append(order, id)
		}
		counts[id]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	return counts, order
}
