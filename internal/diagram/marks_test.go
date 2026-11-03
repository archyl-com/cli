package diagram

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

// markedContainers is the shop's containers with a few changes: the worker
// added, the database changed, the fulfilment arrow to the warehouse
// removed and a component of the API changed.
func markedContainers(t *testing.T) *View {
	t.Helper()
	g := shopGraph()
	v := For(g, g.Node("shop"))
	v.MarkChanges(
		map[string]Change{"worker": Added, "db": Changed, "catalog": Changed, "mail": Removed},
		[]Link{
			{From: "worker", To: "db", Change: Added},
			{From: "worker", To: "wh", Change: Removed},
			{From: "worker", To: "mail", Change: Removed},
			{From: "catalog", To: "db", Change: ""},
			{From: "orders", To: "db", Change: Changed},
			{From: "web", To: "checkout", Change: ""},
			{From: "web", To: "catalog", Change: ""},
		},
	)
	return v
}

func TestMarkChanges(
	t *testing.T,
) {
	v := markedContainers(t)
	testCases := []struct {
		name string
		got  func() Change
		want Change
	}{
		{name: "an added box", got: func() Change { return v.Node("worker").Change }, want: Added},
		{name: "a changed box", got: func() Change { return v.Node("db").Change }, want: Changed},
		{name: "a removed box", got: func() Change { return v.Node("mail").Change }, want: Removed},
		{name: "a box holding a changed element", got: func() Change { return v.Node("api").Change }, want: Changed},
		{name: "an unchanged box", got: func() Change { return v.Node("web").Change }, want: ""},
		{name: "an arrow of added relationships", got: func() Change { return edge(t, v, "worker", "db").Change }, want: Added},
		{name: "an arrow of removed relationships", got: func() Change { return edge(t, v, "worker", "wh").Change }, want: Removed},
		{name: "an arrow mixing changes", got: func() Change { return edge(t, v, "api", "db").Change }, want: Changed},
		{name: "an arrow without changes", got: func() Change { return edge(t, v, "web", "api").Change }, want: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.got())
		})
	}
}

func edge(
	t *testing.T,
	v *View,
	from string,
	to string,
) Edge {
	t.Helper()
	for _, e := range v.Edges {
		if (e.From == from && e.To == to) || (e.From == to && e.To == from) {
			return e
		}
	}
	require.Failf(t, "no arrow", "%s → %s", from, to)
	return Edge{}
}

func TestMarked(
	t *testing.T,
) {
	g := shopGraph()
	testCases := []struct {
		name string
		mark func(v *View)
		want bool
	}{
		{name: "nothing marked", mark: func(*View) {}, want: false},
		{name: "a box", mark: func(v *View) { v.Nodes[0].Change = Added }, want: true},
		{name: "an arrow", mark: func(v *View) { v.Edges[0].Change = Removed }, want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := For(g, g.Node("shop"))
			testCase.mark(v)
			assert.Equal(t, testCase.want, v.Marked())
		})
	}
}

func TestBadge(
	t *testing.T,
) {
	testCases := []struct {
		change Change
		want   string
	}{
		{change: Added, want: "+"},
		{change: Removed, want: "−"},
		{change: Changed, want: "~"},
		{change: "", want: ""},
	}
	for _, testCase := range testCases {
		t.Run(string(testCase.change), func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.change.Badge())
		})
	}
}

func TestTrimKeepsChangesFirst(
	t *testing.T,
) {
	g := shopGraph()
	v := For(g, g.Node("shop"))
	v.MarkChanges(map[string]Change{"mail": Removed}, nil)

	trimmed := Trim(v, 2)

	require.Len(t, trimmed.Nodes, 2)
	kept := trimmed.Node("mail")
	require.NotNil(t, kept, "a changed box outranks better connected ones")
	assert.Equal(t, Removed, kept.Change, "marks survive the trim")
}

func TestMarkedText(
	t *testing.T,
) {
	v := markedContainers(t)
	testCases := []struct {
		name string
		opts TextOptions
		want []string
	}{
		{
			name: "plain",
			want: []string{"━ + ━", "━ ~ ━", "╌ − ╌", "╎"},
		},
		{
			name: "coloured",
			opts: TextOptions{Theme: ui.NewTheme(true), Color: true},
			want: []string{"━ + ━", "╌ − ╌"},
		},
		{
			name: "too narrow for the drawing",
			opts: TextOptions{Width: 30},
			want: []string{"+ ▣ Fulfilment Worker", "− ◆ Mailgun", "+→ Orders DB", "−→ Mailgun"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := ansi.Strip(Text(v, testCase.opts))
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
		})
	}
}

func TestUnmarkedTextKeepsItsLook(
	t *testing.T,
) {
	g := shopGraph()
	v := For(g, g.Node("shop"))
	before := Text(v, TextOptions{})
	v.MarkChanges(nil, nil)
	assert.Equal(t, before, Text(v, TextOptions{}), "a view without changes draws as before")
}

func TestMarkedImages(
	t *testing.T,
) {
	v := markedContainers(t)
	opts := ImageOptions{Theme: ui.NewTheme(false)}

	var svg bytes.Buffer
	require.NoError(t, SVG(&svg, v, opts))
	out := svg.String()
	assert.Contains(t, out, `<polyline`, "arrows are drawn")
	assert.Contains(t, out, `stroke-dasharray="6 4"/>`, "removed arrows are dashed")
	assert.Contains(t, out, ">+</text>", "added boxes carry a badge")
	assert.Contains(t, out, ">−</text>")
	assert.Contains(t, out, ">removed</text>", "the legend names the changes")
	assert.Contains(t, out, "#16a34a", "added is green")

	img, width, err := PNG(v, opts)
	require.NoError(t, err)
	assert.NotEmpty(t, img)
	assert.Positive(t, width)
}

func TestMarkedMermaid(
	t *testing.T,
) {
	v := markedContainers(t)

	out := Mermaid(v, nil)

	for _, w := range []string{
		"classDef added ", "classDef changed ", "classDef removed ",
		"<b>+ Fulfilment Worker</b>", "<b>− Mailgun</b>", "<b>~ Orders DB</b>",
		"linkStyle", "stroke:#16a34a",
	} {
		assert.Contains(t, out, w)
	}
	assert.Contains(t, out, " -.->", "removed arrows are dotted")
	assert.Equal(t, 0, strings.Count(Mermaid(For(shopGraph(), nil), nil), "linkStyle"), "unmarked views have no link styles")
}

func TestForIncluding(
	t *testing.T,
) {
	g := graph.New(&api.Model{Systems: []api.System{
		{ID: "shop", Name: "Shop", Containers: []api.Container{{ID: "web", Name: "Web"}}},
		{ID: "lone", Name: "Lone", Containers: []api.Container{{ID: "lone-db", Name: "DB"}}},
	}})
	testCases := []struct {
		name  string
		extra []string
		want  []string
	}{
		{name: "as For", want: []string{"web"}},
		{name: "an unlinked system", extra: []string{"lone"}, want: []string{"web", "lone"}},
		{name: "an element inside it shows as its system", extra: []string{"lone-db"}, want: []string{"web", "lone"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := ForIncluding(g, g.Node("shop"), testCase.extra)
			var ids []string
			for _, n := range v.Nodes {
				ids = append(ids, n.ID)
			}
			assert.Equal(t, testCase.want, ids)
		})
	}
}
