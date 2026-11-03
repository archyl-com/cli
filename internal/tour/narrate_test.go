package tour

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNarrationPrompt(
	t *testing.T,
) {
	tr, _ := Plan(platform(), Options{Project: "video-wf"})
	prompt := NarrationPrompt(tr)
	for _, want := range []string{
		"Use only the facts below",
		"Reply with one JSON object",
		"Project: video-wf",
		"Shared infrastructure: Kafka, Redis",
		"## Ingest (system)",
		"Part: ingest-api",
		"Uses: Video Workflow (Starts the Check workflow)",
		"Used by: Content Partners (Pushes packages)",
		"A path through the stops: Content Partners → Ingest → Video Workflow",
	} {
		assert.Contains(t, prompt, want)
	}
	assert.Less(t, len(prompt), 32*1024, "the prompt fits one chat message")
}

func TestParseNarration(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		reply     string
		wantErr   bool
		wantStory string
	}{
		{
			name:      "bare json",
			reply:     `{"overview":"It turns packages into deliveries.","stops":[{"name":"Ingest","story":"Checks packages."}]}`,
			wantStory: "It turns packages into deliveries.",
		},
		{
			name:      "fenced, with prose around",
			reply:     "Here is the tour:\n```json\n{\"overview\":\"A story.\"}\n```\nEnjoy!",
			wantStory: "A story.",
		},
		{name: "no json", reply: "I cannot help with that.", wantErr: true},
		{name: "broken json", reply: `{"overview": "x",`, wantErr: true},
		{name: "empty narration", reply: `{"flows":[]}`, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			n, err := ParseNarration(testCase.reply)
			if testCase.wantErr {
				require.ErrorIs(t, err, ErrNoNarration)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantStory, n.Overview)
		})
	}
}

func TestApplyNarration(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		order     []string
		wantOrder []string
	}{
		{
			name:      "a full order is followed, whatever the case",
			order:     []string{"video workflow", "Ingest"},
			wantOrder: []string{"overview:video-wf", "element:Video Workflow", "element:Ingest", "recap:Recap"},
		},
		{
			name:      "an order missing a stop is ignored",
			order:     []string{"Video Workflow"},
			wantOrder: []string{"overview:video-wf", "element:Ingest", "element:Video Workflow", "recap:Recap"},
		},
		{
			name:      "an order naming an unknown stop is ignored",
			order:     []string{"Video Workflow", "Billing"},
			wantOrder: []string{"overview:video-wf", "element:Ingest", "element:Video Workflow", "recap:Recap"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tr, _ := Plan(platform(), Options{Project: "video-wf"})
			n, err := ParseNarration(`{"overview":"Story.","flows":["a","b","c","d"],"trace":"Follow a package.",` +
				`"stops":[{"name":"ingest","story":"Checks packages."},{"name":"Nope","story":"Invented."}]}`)
			require.NoError(t, err)
			n.Order = testCase.order
			tr.Apply(n)

			assert.Equal(t, testCase.wantOrder, stopNames(tr))
			assert.True(t, tr.Narrated)
			assert.Equal(t, "Story.", tr.Stops[0].Story)
			assert.Len(t, tr.Stops[0].Flows, 3, "at most three flows")
			assert.Equal(t, "Follow a package.", tr.TraceTask)
			for _, s := range tr.Stops {
				if s.Name == "Ingest" {
					assert.Equal(t, "Checks packages.", s.Story)
				}
				assert.False(t, strings.Contains(s.Story, "Invented"), "a story for an unknown stop is dropped")
			}
		})
	}
}

func TestApplyNarrationGuards(
	t *testing.T,
) {
	twins := func() *Tour {
		return &Tour{Stops: []Stop{
			{Kind: KindOverview, Name: "p"},
			{Kind: KindElement, Name: "API", ElementID: "a"},
			{Kind: KindElement, Name: "api", ElementID: "b"},
			{Kind: KindElement, Name: "Worker", ElementID: "w", Why: "api" + nextReason},
			{Kind: KindRecap},
		}}
	}
	tr := twins()
	tr.Apply(&Narration{Overview: "o", Order: []string{"Worker", "API", "api"},
		Stops: []struct {
			Name  string `json:"name"`
			Story string `json:"story"`
		}{{Name: "API", Story: "which one?"}}})
	assert.Equal(t, "API", tr.Stops[1].Name, "two stops sharing a name: the order is kept")
	assert.Empty(t, tr.Stops[1].Story, "and no story can say which stop it is about")

	focused := &Tour{FocusID: "f", Stops: []Stop{
		{Kind: KindOverview},
		{Kind: KindElement, Name: "Focus", ElementID: "f"},
		{Kind: KindElement, Name: "Other", ElementID: "o"},
		{Kind: KindRecap},
	}}
	focused.Apply(&Narration{Overview: "o", Order: []string{"Other", "Focus"}})
	assert.Equal(t, "Focus", focused.Stops[1].Name, "the focus stays first")

	moved := &Tour{Stops: []Stop{
		{Kind: KindOverview},
		{Kind: KindElement, Name: "A"},
		{Kind: KindElement, Name: "B", Why: "A" + nextReason},
		{Kind: KindElement, Name: "C"},
		{Kind: KindRecap},
	}}
	moved.Apply(&Narration{Overview: "o", Order: []string{"B", "C", "A"}})
	assert.Equal(t, "B", moved.Stops[1].Name)
	assert.Empty(t, moved.Stops[1].Why, "a reason naming a stop that no longer precedes it is dropped")
}

func TestSanitize(
	t *testing.T,
) {
	tr := &Tour{Project: "p\x1b[2J", TraceTask: "t\x1b]0;x\x07", Trace: []string{"a\rb"}, Stops: []Stop{{
		Kind: KindElement, Name: "API\x1b[31m", Story: "s\x1b[H", Notes: []Note{{Kind: "pitfall", Text: "n\x1b[K"}},
		Uses: []Link{{Name: "x\x07", Label: "l\x1b"}}, Owners: []string{"o\x1b"},
	}}}
	tr.Sanitize()
	assert.Equal(t, "p[2J", tr.Project)
	assert.Equal(t, "t]0;x", tr.TraceTask)
	assert.Equal(t, []string{"ab"}, tr.Trace)
	s := tr.Stops[0]
	assert.Equal(t, "API[31m", s.Name)
	assert.Equal(t, "s[H", s.Story)
	assert.Equal(t, "n[K", s.Notes[0].Text)
	assert.Equal(t, Link{Name: "x", Label: "l"}, s.Uses[0])
	assert.Equal(t, []string{"o"}, s.Owners)
}
