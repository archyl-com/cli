package decide

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/repo"
)

// sampleFacts is a change to the Orders API, with two recorded decisions.
func sampleFacts() Facts {
	return Facts{
		Decided: "use an outbox table for order events",
		Branch:  "feat/outbox",
		Base:    "origin/main",
		Files: []repo.Change{
			{Path: "internal/orders/outbox.go", Status: "added"},
			{Path: "internal/orders/service.go", Status: "modified"},
		},
		Diff: "diff --git a/internal/orders/outbox.go b/internal/orders/outbox.go\n+package orders\n",
		Touched: []Touched{
			{Element: Element{ID: "api", Name: "Orders API", Type: "container", Path: "Shop / Orders API"}, UsedBy: []string{"Storefront"}},
			{Element: Element{ID: "db", Name: "Orders DB", Type: "container", Path: "Shop / Orders DB"}},
		},
		ADRs: []ADRRef{
			{Number: 4, Title: "Publish order events straight to Kafka", Status: "accepted"},
			{Number: 2, Title: "Use Postgres", Status: "accepted"},
		},
	}
}

func TestTemplate(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		facts func() Facts
		want  *Draft
	}{
		{
			name:  "prefilled with the facts",
			facts: sampleFacts,
			want: &Draft{
				Title:    "Use an outbox table for order events",
				Status:   StatusProposed,
				Decision: "use an outbox table for order events",
				Context: "The change touches Orders API (container), Orders DB (container). " +
					"Files changed: internal/orders/outbox.go, internal/orders/service.go.",
				Elements: sampleFacts().Elements(),
			},
		},
		{
			name: "nothing decided, nothing touched",
			facts: func() Facts {
				return Facts{}
			},
			want: &Draft{Status: StatusProposed, Elements: []Element{}},
		},
		{
			name: "a long first line becomes a short title",
			facts: func() Facts {
				return Facts{Decided: "évite " + strings.Repeat("x", 200) + ".\nmore detail"}
			},
			want: &Draft{
				Title:    "Évite " + strings.Repeat("x", maxTitle-7) + "…",
				Status:   StatusProposed,
				Decision: "évite " + strings.Repeat("x", 200) + ".\nmore detail",
				Elements: []Element{},
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Template(testCase.facts()))
		})
	}
}

func TestTemplateListsAtMostTenFiles(
	t *testing.T,
) {
	var f Facts
	for i := range 13 {
		f.Files = append(f.Files, repo.Change{Path: string(rune('a'+i)) + ".go"})
	}
	assert.Contains(t, Template(f).Context, "j.go, and 3 more.")
}

func TestRequest(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		draft Draft
		want  api.ADRRequest
	}{
		{
			name:  "plain",
			draft: Draft{Title: " Use an outbox ", Context: "c\n", Decision: "d", Consequences: "**Positive**\n\n- p"},
			want:  api.ADRRequest{Title: "Use an outbox", Status: StatusProposed, Context: "c", Decision: "d", Consequences: "**Positive**\n\n- p"},
		},
		{
			name:  "supersedes and alternatives go in the text",
			draft: Draft{Title: "T", Status: "accepted", Context: "c", Decision: "d", Alternatives: "- CDC: more moving parts", Supersedes: 4},
			want: api.ADRRequest{
				Title:    "T",
				Status:   "accepted",
				Context:  "c\n\nSupersedes ADR-4.",
				Decision: "d\n\n### Alternatives considered\n\n- CDC: more moving parts",
			},
		},
		{
			name:  "empty sections",
			draft: Draft{Title: "T", Supersedes: 2},
			want:  api.ADRRequest{Title: "T", Status: StatusProposed, Context: "Supersedes ADR-2."},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.draft.Request())
		})
	}
}
