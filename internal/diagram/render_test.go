package diagram

import (
	"bytes"
	"flag"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(
	t *testing.T,
	name, got string,
) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test ./internal/diagram -update to create it")
	assert.Equal(t, string(want), got)
}

func TestTextGolden(
	t *testing.T,
) {
	g := shopGraph()
	impact := Highlight{"charge": 0, "checkout": 1, "web": 2}
	testCases := []struct {
		name string
		view *View
		opts TextOptions
	}{
		{name: "context.txt", view: For(g, nil)},
		{name: "containers.txt", view: For(g, g.Node("shop"))},
		{name: "components.txt", view: For(g, g.Node("api"))},
		{name: "impact.txt", view: Around(g, impact), opts: TextOptions{Highlight: impact}},
		{name: "narrow.txt", view: For(g, g.Node("shop")), opts: TextOptions{Width: 40}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			golden(t, testCase.name, Text(testCase.view, testCase.opts)+"\n")
		})
	}
}

func TestTextColorKeepsTheDrawing(
	t *testing.T,
) {
	g := shopGraph()
	v := For(g, g.Node("shop"))
	plain := Text(v, TextOptions{})
	colored := Text(v, TextOptions{Theme: ui.NewTheme(true), Color: true, Highlight: Highlight{"web": 0}})
	assert.NotEqual(t, plain, colored)
	assert.Contains(t, ansi.Strip(colored), "┃ ▣ Storefront    ┃", "touched boxes are heavy")
}

func TestArrange(
	t *testing.T,
) {
	g := shopGraph()
	for _, v := range []*View{For(g, nil), For(g, g.Node("shop")), For(g, g.Node("api")), cyclic()} {
		t.Run(v.Title, func(t *testing.T) {
			l := ArrangeText(v, TextBoxMax)
			again := ArrangeText(v, TextBoxMax)
			assert.Equal(t, l.Boxes, again.Boxes, "deterministic")
			assert.Equal(t, l.Routes, again.Routes, "deterministic")
			require.Len(t, l.Boxes, len(v.Nodes))
			require.Len(t, l.Routes, len(v.Edges))

			for i, a := range l.Boxes {
				assert.True(t, a.X >= 0 && a.Y >= 0 && a.X+a.W <= l.Width && a.Y+a.H <= l.Height, "%s inside the canvas", a.Name)
				for _, b := range l.Boxes[i+1:] {
					overlap := a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
					assert.False(t, overlap, "%s and %s overlap", a.Name, b.Name)
				}
			}
			for _, r := range l.Routes {
				require.GreaterOrEqual(t, len(r.Points), 2)
				for i := 1; i < len(r.Points); i++ {
					p, q := r.Points[i-1], r.Points[i]
					assert.True(t, p.X == q.X || p.Y == q.Y, "orthogonal segments")
				}
				from, to := l.Box(r.From), l.Box(r.To)
				start, end := r.Points[0], r.Points[len(r.Points)-1]
				assert.True(t, adjacent(from, start), "%s starts next to %s", r.Label, from.Name)
				assert.True(t, adjacent(to, end), "%s ends next to %s", r.Label, to.Name)
			}
		})
	}
}

// adjacent reports whether p is the cell just above or below the box.
func adjacent(b *Box, p Point) bool {
	return p.X > b.X && p.X < b.X+b.W-1 && (p.Y == b.Y-1 || p.Y == b.Y+b.H)
}

// cyclic is a view whose arrows loop.
func cyclic() *View {
	g := graph.New(&api.Model{
		Systems: []api.System{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"}, {ID: "d", Name: "Loner"}},
		Relationships: []api.Relationship{
			{SourceID: "a", TargetID: "b", Type: "calls"},
			{SourceID: "b", TargetID: "c", Type: "calls"},
			{SourceID: "c", TargetID: "a", Type: "calls"},
		},
	})
	return For(g, nil)
}

func TestArrangeEmpty(
	t *testing.T,
) {
	l := ArrangeText(&View{}, TextBoxMax)
	assert.Empty(t, l.Boxes)
	assert.Empty(t, RenderText(l, TextOptions{}))
}

func TestMermaidGolden(
	t *testing.T,
) {
	g := shopGraph()
	impact := Highlight{"charge": 0, "checkout": 1, "web": 2}
	golden(t, "containers.mmd", Mermaid(For(g, g.Node("shop")), nil))
	golden(t, "impact.mmd", Mermaid(Around(g, impact), impact))
}

func TestMermaidEscapes(
	t *testing.T,
) {
	g := graph.New(&api.Model{Systems: []api.System{{ID: "a", Name: `Say "hi" <now>`}}})
	out := Mermaid(For(g, nil), nil)
	assert.Contains(t, out, "Say #quot;hi#quot; #lt;now#gt;")
	assert.NotContains(t, out, `"hi"`)
}

func TestImages(
	t *testing.T,
) {
	g := shopGraph()
	v := For(g, g.Node("shop"))
	for _, dark := range []bool{true, false} {
		opts := ImageOptions{Theme: ui.NewTheme(dark), Dark: dark, Highlight: Highlight{"web": 0, "wh": 1}}
		data, width, err := PNG(v, opts)
		require.NoError(t, err)
		img, err := png.Decode(bytes.NewReader(data))
		require.NoError(t, err)
		assert.Equal(t, width*imageScale, img.Bounds().Dx(), "rendered at twice the logical width")
		assert.Greater(t, img.Bounds().Dy(), 200)

		var svg bytes.Buffer
		require.NoError(t, SVG(&svg, v, opts))
		out := svg.String()
		assert.True(t, strings.HasPrefix(out, "<svg "))
		assert.Contains(t, out, ">Fulfilment Worker</text>")
		assert.Contains(t, out, "stroke-dasharray", "external systems are dashed")
		assert.Contains(t, out, ">touched</text>", "a highlight brings its legend")
	}
}

func TestMermaidNewView(
	t *testing.T,
) {
	v := NewView("impact", "Impact", []Node{{ID: "a", Name: "A", Level: "container"}, {ID: "b", Name: "B", Level: "component"}},
		[]Edge{{From: "b", To: "a", Indirect: true, Count: 1}})
	out := MermaidWith(v, Highlight{"a": 0, "b": 3}, MermaidOptions{Direction: "LR"})
	assert.True(t, strings.HasPrefix(out, "flowchart LR\n"))
	assert.Contains(t, out, "n2 -.-> n1")
	assert.Contains(t, out, "class n2 distant")
	assert.NotContains(t, out, "linkStyle", "a dotted path is not accented")
	assert.NotContains(t, out, "classDef muted", "only the classes in use are defined")
}
