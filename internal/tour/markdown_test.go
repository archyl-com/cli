package tour

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarkdown(
	t *testing.T,
) {
	g := platform()
	plain, _ := Plan(g, Options{Project: "video-wf"})
	plain.Stops[1].Files = []File{{Path: "api/ingest/main.go", Reason: "entry point"}}
	plain.Stops[1].Owners = []string{"Ingest team"}
	plain.Stops[2].Decisions = []Decision{{Number: 12, Title: "Workflows run on Temporal", Status: "accepted"}}
	plain.Stops[2].Notes = []Note{{Kind: "pitfall", Text: "Activities   must be idempotent"}}

	narrated, _ := Plan(g, Options{Project: "video-wf"})
	narrated.Apply(&Narration{
		Overview: "Partners push packages to Ingest, which starts workflows.",
		Flows:    []string{"Content Partners → Ingest → Video Workflow: a package becomes a delivery"},
		Stops: []struct {
			Name  string `json:"name"`
			Story string `json:"story"`
		}{{Name: "Ingest", Story: "Ingest checks what partners send."}},
		Trace: "Follow one package from ingest-api to wf-worker-anywf.",
	})

	testCases := []struct {
		name        string
		tour        *Tour
		diagrams    bool
		want        []string
		wantMissing []string
	}{
		{
			name: "facts only, for agents",
			tour: plain,
			want: []string{
				"# Tour of video-wf",
				"## The big picture",
				"- **Systems:** Ingest, Video Workflow",
				"- **Shared infrastructure:** Kafka, Redis",
				"## 1. Ingest",
				"**Why it's here:** Where work comes in from outside: Content Partners sends work to it.",
				"- **Key parts:** ingest-api, ingest-ui and 1 more",
				"- `api/ingest/main.go`: entry point",
				"- **Owned by:** Ingest team",
				"## 2. Video Workflow",
				"- ADR-12 Workflows run on Temporal (accepted)",
				"- pitfall: Activities must be idempotent",
				"**Try it:** follow one piece of work through the code: Content Partners → Ingest → Video Workflow.",
			},
			wantMissing: []string{"```mermaid", "written by AI"},
		},
		{
			name: "with a story",
			tour: narrated,
			want: []string{
				"the story is written by AI from that model",
				"Partners push packages to Ingest, which starts workflows.",
				"**Main flows**\n\n- Content Partners → Ingest → Video Workflow: a package becomes a delivery",
				"## 1. Ingest\n\n*system*",
				"Ingest checks what partners send.",
				"**Try it:** Follow one package from ingest-api to wf-worker-anywf.",
			},
			wantMissing: []string{"**Why it's here:** Where work comes in"},
		},
		{
			name:     "with diagrams, for a document",
			tour:     plain,
			diagrams: true,
			want:     []string{"```mermaid\nflowchart TB"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			md := Markdown(testCase.tour, g, MarkdownOptions{Diagrams: testCase.diagrams})
			for _, w := range testCase.want {
				assert.Contains(t, md, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, md, w)
			}
			assert.Equal(t, 2, strings.Count(md, "\n## ")-2, "one section per element stop")
		})
	}
}
