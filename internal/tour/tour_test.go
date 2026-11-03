package tour

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// shop is a customer, a shop with four containers and the API's
// components, and the systems around it.
func shop() *graph.Graph {
	return graph.New(&api.Model{
		Systems: []api.System{
			{ID: "customer", Name: "Customer", Type: "person"},
			{ID: "shop", Name: "Shop", Type: "software_system", Description: "Sells products online.", Containers: []api.Container{
				{ID: "web", Name: "Storefront", ContainerType: "web_app"},
				{ID: "api", Name: "Shop API", ContainerType: "api", Components: []api.Component{
					{ID: "checkout", Name: "Checkout", ComponentType: "service", FilePath: "services/api/checkout.go"},
					{ID: "catalog", Name: "Catalog", ComponentType: "service"},
				}},
				{ID: "worker", Name: "Fulfilment Worker", ContainerType: "worker"},
				{ID: "db", Name: "Orders DB", ContainerType: "database"},
			}},
			{ID: "pay", Name: "Stripe", IsExternal: true},
			{ID: "wh", Name: "Warehouse", IsExternal: true},
		},
		Relationships: []api.Relationship{
			{ID: "r0", SourceID: "customer", TargetID: "web", Type: "uses", Description: "browses and buys"},
			{ID: "r1", SourceID: "web", TargetID: "checkout", Type: "calls", Description: "places orders"},
			{ID: "r2", SourceID: "web", TargetID: "catalog", Type: "calls"},
			{ID: "r3", SourceID: "checkout", TargetID: "db", Type: "writes_to"},
			{ID: "r4", SourceID: "checkout", TargetID: "pay", Type: "calls", Description: "charges cards"},
			{ID: "r5", SourceID: "worker", TargetID: "db", Type: "reads_from"},
			{ID: "r6", SourceID: "worker", TargetID: "wh", Type: "calls", Description: "ships orders"},
			{ID: "r7", SourceID: "wh", TargetID: "api", Type: "calls", Description: "updates stock"},
		},
	})
}

func stopNames(t *Tour) []string {
	var out []string
	for _, s := range t.Stops {
		out = append(out, s.Kind+":"+s.Name)
	}
	return out
}

func TestPlan(
	t *testing.T,
) {
	g := shop()
	testCases := []struct {
		name      string
		focus     string
		max       int
		wantStops []string
		wantTrace []string
	}{
		{
			name: "one system: its containers, where work comes in first, no database",
			wantStops: []string{"overview:Shop", "element:Shop API", "element:Storefront",
				"element:Fulfilment Worker", "recap:Recap"},
			wantTrace: []string{"Warehouse", "Shop API"},
		},
		{
			name:      "fewer stops keep the most connected",
			max:       2,
			wantStops: []string{"overview:Shop", "element:Shop API", "element:Storefront", "recap:Recap"},
		},
		{
			name:      "a container and its components, the container first",
			focus:     "api",
			wantStops: []string{"overview:Shop API", "element:Shop API", "element:Checkout", "element:Catalog", "recap:Recap"},
		},
		{
			name:      "a system tours its containers",
			focus:     "shop",
			wantStops: []string{"overview:Shop", "element:Shop API", "element:Storefront", "element:Fulfilment Worker", "recap:Recap"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var focus *graph.Node
			if testCase.focus != "" {
				focus = g.Node(testCase.focus)
			}
			tr, _ := Plan(g, Options{Project: "Shop", Focus: focus, MaxStops: testCase.max})
			assert.Equal(t, testCase.wantStops, stopNames(tr))
			if testCase.wantTrace != nil {
				assert.Equal(t, testCase.wantTrace, tr.Trace)
			}
		})
	}
}

func TestPlanStops(
	t *testing.T,
) {
	tr, _ := Plan(shop(), Options{Project: "Shop"})
	overview := tr.Stops[0]
	assert.Equal(t, []string{"Customer"}, overview.People)
	assert.ElementsMatch(t, []string{"Stripe", "Warehouse"}, overview.Externals)
	assert.Equal(t, []string{"Shop"}, overview.Systems)
	assert.Equal(t, "Sells products online.", overview.Description)

	api := tr.Stops[1]
	assert.Equal(t, "Where work comes in from outside: Warehouse sends work to it.", api.Why)
	assert.Contains(t, api.Uses, Link{Name: "Stripe", Label: "charges cards"}, "outsiders show on the stop")
	assert.Contains(t, api.UsedBy, Link{Name: "Warehouse", Label: "updates stock"})
	assert.Equal(t, "Checkout", api.KeyParts[0].Name, "the most connected part first")

	web := tr.Stops[2]
	assert.Equal(t, []Link{{Name: "Shop API", Label: "places orders"}}, web.Uses, "two component calls are one link")
	assert.Equal(t, "Customer works with it directly.", web.Why)
}

