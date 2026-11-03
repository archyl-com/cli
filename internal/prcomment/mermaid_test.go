package prcomment

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func element(id, name, level string) api.Element {
	return api.Element{ID: id, Name: name, Type: level}
}

func dependent(id, name, level string, distance int, via, rel string) api.ImpactedElement {
	return api.ImpactedElement{Element: element(id, name, level), Distance: distance, Via: via, RelationshipType: rel}
}

func TestMermaidGolden(
	t *testing.T,
) {
	impact := &api.BlastRadius{Elements: []api.TouchedElement{
		{
			Element: element("pay", `Payments "Core" API`, "container"),
			Dependents: []api.ImpactedElement{
				dependent("chk", "Checkout <web>", "container", 1, `Checkout <web> uses Payments "Core" API`, "uses"),
				dependent("adm", "Admin | Back-office", "container", 1, `Admin | Back-office calls Payments "Core" API`, "calls"),
				dependent("mob", "Mobile App", "container", 2, "Mobile App uses Checkout <web>", "uses"),
				dependent("bat", "Nightly #batch", "component", 2, "unparseable", "reads_from"),
			},
		},
		{
			Element: element("led", "Ledger", "component"),
			Dependents: []api.ImpactedElement{
				dependent("pay", `Payments "Core" API`, "container", 1, `Payments "Core" API writes_to Ledger`, "writes_to"),
			},
		},
	}}

	d := Mermaid(impact)
	require.NotNil(t, d)
	assert.Equal(t, 6, d.Nodes)
	assert.Zero(t, d.Omitted)

	golden := filepath.Join("testdata", "impact.mmd")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, []byte(d.Source), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run go test ./internal/prcomment -update to create it")
	assert.Equal(t, string(want), d.Source)
}

func TestMermaid(
	t *testing.T,
) {
	many := api.TouchedElement{Element: element("core", "Core", "container")}
	for i := range 40 {
		many.Dependents = append(many.Dependents, dependent(fmt.Sprintf("d%d", i), fmt.Sprintf("Dep %d", i), "container", 1, "", "uses"))
	}

	testCases := []struct {
		name         string
		impact       *api.BlastRadius
		wantNil      bool
		wantNodes    int
		wantOmitted  int
		wantContains []string
		wantMissing  []string
	}{
		{name: "no impact", impact: nil, wantNil: true},
		{name: "nothing touched", impact: &api.BlastRadius{}, wantNil: true},
		{
			name:         "a lone element",
			impact:       &api.BlastRadius{Elements: []api.TouchedElement{{Element: element("a", "Alone", "system")}}},
			wantNodes:    1,
			wantContains: []string{`n1["<b>Alone</b><br/>system"]`, "class n1 touched", "classDef touched "},
			wantMissing:  []string{"-->", "classDef muted", "classDef system"},
		},
		{
			name:         "capped",
			impact:       &api.BlastRadius{Elements: []api.TouchedElement{many}},
			wantNodes:    maxDiagramNodes,
			wantOmitted:  40 - (maxDiagramNodes - 1),
			wantContains: []string{`n2 -->|"uses"| n1`},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			d := Mermaid(testCase.impact)
			if testCase.wantNil {
				assert.Nil(t, d)
				assert.Empty(t, d.Markdown())
				return
			}
			require.NotNil(t, d)
			assert.Equal(t, testCase.wantNodes, d.Nodes)
			assert.Equal(t, testCase.wantOmitted, d.Omitted)
			for _, s := range testCase.wantContains {
				assert.Contains(t, d.Source, s)
			}
			for _, s := range testCase.wantMissing {
				assert.NotContains(t, d.Source, s)
			}
		})
	}
}

func TestDiagramMarkdown(
	t *testing.T,
) {
	small := &Diagram{Source: "flowchart LR\n", Nodes: 3}
	assert.Equal(t, "**Impact diagram**\n\n```mermaid\nflowchart LR\n```\n", small.Markdown())

	large := &Diagram{Source: "flowchart LR\n", Nodes: 20, Omitted: 4}
	md := large.Markdown()
	assert.True(t, strings.HasPrefix(md, "<details>\n<summary><b>Impact diagram</b> (20 elements)</summary>\n\n```mermaid\n"))
	assert.Contains(t, md, "+4 more not drawn.")
	assert.True(t, strings.HasSuffix(md, "</details>\n"))
}
