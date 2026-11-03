package decide

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleDraft() *Draft {
	f := sampleFacts()
	return &Draft{
		Title:        "Publish order events through an outbox table",
		Status:       StatusProposed,
		Context:      "Orders were published to Kafka in the request.",
		Decision:     "Write events to an outbox table.\n\n```sql\n## not a heading\nCREATE TABLE outbox ();\n```",
		Consequences: "**Positive**\n\n- No lost events",
		Alternatives: "- Change data capture: needs Debezium",
		Supersedes:   4,
		Elements:     f.Elements()[:1],
	}
}

func TestMarkdown(
	t *testing.T,
) {
	got := sampleDraft().Markdown(sampleFacts().Elements())
	assert.Equal(t, `---
title: Publish order events through an outbox table
status: proposed
supersedes: ADR-4
elements:
    - Orders API
---

## Context

Orders were published to Kafka in the request.

## Decision

Write events to an outbox table.

`+"```sql\n## not a heading\nCREATE TABLE outbox ();\n```"+`

## Consequences

**Positive**

- No lost events

## Alternatives considered

- Change data capture: needs Debezium
`, got)
}

func TestMarkdownRoundTrip(
	t *testing.T,
) {
	f := sampleFacts()
	testCases := []struct {
		name  string
		draft *Draft
	}{
		{name: "full draft", draft: sampleDraft()},
		{name: "a template", draft: Template(f)},
		{
			name:  "a title YAML would misread",
			draft: &Draft{Title: "Use: colons, #hashes and 'quotes'", Status: "accepted", Elements: []Element{}},
		},
		{
			name: "same names in two systems are written as paths",
			draft: &Draft{Title: "T", Status: StatusProposed, Elements: []Element{
				{ID: "a2", Name: "Orders API", Type: "container", Path: "Billing / Orders API"},
			}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			facts := f
			facts.Touched = append(facts.Touched, Touched{Element: Element{ID: "a2", Name: "Orders API", Type: "container", Path: "Billing / Orders API"}})
			candidates := facts.Elements()
			for _, text := range []string{testCase.draft.Markdown(candidates), testCase.draft.EditorFile(candidates)} {
				got, unknown, err := Parse(text, facts)
				require.NoError(t, err)
				assert.Empty(t, unknown)
				assert.Equal(t, testCase.draft, got)
			}
		})
	}
}

func TestEditorFile(
	t *testing.T,
) {
	got := sampleDraft().EditorFile(sampleFacts().Elements())
	assert.True(t, strings.HasPrefix(got, "<!--\nEdit the decision"))
	assert.Contains(t, got, "Delete everything to cancel.")
	assert.Contains(t, got, "  - Orders DB (container)")
	assert.Contains(t, got, "status: proposed or accepted.")
}

func TestParse(
	t *testing.T,
) {
	apiElement := Element{ID: "api", Name: "Orders API", Type: "container", Path: "Shop / Orders API"}
	dbElement := Element{ID: "db", Name: "Orders DB", Type: "container", Path: "Shop / Orders DB"}

	testCases := []struct {
		name        string
		text        string
		want        *Draft
		wantUnknown []string
		wantErr     error
		wantErrText string
	}{
		{name: "an empty file cancels", text: "", wantErr: ErrEmpty},
		{name: "blank lines cancel", text: "\n  \n\t\n", wantErr: ErrEmpty},
		{name: "only the comment left cancels", text: "<!--\nEdit the decision\n-->\n\n", wantErr: ErrEmpty},
		{
			name: "no front matter: the heading is the title",
			text: "# Use an outbox\n\nWe kept losing events.\n\n## Decision\n\nOutbox table.\n",
			want: &Draft{Title: "Use an outbox", Status: StatusProposed, Context: "We kept losing events.", Decision: "Outbox table."},
		},
		{
			name:        "elements by name, path, with the type copied, unknown ones apart",
			text:        "---\ntitle: T\nelements:\n  - orders api\n  - Shop / Orders DB (container)\n  - Payments\n  - Orders API\n---\n",
			want:        &Draft{Title: "T", Status: StatusProposed, Elements: []Element{apiElement, dbElement}},
			wantUnknown: []string{"Payments"},
		},
		{
			name: "an unknown heading stays in its section",
			text: "---\ntitle: T\n---\n## Decision\n\nD\n\n## Rollout\n\nBehind a flag.\n\n## Alternatives\n\n- none\n",
			want: &Draft{Title: "T", Status: StatusProposed, Decision: "D\n\n## Rollout\n\nBehind a flag.", Alternatives: "- none"},
		},
		{
			name: "Windows line ends, a byte order mark, any heading case and status case",
			text: "\ufeff---\r\ntitle: T\r\nstatus: Accepted\r\n---\r\n## CONTEXT\r\n\r\nC\r\n",
			want: &Draft{Title: "T", Status: "accepted", Context: "C"},
		},
		{
			name: "front matter title wins over a heading",
			text: "---\ntitle: From the front matter\n---\n# From the heading\n",
			want: &Draft{Title: "From the front matter", Status: StatusProposed},
		},
		{
			name: "only the opening instructions are dropped; the person's comments stay",
			text: "<!--\nEdit the decision\n-->\n---\ntitle: T\n---\n## Context\n\nC <!-- todo -->\n\n" +
				"```html\n<!-- a sample -->\n```\n",
			want: &Draft{Title: "T", Status: StatusProposed, Context: "C <!-- todo -->\n\n```html\n<!-- a sample -->\n```"},
		},
		{
			name: "a tilde fence hides headings, and only ~~~ closes it",
			text: "---\ntitle: T\n---\n## Decision\n\n~~~md\n## Context\n```\n## Consequences\n~~~\n\n## Context\n\nC\n",
			want: &Draft{Title: "T", Status: StatusProposed, Decision: "~~~md\n## Context\n```\n## Consequences\n~~~", Context: "C"},
		},
		{
			name:        "too many elements",
			text:        "---\ntitle: T\nelements: [a, b, c, d, e, f, g, h, i, j, k, l, m]\n---\n",
			wantErrText: "a decision links at most 12 elements",
		},
		{name: "no title anywhere", text: "---\nstatus: proposed\n---\n## Context\n\nC\n", wantErr: ErrNoTitle},
		{name: "unclosed front matter", text: "---\ntitle: T\n## Context\n", wantErrText: "the front matter has no closing --- line"},
		{name: "broken YAML", text: "---\ntitle: [T\n---\n", wantErrText: "read the front matter"},
		{name: "unknown status", text: "---\ntitle: T\nstatus: maybe\n---\n", wantErrText: `status "maybe" is not one of proposed, accepted`},
		{name: "supersedes a decision that does not exist", text: "---\ntitle: T\nsupersedes: ADR-99\n---\n", wantErrText: `supersedes "ADR-99" is not a decision of this project`},
		{
			name: "supersedes as a bare number",
			text: "---\ntitle: T\nsupersedes: 2\n---\n",
			want: &Draft{Title: "T", Status: StatusProposed, Supersedes: 2},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, unknown, err := Parse(testCase.text, sampleFacts())
			switch {
			case testCase.wantErr != nil:
				require.Error(t, err)
				assert.True(t, errors.Is(err, testCase.wantErr), err.Error())
				return
			case testCase.wantErrText != "":
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErrText)
				return
			}
			require.NoError(t, err)
			if testCase.want.Elements == nil {
				testCase.want.Elements = []Element{}
			}
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.wantUnknown, unknown)
		})
	}
}
