package diagram

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFocus(
	t *testing.T,
) {
	g := shopGraph()
	testCases := []struct {
		name      string
		element   string
		depth     int
		wantNodes []string
		wantEdges []string
		wantHops  Highlight
	}{
		{
			name:      "a system folds in everything it holds",
			element:   "shop",
			depth:     1,
			wantNodes: []string{"shop", "pay", "wh", "mail"},
			wantEdges: []string{"shop->pay", "shop<->wh", "shop->mail"},
			wantHops:  Highlight{"shop": 0, "pay": 1, "wh": 1, "mail": 1},
		},
		{
			name:      "a container sees components of its neighbours as their container",
			element:   "db",
			depth:     1,
			wantNodes: []string{"db", "api", "worker"},
			wantEdges: []string{"api->db", "worker->db"},
			wantHops:  Highlight{"db": 0, "api": 1, "worker": 1},
		},
		{
			name:      "a component keeps shallower neighbours and skips its own container",
			element:   "checkout",
			depth:     1,
			wantNodes: []string{"checkout", "web", "payclient", "orders"},
			wantEdges: []string{"web->checkout", "checkout->payclient", "checkout->orders"},
		},
		{
			name:      "code shows what it calls",
			element:   "charge",
			depth:     1,
			wantNodes: []string{"charge", "pay"},
			wantEdges: []string{"charge->pay"},
		},
		{
			name:      "two hops reach the neighbours' neighbours",
			element:   "db",
			depth:     2,
			wantNodes: []string{"db", "api", "worker", "web", "pay", "wh", "mail"},
			wantHops:  Highlight{"db": 0, "api": 1, "worker": 1, "web": 2, "pay": 2, "wh": 2, "mail": 2},
		},
		{
			name:      "the depth is clamped",
			element:   "charge",
			depth:     0,
			wantNodes: []string{"charge", "pay"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := Focus(g, g.Node(testCase.element), testCase.depth)
			assert.Equal(t, KindFocus, v.Kind)
			assert.Equal(t, testCase.element, v.FocusID)
			assert.Empty(t, v.ScopeID)
			assert.ElementsMatch(t, testCase.wantNodes, nodeIDs(v))
			assert.Equal(t, testCase.element, v.Nodes[0].ID, "the focus comes first")
			if testCase.wantEdges != nil {
				assert.ElementsMatch(t, testCase.wantEdges, edgePairs(v))
			}
			if testCase.wantHops != nil {
				assert.Equal(t, testCase.wantHops, v.Hops())
			}
		})
	}
}

func TestFocusMembersAndContext(
	t *testing.T,
) {
	g := shopGraph()
	v := Focus(g, g.Node("checkout"), 1)

	assert.Equal(t, "payclient", v.Rep("charge"), "code is drawn as its component")
	assert.Empty(t, v.Rep("api"), "the focus's own container has no box")
	assert.Equal(t, "Shop", v.Node("web").Parent, "a neighbour from elsewhere names its parent")
	assert.Empty(t, v.Node("orders").Parent, "a sibling does not")
	assert.Contains(t, boxSubtitle(*v.Node("web")), "in Shop")

	merged := Focus(g, g.Node("db"), 1)
	for _, e := range merged.Edges {
		if e.From == "api" {
			assert.Equal(t, 2, e.Count, "two component arrows lift onto one")
		}
	}
	assert.Nil(t, For(g, nil).Hops(), "only focus views have hops")
}

func TestTrim(
	t *testing.T,
) {
	g := shopGraph()
	v := Focus(g, g.Node("db"), 2)
	require.Len(t, v.Nodes, 7)

	testCases := []struct {
		name  string
		limit int
		want  []string
	}{
		{name: "within the limit, untouched", limit: 10, want: []string{"db", "api", "worker", "web", "pay", "wh", "mail"}},
		{name: "the focus, then the nearest and most connected", limit: 3, want: []string{"db", "api", "worker"}},
		{name: "the focus alone", limit: 1, want: []string{"db"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			trimmed := Trim(v, testCase.limit)
			assert.ElementsMatch(t, testCase.want, nodeIDs(trimmed))
			kept := map[string]bool{}
			for _, id := range nodeIDs(trimmed) {
				kept[id] = true
			}
			for _, e := range trimmed.Edges {
				assert.True(t, kept[e.From] && kept[e.To], "no arrow to a dropped box")
			}
			for id := range trimmed.Hops() {
				assert.True(t, kept[id])
			}
			assert.Equal(t, v.FocusID, trimmed.FocusID)
		})
	}
}

func TestDrop(
	t *testing.T,
) {
	g := shopGraph()
	v := Focus(g, g.Node("api"), 1)
	dropped := Drop(v, func(n Node) bool { return n.Kind == "database" })
	assert.NotContains(t, nodeIDs(dropped), "db")
	assert.Contains(t, nodeIDs(dropped), "api")
	for _, e := range dropped.Edges {
		assert.NotEqual(t, "db", e.From)
		assert.NotEqual(t, "db", e.To)
	}
	assert.Same(t, v, Drop(v, func(Node) bool { return false }), "nothing dropped, same view")
}
