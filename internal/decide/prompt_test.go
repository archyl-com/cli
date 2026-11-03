package decide

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrompt(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		facts       func() Facts
		want        []string
		wantMissing []string
	}{
		{
			name:  "hands over every fact",
			facts: sampleFacts,
			want: []string{
				"Use only the facts below. Do not invent",
				"Reply with one JSON object and nothing else",
				"What the developer says was decided: use an outbox table for order events",
				"Branch: feat/outbox (compared with origin/main)",
				"- Orders API (container, in Shop), used by Storefront",
				"- Orders DB (container, in Shop)",
				"- ADR-4 Publish order events straight to Kafka (accepted)",
				"- internal/orders/outbox.go",
				"The diff:\ndiff --git a/internal/orders/outbox.go",
			},
			wantMissing: []string{"infer it from the diff."},
		},
		{
			name:  "nothing said, nothing in the model",
			facts: func() Facts { return Facts{Diff: "+x\n"} },
			want: []string{
				"did not say what was decided: infer it from the diff.",
				"- none: the changed files are not in the architecture model",
			},
			wantMissing: []string{"Decisions already recorded", "Changed files:", "Branch:"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := Prompt(testCase.facts())
			for _, w := range testCase.want {
				assert.Contains(t, got, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, got, w)
			}
		})
	}
}

func TestPromptStaysUnderTheChatLimit(
	t *testing.T,
) {
	f := sampleFacts()
	f.Diff = strings.Repeat("+a line of the diff\n", 4000)
	got := Prompt(f)
	assert.LessOrEqual(t, len(got), maxPromptBytes)
	assert.True(t, strings.HasSuffix(got, "\n"), "the diff is cut at a line end")
	assert.Contains(t, got, "- Orders API (container", "the facts are kept, the diff is cut")
}

func TestParseReply(
	t *testing.T,
) {
	api := Element{ID: "api", Name: "Orders API", Type: "container", Path: "Shop / Orders API"}
	db := Element{ID: "db", Name: "Orders DB", Type: "container", Path: "Shop / Orders DB"}
	full := `{"title":"Publish order events through an outbox table",` +
		`"context":"Orders were published to Kafka in the request.",` +
		`"decision":"Write events to an outbox table in the order's transaction.",` +
		`"consequences":{"positive":["No lost events","  "],"negative":["One more table to purge"]},` +
		`"alternatives":[{"option":"Change data capture","whyNot":"needs Debezium"},{"option":"Retry in memory"},{"option":""}],` +
		`"elements":["orders api","Billing"],"supersedes":4,"duplicateOf":0}`

	testCases := []struct {
		name    string
		reply   string
		want    *Draft
		wantErr error
	}{
		{
			name:  "a full reply",
			reply: full,
			want: &Draft{
				Title:        "Publish order events through an outbox table",
				Status:       StatusProposed,
				Context:      "Orders were published to Kafka in the request.",
				Decision:     "Write events to an outbox table in the order's transaction.",
				Consequences: "**Positive**\n\n- No lost events\n\n**Negative**\n\n- One more table to purge",
				Alternatives: "- Change data capture: needs Debezium\n- Retry in memory",
				Supersedes:   4,
				Elements:     []Element{api},
			},
		},
		{
			name:  "inside a code fence with prose around",
			reply: "Here is the draft:\n```json\n" + full + "\n```\nHope it helps.",
			want: &Draft{
				Title:        "Publish order events through an outbox table",
				Status:       StatusProposed,
				Context:      "Orders were published to Kafka in the request.",
				Decision:     "Write events to an outbox table in the order's transaction.",
				Consequences: "**Positive**\n\n- No lost events\n\n**Negative**\n\n- One more table to purge",
				Alternatives: "- Change data capture: needs Debezium\n- Retry in memory",
				Supersedes:   4,
				Elements:     []Element{api},
			},
		},
		{
			name: "text where lists were asked, numbers as strings, elements as objects",
			reply: `{"title":"T","decision":"D","consequences":"Fewer lost events.","alternatives":["CDC"],` +
				`"elements":[{"name":"Shop / Orders DB"},{"id":"api"}],"supersedes":"ADR-2","duplicateOf":"#4"}`,
			want: &Draft{
				Title:        "T",
				Status:       StatusProposed,
				Decision:     "D",
				Consequences: "Fewer lost events.",
				Alternatives: "- CDC",
				Supersedes:   2,
				DuplicateOf:  4,
				Elements:     []Element{db, api},
			},
		},
		{
			name:  "unknown elements and decisions are dropped; no element left means all touched",
			reply: `{"title":"T","decision":"D","elements":["Payments","Orders"],"supersedes":99,"duplicateOf":-1}`,
			want:  &Draft{Title: "T", Status: StatusProposed, Decision: "D", Elements: []Element{api, db}},
		},
		{
			name:  "an element named twice is linked once",
			reply: `{"title":"T","decision":"D","elements":["Orders API","api","shop / orders api"]}`,
			want:  &Draft{Title: "T", Status: StatusProposed, Decision: "D", Elements: []Element{api}},
		},
		{
			name:  "fields of the wrong type are ignored",
			reply: `{"title":"T","decision":"D","consequences":42,"alternatives":{"x":1},"elements":"Orders API","supersedes":true}`,
			want:  &Draft{Title: "T", Status: StatusProposed, Decision: "D", Elements: []Element{api, db}},
		},
		{name: "no JSON", reply: "I cannot help with that.", wantErr: ErrNoDraft},
		{name: "broken JSON", reply: `{"title": "T", "decision": }`, wantErr: ErrNoDraft},
		{name: "a title of the wrong type", reply: `{"title": 3, "decision": "D"}`, wantErr: ErrNoDraft},
		{name: "no title", reply: `{"decision":"D"}`, wantErr: ErrNoDraft},
		{name: "no decision", reply: `{"title":"T","decision":"   "}`, wantErr: ErrNoDraft},
		{name: "empty", reply: "", wantErr: ErrNoDraft},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseReply(testCase.reply, sampleFacts())
			if testCase.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, testCase.wantErr), err.Error())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestParseReplyDefaultLinks(
	t *testing.T,
) {
	many := func(n int) Facts {
		var f Facts
		for i := range n {
			id := fmt.Sprintf("e%d", i)
			f.Touched = append(f.Touched, Touched{Element: Element{ID: id, Name: "Element " + id, Type: "component"}})
		}
		return f
	}
	testCases := []struct {
		name              string
		facts             Facts
		reply             string
		wantLinks         int
		wantTemplateLinks int
	}{
		{
			name:              "nothing named, a few touched: all of them",
			facts:             many(3),
			reply:             `{"title":"T","decision":"D"}`,
			wantLinks:         3,
			wantTemplateLinks: 3,
		},
		{
			name:  "nothing named, many touched: none",
			facts: many(4),
			reply: `{"title":"T","decision":"D","elements":["nope"]}`,
		},
		{
			name:      "named elements are capped",
			facts:     many(30),
			reply:     `{"title":"T","decision":"D","elements":` + namesJSON(30) + `}`,
			wantLinks: MaxLinks,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseReply(testCase.reply, testCase.facts)
			require.NoError(t, err)
			assert.NotNil(t, got.Elements)
			assert.Len(t, got.Elements, testCase.wantLinks)
			assert.Len(t, Template(testCase.facts).Elements, testCase.wantTemplateLinks)
		})
	}
}

