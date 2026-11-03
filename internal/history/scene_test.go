package history

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
)

// shopAfter drops the mailer, renames the storefront, adds a warehouse
// called by the API, and moves the cart into the storefront.
func shopAfter() *api.Model {
	m := shop()
	ctrs := m.Systems[0].Containers
	cart := ctrs[1].Components[1]
	ctrs[1].Components = ctrs[1].Components[:1]
	ctrs[0].Name = "Shopfront"
	ctrs[0].Components = []api.Component{cart}
	m.Systems[0].Containers = ctrs[:2]
	m.Systems = append(m.Systems, api.System{ID: "wh", Name: "Warehouse", Type: "software_system"})
	m.Relationships = append(m.Relationships[:2], api.Relationship{ID: "r9", SourceID: "api", TargetID: "wh", Type: "calls", Description: "reserves stock"})
	return m
}

func TestScene(
	t *testing.T,
) {
	s := Compare(shop(), shopAfter(), Options{}).Scene()

	testCases := []struct {
		name       string
		id         string
		wantParent string
		want       diagram.Change
	}{
		{name: "a removed container goes back into its system", id: "mailer", wantParent: "shop", want: diagram.Removed},
		{name: "its children with it", id: "render", wantParent: "tpl", want: diagram.Removed},
		{name: "a renamed container is changed", id: "web", wantParent: "shop", want: diagram.Changed},
		{name: "a moved component sits where it is now", id: "cart", wantParent: "web", want: diagram.Changed},
		{name: "an added system", id: "wh", want: diagram.Added},
		{name: "an unchanged container", id: "api", wantParent: "shop"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			n := s.Graph.Node(testCase.id)
			require.NotNil(t, n)
			if testCase.wantParent != "" {
				require.NotNil(t, n.Parent)
				assert.Equal(t, testCase.wantParent, n.Parent.ID)
			}
			assert.Equal(t, testCase.want, s.Elements[testCase.id])
		})
	}

	assert.Contains(t, s.Links, diagram.Link{From: "api", To: "mailer", Change: diagram.Removed})
	assert.Contains(t, s.Links, diagram.Link{From: "api", To: "wh", Change: diagram.Added})
	assert.Contains(t, s.Links, diagram.Link{From: "web", To: "api"})
	assert.Len(t, s.Model.Relationships, 5, "now's three and the two removed")
}

func TestSceneKeepsIDsApart(
	t *testing.T,
) {
	before := &api.Model{Systems: []api.System{{ID: "s", Name: "Shop", Containers: []api.Container{{ID: "x", Name: "Old"}}}}}
	after := &api.Model{Systems: []api.System{{ID: "s", Name: "Shop"}, {ID: "x", Name: "New"}}}

	s := Compare(before, after, Options{}).Scene()

	assert.Equal(t, diagram.Added, s.Elements["x"], "the system that reuses the ID")
	assert.Equal(t, diagram.Removed, s.Elements["removed:x"], "the container that had it")
	assert.Equal(t, "Old", s.Graph.Node("removed:x").Name)
}

func TestSceneView(
	t *testing.T,
) {
	s := Compare(shop(), shopAfter(), Options{}).Scene()
	testCases := []struct {
		name      string
		scope     Scope
		wantTitle string
		wantNodes map[string]diagram.Change
	}{
		{
			name:      "the containers of the one system",
			scope:     DefaultScope(s.Graph),
			wantTitle: "Containers of Shop",
			wantNodes: map[string]diagram.Change{"web": diagram.Changed, "mailer": diagram.Removed, "api": "", "wh": diagram.Added},
		},
		{
			name:      "centred on an element",
			scope:     Scope{ElementID: "wh"},
			wantTitle: "Around Warehouse",
			wantNodes: map[string]diagram.Change{"wh": diagram.Added, "shop": diagram.Changed},
		},
		{
			name:      "an element the scene lacks",
			scope:     Scope{ElementID: "nope", Inside: true},
			wantTitle: "System context",
			wantNodes: map[string]diagram.Change{"shop": diagram.Changed, "wh": diagram.Added, "pay": ""},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := s.View(testCase.scope)
			assert.Equal(t, testCase.wantTitle, v.Title)
			for id, want := range testCase.wantNodes {
				n := v.Node(id)
				require.NotNil(t, n, id)
				assert.Equal(t, want, n.Change, id)
			}
		})
	}
}

func TestDefaultScope(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		model *api.Model
		want  Scope
	}{
		{name: "one system with containers", model: shop(), want: Scope{ElementID: "shop", Inside: true}},
		{name: "systems only", model: &api.Model{Systems: []api.System{{ID: "a"}, {ID: "b"}}}},
		{name: "several systems with containers", model: &api.Model{Systems: []api.System{
			{ID: "a", Containers: []api.Container{{ID: "a1"}}},
			{ID: "b", Containers: []api.Container{{ID: "b1"}}},
		}}},
		{name: "empty", model: &api.Model{}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, DefaultScope(graph.New(testCase.model)))
		})
	}
}

func TestSceneViewShowsUnlinkedChanges(
	t *testing.T,
) {
	after := shop()
	after.Systems = append(after.Systems, api.System{ID: "audit", Name: "Audit", Type: "software_system"})

	v := Compare(shop(), after, Options{}).Scene().View(Scope{ElementID: "shop", Inside: true})

	n := v.Node("audit")
	require.NotNil(t, n, "an added system no arrow reaches still shows")
	assert.Equal(t, diagram.Added, n.Change)
}
