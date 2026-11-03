package history

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func TestDescribe(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		changes []FieldChange
		want    string
	}{
		{name: "renamed", changes: []FieldChange{{Field: FieldName, Before: "Order Service", After: "Orders API"}}, want: `renamed from "Order Service"`},
		{name: "moved", changes: []FieldChange{{Field: FieldParent, Before: "Shop / API", After: "Shop / Web"}}, want: "moved from Shop / API to Shop / Web"},
		{name: "to the top", changes: []FieldChange{{Field: FieldParent, Before: "Shop"}}, want: "moved from Shop to the top level"},
		{name: "a type", changes: []FieldChange{{Field: FieldType, Before: "web_app", After: "api"}}, want: "type web app → api"},
		{name: "technologies", changes: []FieldChange{{Field: FieldTechnology, After: "Go"}}, want: "technology none → Go"},
		{name: "external", changes: []FieldChange{{Field: FieldExternal, Before: "no", After: "yes"}}, want: "now external"},
		{name: "internal", changes: []FieldChange{{Field: FieldExternal, Before: "yes", After: "no"}}, want: "no longer external"},
		{name: "ends", changes: []FieldChange{{Field: FieldSource, Before: "A", After: "B"}, {Field: FieldTarget, Before: "C", After: "D"}}, want: "now from B (was A); now to D (was C)"},
		{name: "several", changes: []FieldChange{{Field: FieldName, Before: "A", After: "B"}, {Field: FieldDescription}}, want: `renamed from "A"; description changed`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Describe(testCase.changes))
		})
	}
}

func TestWhereAndFolded(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		element    Element
		wantWhere  string
		wantFolded string
	}{
		{name: "a system", element: Element{Level: "system", Name: "Shop", Path: "Shop"}, wantWhere: "system"},
		{name: "a container", element: Element{Level: "container", Name: "API", Path: "Shop / API", Descendants: 1}, wantWhere: "container in Shop", wantFolded: "and its child"},
		{name: "a component", element: Element{Level: "component", Name: "Cart", Path: "Shop / API / Cart", Descendants: 4}, wantWhere: "component in Shop / API", wantFolded: "and its 4 children"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.wantWhere, Where(testCase.element))
			assert.Equal(t, testCase.wantFolded, Folded(testCase.element))
		})
	}
}

func TestTally(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		summary Summary
		want    string
	}{
		{name: "nothing", want: "no changes"},
		{name: "elements only", summary: Summary{Added: 2, Modified: 1}, want: "elements: 2 added, 1 modified"},
		{name: "both", summary: Summary{Removed: 1, RelationshipsAdded: 3, RelationshipsRemoved: 1}, want: "elements: 1 removed; relationships: 3 added, 1 removed"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Tally(testCase.summary))
		})
	}
}

func report(
	t *testing.T,
	before *api.Model,
	after *api.Model,
) *Report {
	t.Helper()
	d := Compare(before, after, Options{})
	s := d.Scene()
	return &Report{
		From:    Resolved{Label: "v1.2.0", Version: &api.Version{Number: 11, CreatedAt: time.Date(2026, 9, 8, 12, 0, 0, 0, time.Local)}},
		To:      Resolved{Label: "now"},
		Summary: d.Summary(),
		Diff:    d,
		View:    s.View(DefaultScope(s.Graph)),
	}
}

func TestMarkdown(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		after       *api.Model
		opts        MarkdownOptions
		want        []string
		wantMissing []string
	}{
		{
			name:  "release notes",
			after: shopAfter(),
			want: []string{
				"## Architecture changes v1.2.0 → now\n",
				"v1.2.0 (REV 11, 2026-09-08) → now · elements: 1 added, 1 removed, 2 modified; relationships: 1 added, 2 removed",
				"### Added\n\n- **Warehouse** (system)\n",
				"### Removed\n\n- **Mailer** (container in Shop) and its 3 children\n",
				"### Modified\n\n- **Shopfront** (container in Shop): renamed from \"Storefront\"\n- **Cart** (component in Shop / Shopfront): moved from Shop / Orders API to Shop / Shopfront\n",
				"### Relationships\n\n- added: Orders API → Warehouse (reserves stock)\n- and 2 relationships removed with the elements above\n",
			},
			wantMissing: []string{"```mermaid", "Orders API → Mailer"},
		},
		{
			name:  "with the diagram",
			after: shopAfter(),
			opts:  MarkdownOptions{Diagram: true},
			want:  []string{"```mermaid\nflowchart TB\n", "classDef added", "<b>− Mailer</b>"},
		},
		{
			name:        "no changes",
			after:       shop(),
			opts:        MarkdownOptions{Diagram: true},
			want:        []string{"· no changes\n", "No architecture changes."},
			wantMissing: []string{"###", "```mermaid"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := Markdown(report(t, shop(), testCase.after), testCase.opts)
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, out, w)
			}
		})
	}
}

func TestMarkdownStripsControlCharacters(
	t *testing.T,
) {
	after := shop()
	after.Systems = append(after.Systems, api.System{ID: "evil", Name: "Evil\x1b[31m"})

	out := Markdown(report(t, shop(), after), MarkdownOptions{})

	assert.NotContains(t, out, "\x1b")
	assert.Contains(t, out, "**Evil[31m**")
}

func TestReportTitle(
	t *testing.T,
) {
	r := &Report{From: Resolved{Label: "REV 3"}, To: Resolved{Label: "now"}, Scope: "Billing"}
	assert.Equal(t, "Architecture changes in Billing, REV 3 → now", r.Title())
}

func TestWithin(
	t *testing.T,
) {
	d := Compare(shop(), shopAfter(), Options{})
	s := d.Scene()
	testCases := []struct {
		name     string
		id       string
		wantIDs  []string
		wantRels []string
	}{
		{name: "a container and what is inside it", id: "web", wantIDs: []string{"web", "cart"}},
		{name: "an element inside a removed parent", id: "smtp", wantIDs: []string{"mailer"}, wantRels: []string{"r4"}},
		{name: "a system", id: "wh", wantIDs: []string{"wh"}, wantRels: []string{"r9"}},
		{name: "everything under the shop", id: "shop", wantIDs: []string{"mailer", "web", "cart"}, wantRels: []string{"r9", "r3", "r4"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := d.Within(s.Graph, testCase.id)
			var ids, rels []string
			for _, e := range got.Elements {
				ids = append(ids, e.ID)
			}
			for _, r := range got.Relationships {
				rels = append(rels, r.ID)
			}
			assert.Equal(t, testCase.wantIDs, ids)
			assert.Equal(t, testCase.wantRels, rels)
			require.Len(t, d.Elements, 4, "the diff itself is left whole")
		})
	}
}