func namesJSON(n int) string {
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, fmt.Sprintf("%q", fmt.Sprintf("Element e%d", i)))
	}
	return "[" + strings.Join(names, ",") + "]"
}

func TestParseReplyStripsControlCharacters(
	t *testing.T,
) {
	reply := `{"title":"Use \u001b]0;pwned\u0007an outbox","context":"C\u001b[2J","decision":"D\u0000",` +
		`"consequences":{"positive":["fast\u001b[31m"]},"alternatives":["\u009bnone"]}`
	got, err := ParseReply(reply, Facts{})
	require.NoError(t, err)
	for _, text := range []string{got.Title, got.Context, got.Decision, got.Consequences, got.Alternatives} {
		assert.NotRegexp(t, `[\x00-\x08\x0b-\x1f\x7f-\x9f]`, text)
	}
	assert.Equal(t, "Use ]0;pwnedan outbox", got.Title)
}

func TestPromptBoundsTheFacts(
	t *testing.T,
) {
	var f Facts
	for i := range 2000 {
		name := fmt.Sprintf("Élément très long numéro %d %s", i, strings.Repeat("é", 50))
		f.Touched = append(f.Touched, Touched{
			Element: Element{ID: fmt.Sprint(i), Name: name, Type: "component", Path: "Système / " + name},
			UsedBy:  []string{"A", "B"},
		})
	}
	f.Decided = strings.Repeat("décidé ", 1000)
	f.Diff = strings.Repeat("+é\n", 10000)
	got := Prompt(f)
	assert.LessOrEqual(t, len(got), maxPromptBytes)
	assert.True(t, utf8.ValidString(got), "never cut inside a character")
	assert.Contains(t, got, fmt.Sprintf("- and %d more\n", 2000-maxTouched))
	assert.Equal(t, maxTouched, strings.Count(got, "(component, in "))
}

func TestParseReplyClipsLongFields(
	t *testing.T,
) {
	long := strings.Repeat("word ", 2000)
	got, err := ParseReply(`{"title":"`+long+`","decision":"`+long+`","consequences":{"positive":["`+long+`"]}}`, Facts{})
	require.NoError(t, err)
	assert.LessOrEqual(t, len([]rune(got.Title)), maxTitle)
	assert.LessOrEqual(t, len([]rune(got.Decision)), maxSection)
	assert.LessOrEqual(t, len([]rune(got.Consequences)), maxItem+len("**Positive**\n\n- "))
	assert.Empty(t, got.Elements, "nothing touched, nothing to link")
}

func TestParseADRNumber(
	t *testing.T,
) {
	testCases := []struct {
		in   string
		want int
	}{
		{in: "ADR-4", want: 4},
		{in: "adr 12", want: 12},
		{in: "#7", want: 7},
		{in: " 3 ", want: 3},
		{in: "ADR-0", want: 0},
		{in: "-2", want: 0},
		{in: "four", want: 0},
		{in: "", want: 0},
	}
	for _, testCase := range testCases {
		t.Run(testCase.in, func(t *testing.T) {
			assert.Equal(t, testCase.want, ParseADRNumber(testCase.in))
		})
	}
}

func TestResolve(
	t *testing.T,
) {
	candidates := []Element{
		{ID: "a1", Name: "API", Type: "container", Path: "Shop / API"},
		{ID: "a2", Name: "API", Type: "container", Path: "Billing / API"},
		{ID: "w", Name: "Web", Type: "container", Path: "Shop / Web"},
	}
	testCases := []struct {
		name   string
		ref    string
		wantID string
	}{
		{name: "by id", ref: "a2", wantID: "a2"},
		{name: "by path, any case", ref: "billing / api", wantID: "a2"},
		{name: "by unique name", ref: "web", wantID: "w"},
		{name: "an ambiguous name resolves to nothing", ref: "API"},
		{name: "unknown", ref: "Payments"},
		{name: "blank", ref: "  "},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			e, ok := resolve(testCase.ref, candidates)
			assert.Equal(t, testCase.wantID != "", ok)
			assert.Equal(t, testCase.wantID, e.ID)
		})
	}
}
