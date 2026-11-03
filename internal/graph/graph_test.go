package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func sample() *api.Model {
	return &api.Model{
		Systems: []api.System{{
			ID: "s", Name: "Billing",
			Containers: []api.Container{
				{ID: "api", Name: "API", Components: []api.Component{
					{ID: "inv", Name: "Invoicing Service", CodeElements: []api.CodeElement{{ID: "fn", Name: "Invoice"}}},
				}},
				{ID: "web", Name: "Checkout Web"},
				{ID: "adm", Name: "Admin"},
			},
		}},
		Relationships: []api.Relationship{
			{ID: "r1", SourceID: "web", TargetID: "inv", Type: "calls"},
			{ID: "r2", SourceID: "adm", TargetID: "web", Type: "embeds"},
			{ID: "r3", SourceID: "ghost", TargetID: "inv", Type: "calls"},
		},
	}
}

func TestGraph(
	t *testing.T,
) {
	g := New(sample())

	inv := g.Node("inv")
	require.NotNil(t, inv)
	assert.Equal(t, "Billing / API / Invoicing Service", inv.PathString())
	assert.Equal(t, map[string]int{"system": 1, "container": 3, "component": 1, "code": 1}, g.Count())
	assert.Len(t, g.Incoming("inv"), 1, "relationships to unknown elements are dropped")
	assert.Equal(t, "web", g.Outgoing("adm")[0].Other.ID)

	hops := g.Dependents("inv", 2)
	require.Len(t, hops, 2)
	assert.Equal(t, "Checkout Web", hops[0].Node.Name)
	assert.Equal(t, 2, hops[1].Distance)
	assert.Len(t, g.Dependents("inv", 1), 1)
}

func TestFind(
	t *testing.T,
) {
	g := New(sample())
	testCases := []struct {
		name string
		ref  string
		want string
	}{
		{name: "by id", ref: "inv", want: "Invoicing Service"},
		{name: "exact name, any case", ref: "api", want: "API"},
		{name: "hash prefix", ref: "#Admin", want: "Admin"},
		{name: "partial matches prefer the less specific level", ref: "invoic", want: "Invoicing Service"},
		{name: "an exact match beats a partial one", ref: "invoice", want: "Invoice"},
		{name: "unknown", ref: "nope", want: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			n, _ := g.Find(testCase.ref)
			if testCase.want == "" {
				assert.Nil(t, n)
				return
			}
			require.NotNil(t, n)
			assert.Equal(t, testCase.want, n.Name)
		})
	}
}