// platform has two systems of its own, one made only of infrastructure,
// and partners sending work in.
func platform() *graph.Graph {
	return graph.New(&api.Model{
		Systems: []api.System{
			{ID: "ops", Name: "Operations", Type: "person"},
			{ID: "partner", Name: "Content Partners", IsExternal: true},
			{ID: "ingest", Name: "Ingest", Containers: []api.Container{
				{ID: "ingest-api", Name: "ingest-api", ContainerType: "api"},
				{ID: "ingest-ui", Name: "ingest-ui", ContainerType: "web_app"},
				{ID: "ingest-db", Name: "Ingest Database", ContainerType: "database"},
			}},
			{ID: "wf", Name: "Video Workflow", Containers: []api.Container{
				{ID: "wf-api", Name: "wf-api", ContainerType: "api"},
				{ID: "wf-worker", Name: "wf-worker-anywf", ContainerType: "worker"},
			}},
			{ID: "infra", Name: "Video Platform Infrastructure", Containers: []api.Container{
				{ID: "kafka", Name: "Kafka", ContainerType: "message_queue"},
				{ID: "redis", Name: "Redis", ContainerType: "cache"},
			}},
		},
		Relationships: []api.Relationship{
			{ID: "r1", SourceID: "partner", TargetID: "ingest-api", Type: "calls", Description: "Pushes packages"},
			{ID: "r2", SourceID: "ops", TargetID: "ingest-ui", Type: "uses"},
			{ID: "r3", SourceID: "ops", TargetID: "wf-api", Type: "uses"},
			{ID: "r4", SourceID: "ingest-api", TargetID: "wf-api", Type: "calls", Description: "Starts the Check workflow"},
			{ID: "r5", SourceID: "ingest-api", TargetID: "kafka", Type: "publishes_to"},
			{ID: "r6", SourceID: "wf-api", TargetID: "redis", Type: "writes_to"},
			{ID: "r7", SourceID: "wf-worker", TargetID: "kafka", Type: "consumes_from"},
			{ID: "r8", SourceID: "wf-api", TargetID: "wf-worker", Type: "calls"},
			{ID: "r9", SourceID: "ingest-api", TargetID: "ingest-db", Type: "writes_to"},
		},
	})
}

func TestPlanSeveralSystems(
	t *testing.T,
) {
	tr, _ := Plan(platform(), Options{Project: "video-wf"})
	assert.Equal(t, []string{"overview:video-wf", "element:Ingest", "element:Video Workflow", "recap:Recap"}, stopNames(tr),
		"systems are the stops, from where partners send work, and infrastructure is not one")
	assert.Equal(t, []string{"Content Partners", "Ingest", "Video Workflow"}, tr.Trace)

	overview := tr.Stops[0]
	assert.Equal(t, []string{"Ingest", "Video Workflow"}, overview.Systems)
	assert.Equal(t, []string{"Kafka", "Redis"}, overview.Infrastructure)

	ingest := tr.Stops[1]
	assert.Equal(t, "Where work comes in from outside: Content Partners sends work to it.", ingest.Why)
	assert.Equal(t, []string{"ingest-api", "ingest-ui"}, partNames(ingest.KeyParts), "the database is not a key part")
	assert.Equal(t, []Link{{Name: "Video Workflow", Label: "Starts the Check workflow"}}, ingest.Uses,
		"infrastructure is not a link")
	assert.Equal(t, []string{"Kafka"}, ingest.Infrastructure)
	assert.Equal(t, []string{"Kafka", "Redis"}, tr.Stops[2].Infrastructure)

	wf := tr.Stops[2]
	assert.Equal(t, "Ingest hands work to it next.", wf.Why)
}

func partNames(parts []Part) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.Name
	}
	return out
}

func TestInfra(
	t *testing.T,
) {
	g := platform()
	testCases := []struct {
		id   string
		want bool
	}{
		{id: "kafka", want: true},
		{id: "redis", want: true},
		{id: "ingest-db", want: true},
		{id: "infra", want: true},
		{id: "ingest-api"},
		{id: "ingest"},
		{id: "partner"},
		{id: "ops"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.id, func(t *testing.T) {
			assert.Equal(t, testCase.want, Infra(g.Node(testCase.id)))
		})
	}
}

func TestPlanIsStable(
	t *testing.T,
) {
	first, _ := Plan(platform(), Options{Project: "video-wf"})
	for range 20 {
		again, _ := Plan(platform(), Options{Project: "video-wf"})
		require.Equal(t, stopNames(first), stopNames(again))
	}
}

func TestPlanStartsAtSources(
	t *testing.T,
) {
	g := graph.New(&api.Model{
		Systems: []api.System{{ID: "s", Name: "Billing", Containers: []api.Container{
			{ID: "api", Name: "API", Components: []api.Component{{ID: "a"}, {ID: "b"}}},
			{ID: "web", Name: "Checkout Web"},
		}}},
		Relationships: []api.Relationship{{ID: "r", SourceID: "web", TargetID: "api", Type: "calls"}},
	})
	tr, _ := Plan(g, Options{Project: "Billing"})
	assert.Equal(t, []string{"overview:Billing", "element:Checkout Web", "element:API", "recap:Recap"}, stopNames(tr),
		"with nobody calling in, the tour starts where nothing calls")
	assert.Equal(t, []string{"Checkout Web", "API"}, tr.Trace)
}
