package incident

import (
	"fmt"
	"time"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// now is the time every test report is generated at.
var now = time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)

// shop is a small model: customers use the Storefront, which calls the
// Orders API; the Mobile App calls it too; the Back Office's Admin calls one
// of its components. The Orders API stores in the Orders DB and its handlers
// charge through Stripe.
func shop() *graph.Graph {
	return graph.New(&api.Model{
		Systems: []api.System{
			{ID: "u", Name: "Customer", Type: "person"},
			{ID: "s", Name: "Shop", Containers: []api.Container{
				{ID: "web", Name: "Storefront", ContainerType: "web_app"},
				{ID: "mobile", Name: "Mobile App", ContainerType: "mobile_app"},
				{
					ID: "api", Name: "Orders API", ContainerType: "api",
					Technologies: []api.Technology{{Name: "Go"}, {Name: "gRPC"}},
					Components: []api.Component{{
						ID: "handlers", Name: "Order Handlers",
						CodeElements: []api.CodeElement{{ID: "place", Name: "PlaceOrder", CodeType: "function"}},
					}},
				},
				{ID: "db", Name: "Orders DB", ContainerType: "database"},
			}},
			{ID: "bo", Name: "Back Office", Containers: []api.Container{{ID: "admin", Name: "Admin"}}},
			{ID: "pay", Name: "Stripe", IsExternal: true},
		},
		Relationships: []api.Relationship{
			{ID: "r1", SourceID: "u", TargetID: "web", Type: "uses"},
			{ID: "r2", SourceID: "web", TargetID: "api", Type: "calls"},
			{ID: "r3", SourceID: "mobile", TargetID: "api", Type: "calls"},
			{ID: "r4", SourceID: "admin", TargetID: "handlers", Type: "reads_from"},
			{ID: "r5", SourceID: "api", TargetID: "db", Type: "writes_to"},
			{ID: "r6", SourceID: "handlers", TargetID: "pay", Type: "charges"},
		},
	})
}

// fanIn is a model where n containers call one API.
func fanIn(n int) *graph.Graph {
	containers := []api.Container{{ID: "api", Name: "API"}}
	var rels []api.Relationship
	for i := range n {
		id := fmt.Sprintf("c%02d", i)
		containers = append(containers, api.Container{ID: id, Name: "Client " + id})
		rels = append(rels, api.Relationship{ID: "r" + id, SourceID: id, TargetID: "api", Type: "calls"})
	}
	return graph.New(&api.Model{
		Systems:       []api.System{{ID: "s", Name: "S", Containers: containers}},
		Relationships: rels,
	})
}

// names lists the hops' element names.
func names(hops []graph.Hop) []string {
	out := []string{}
	for _, h := range hops {
		out = append(out, fmt.Sprintf("%s@%d", h.Node.Name, h.Distance))
	}
	return out
}

func ids(nodes []*graph.Node) []string {
	out := []string{}
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

// testLinks are web links easy to assert on.
var testLinks = Links{
	Element: func(n *graph.Node) string { return "https://app/e/" + n.ID },
	Doc:     func(id string) string { return "https://app/d/" + id },
	ADR:     func(id string) string { return "https://app/a/" + id },
}
