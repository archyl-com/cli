package diagram

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func nodeIDs(v *View) []string {
	ids := make([]string, len(v.Nodes))
	for i, n := range v.Nodes {
		ids[i] = n.ID
	}
	return ids
}

func edgePairs(v *View) []string {
	out := make([]string, len(v.Edges))
	for i, e := range v.Edges {
		arrow := "->"
		if e.Both {
			arrow = "<->"
		}
		out[i] = e.From + arrow + e.To
	}
	return out
}

func TestFor(
	t *testing.T,
) {
	g := shopGraph()
	testCases := []struct {
		name      string
		element   string
		wantKind  string
		wantScope string
		wantNodes []string
		wantEdges []string
	}{
		{
			name:      "nothing gives the system context",
			wantKind:  KindContext,
			wantNodes: []string{"shop", "pay", "mail", "wh"},
			wantEdges: []string{"shop->pay", "shop<->wh", "shop->mail"},
		},
		{
			name:      "a system gives its containers and the systems around",
			element:   "shop",
			wantKind:  KindContainer,
			wantScope: "shop",
			wantNodes: []string{"web", "api", "worker", "db", "pay", "wh", "mail"},
			wantEdges: []string{"web->api", "api->db", "api->pay", "worker->db", "worker->wh", "worker->mail", "wh->api"},
		},
		{
			name:      "a container gives its components and its neighbours",
			element:   "api",
			wantKind:  KindComponent,
			wantScope: "api",
			wantNodes: []string{"checkout", "catalog", "payclient", "orders", "web", "db", "pay"},
		},
		{
			name:      "a component shows the view it sits in",
			element:   "checkout",
			wantKind:  KindComponent,
			wantScope: "api",
		},
		{
			name:      "code shows its container's components",
			element:   "charge",
			wantKind:  KindComponent,
			wantScope: "api",
		},
		{
			name:      "a container without components shows its system",
			element:   "db",
			wantKind:  KindContainer,
			wantScope: "shop",
		},
		{
			name:     "a system without containers shows the context",
			element:  "pay",
			wantKind: KindContext,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := For(g, g.Node(testCase.element))
			assert.Equal(t, testCase.wantKind, v.Kind)
			assert.Equal(t, testCase.wantScope, v.ScopeID)
			if testCase.wantNodes != nil {
				assert.Equal(t, testCase.wantNodes, nodeIDs(v))
			}
			if testCase.wantEdges != nil {
				assert.Equal(t, testCase.wantEdges, edgePairs(v))
			}
		})
	}
}

func TestForMergesRelationships(
	t *testing.T,
) {
	g := shopGraph()
	v := For(g, g.Node("shop"))
	var webToAPI Edge
	for _, e := range v.Edges {
		if e.From == "web" && e.To == "api" {
			webToAPI = e
		}
	}
	assert.Equal(t, 2, webToAPI.Count, "two component calls lift onto one container arrow")
	assert.Equal(t, "places orders", webToAPI.Label)

	worker := v.Node("worker")
	require.NotNil(t, worker)
	assert.False(t, worker.External)
	assert.True(t, v.Node("wh").External, "systems around the scope are drawn as external")
	assert.Equal(t, 4, v.Node("api").Children)
}

func TestAround(
	t *testing.T,
) {
	g := shopGraph()
	testCases := []struct {
		name      string
		highlight Highlight
		wantKind  string
		wantScope string
		wantLift  Highlight
	}{
		{
			name:      "code and a component of one container",
			highlight: Highlight{"charge": 0, "checkout": 0, "web": 1},
			wantKind:  KindComponent,
			wantScope: "api",
			wantLift:  Highlight{"payclient": 0, "checkout": 0, "web": 1},
		},
		{
			name:      "two containers of one system",
			highlight: Highlight{"web": 0, "checkout": 0, "wh": 2},
			wantKind:  KindContainer,
			wantScope: "shop",
			wantLift:  Highlight{"web": 0, "api": 0, "wh": 2},
		},
		{
			name:      "two systems",
			highlight: Highlight{"shop": 0, "wh": 0},
			wantKind:  KindContext,
			wantLift:  Highlight{"shop": 0, "wh": 0},
		},
		{
			name:      "a container without components, the closest distance wins",
			highlight: Highlight{"worker": 0, "catalog": 2, "checkout": 1},
			wantKind:  KindContainer,
			wantScope: "shop",
			wantLift:  Highlight{"worker": 0, "api": 1},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := Around(g, testCase.highlight)
			assert.Equal(t, testCase.wantKind, v.Kind)
			assert.Equal(t, testCase.wantScope, v.ScopeID)
			if testCase.wantLift != nil {
				assert.Equal(t, testCase.wantLift, v.Lift(testCase.highlight))
			}
		})
	}
}

func TestAroundAddsUnlinkedDependents(
	t *testing.T,
) {
	g := shopGraph()
	v := Around(g, Highlight{"checkout": 0, "mail": 3})
	assert.NotNil(t, v.Node("mail"), "a dependent with no arrow into the view still gets a box")
}

func TestFromImpact(
	t *testing.T,
) {
	r := &api.LocateResult{
		Files: []api.FileLocation{{Path: "a.go", Elements: []api.LocatedElement{{ID: "checkout"}, {ID: "api"}}}},
		Impact: &api.BlastRadius{Elements: []api.TouchedElement{{
			Element: api.Element{ID: "orders"},
			Dependents: []api.ImpactedElement{
				{Element: api.Element{ID: "checkout"}, Distance: 1},
				{Element: api.Element{ID: "web"}, Distance: 2},
			},
		}}},
	}
	assert.Equal(t, Highlight{"checkout": 0, "orders": 0, "web": 2}, FromImpact(r))
	assert.Empty(t, FromImpact(nil))
}
