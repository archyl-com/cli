package diagram

import (
	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

func tech(names ...string) []api.Technology {
	out := make([]api.Technology, 0, len(names))
	for _, n := range names {
		out = append(out, api.Technology{Name: n})
	}
	return out
}

// shopGraph is a small shop: four systems, a handful of containers in the
// main one, and the components of its API.
func shopGraph() *graph.Graph {
	return graph.New(&api.Model{
		Systems: []api.System{
			{ID: "shop", Name: "Shop", Type: "software_system", Containers: []api.Container{
				{ID: "web", Name: "Storefront", ContainerType: "web_app", Technologies: tech("React")},
				{ID: "api", Name: "Shop API", ContainerType: "api", Technologies: tech("Go"), Components: []api.Component{
					{ID: "checkout", Name: "Checkout", ComponentType: "service"},
					{ID: "catalog", Name: "Catalog", ComponentType: "service"},
					{ID: "payclient", Name: "Payments Client", ComponentType: "client", CodeElements: []api.CodeElement{
						{ID: "charge", Name: "Charge", CodeType: "function"},
					}},
					{ID: "orders", Name: "Order Repository", ComponentType: "repository"},
				}},
				{ID: "worker", Name: "Fulfilment Worker", ContainerType: "worker", Technologies: tech("Go")},
				{ID: "db", Name: "Orders DB", ContainerType: "database", Technologies: tech("PostgreSQL")},
			}},
			{ID: "pay", Name: "Stripe", Type: "software_system", IsExternal: true},
			{ID: "mail", Name: "Mailgun", Type: "software_system", IsExternal: true},
			{ID: "wh", Name: "Warehouse", Type: "software_system"},
		},
		Relationships: []api.Relationship{
			{ID: "r1", SourceID: "web", TargetID: "checkout", Type: "calls", Description: "places orders"},
			{ID: "r2", SourceID: "web", TargetID: "catalog", Type: "calls", Description: "browses products"},
			{ID: "r3", SourceID: "checkout", TargetID: "payclient", Type: "uses"},
			{ID: "r4", SourceID: "checkout", TargetID: "orders", Type: "uses"},
			{ID: "r5", SourceID: "catalog", TargetID: "db", Type: "reads_from"},
			{ID: "r6", SourceID: "orders", TargetID: "db", Type: "writes_to"},
			{ID: "r7", SourceID: "charge", TargetID: "pay", Type: "calls", Description: "charges cards"},
			{ID: "r8", SourceID: "worker", TargetID: "db", Type: "reads_from"},
			{ID: "r9", SourceID: "worker", TargetID: "wh", Type: "calls", Description: "ships orders"},
			{ID: "r10", SourceID: "worker", TargetID: "mail", Type: "sends", Description: "sends receipts"},
			{ID: "r11", SourceID: "wh", TargetID: "api", Type: "calls", Description: "updates stock"},
		},
	})
}
