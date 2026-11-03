package incident

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlastRadius(
	t *testing.T,
) {
	testCases := []struct {
		name             string
		element          string
		depth            int
		wantDependents   []string
		wantDependencies []string
	}{
		{
			name:             "a container: its components' relationships count as its own",
			element:          "api",
			depth:            1,
			wantDependents:   []string{"Admin@1", "Mobile App@1", "Storefront@1"},
			wantDependencies: []string{"Stripe@1", "Orders DB@1"},
		},
		{
			name:             "two hops reach who uses the dependents",
			element:          "api",
			depth:            2,
			wantDependents:   []string{"Admin@1", "Mobile App@1", "Storefront@1", "Customer@2"},
			wantDependencies: []string{"Stripe@1", "Orders DB@1"},
		},
		{
			name:             "a component never lists its own container",
			element:          "handlers",
			depth:            2,
			wantDependents:   []string{"Admin@1"},
			wantDependencies: []string{"Stripe@1"},
		},
		{
			name:             "a system leaves out everything inside it",
			element:          "s",
			depth:            2,
			wantDependents:   []string{"Customer@1", "Admin@1"},
			wantDependencies: []string{"Stripe@1"},
		},
		{
			name:             "nothing around a code element",
			element:          "place",
			depth:            2,
			wantDependents:   []string{},
			wantDependencies: []string{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			dependents, dependencies := BlastRadius(g, g.Node(testCase.element), testCase.depth)
			assert.Equal(t, testCase.wantDependents, names(dependents))
			assert.Equal(t, testCase.wantDependencies, names(dependencies))
		})
	}
}

func TestNewPlan(
	t *testing.T,
) {
	testCases := []struct {
		name               string
		clients            int
		depth              int
		wantDepth          int
		wantDependents     int
		wantMoreDependents int
	}{
		{
			name:           "within the cap",
			clients:        3,
			depth:          2,
			wantDepth:      2,
			wantDependents: 3,
		},
		{
			name:               "over the cap keeps the closest and counts the rest",
			clients:            MaxDependents + 3,
			depth:              1,
			wantDepth:          1,
			wantDependents:     MaxDependents,
			wantMoreDependents: 3,
		},
		{
			name:           "depth is clamped",
			clients:        1,
			depth:          9,
			wantDepth:      MaxDepth,
			wantDependents: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := fanIn(testCase.clients)
			plan := NewPlan(g, g.Node("api"), testCase.depth)
			assert.Equal(t, testCase.wantDepth, plan.Depth)
			assert.Len(t, plan.Dependents, testCase.wantDependents)
			assert.Equal(t, testCase.wantMoreDependents, plan.MoreDependents)
		})
	}
}

func TestClampDepth(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		depth int
		want  int
	}{
		{name: "below one", depth: 0, want: 1},
		{name: "in range", depth: 3, want: 3},
		{name: "above the max", depth: 7, want: MaxDepth},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ClampDepth(testCase.depth))
		})
	}
}

func TestPlanTargets(
	t *testing.T,
) {
	testCases := []struct {
		name              string
		element           string
		wantOwners        []string
		wantChanges       []string
		wantReleaseScopes []string
		wantDocs          []string
	}{
		{
			name:              "a component",
			element:           "handlers",
			wantOwners:        []string{"handlers", "api", "s", "admin", "bo", "pay"},
			wantChanges:       []string{"handlers", "pay"},
			wantReleaseScopes: []string{"api", "s"},
			wantDocs:          []string{"handlers", "api", "s"},
		},
		{
			name:              "a code element has no owners of its own",
			element:           "place",
			wantOwners:        []string{"handlers", "api", "s"},
			wantChanges:       []string{"place"},
			wantReleaseScopes: []string{"api", "s"},
			wantDocs:          []string{"place", "handlers", "api", "s"},
		},
		{
			name:              "a system is its own release scope",
			element:           "pay",
			wantOwners:        []string{"pay", "handlers", "api", "s"},
			wantChanges:       []string{"pay"},
			wantReleaseScopes: []string{"pay"},
			wantDocs:          []string{"pay"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			n := g.Node(testCase.element)
			require.NotNil(t, n)
			plan := NewPlan(g, n, 1)
			assert.Equal(t, testCase.wantOwners, ids(plan.OwnerTargets()))
			assert.Equal(t, testCase.wantChanges, ids(plan.ChangeTargets()))
			assert.Equal(t, testCase.wantReleaseScopes, ids(plan.ReleaseScopes()))
			assert.Equal(t, testCase.wantDocs, ids(plan.DocTargets()))
		})
	}
}
