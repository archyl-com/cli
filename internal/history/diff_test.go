package history

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

// shop is the model before: a shop with an API, a mailer and a worker, a
// payment provider, and their relationships.
func shop() *api.Model {
	return &api.Model{
		Systems: []api.System{
			{ID: "shop", Name: "Shop", Type: "software_system", Containers: []api.Container{
				{ID: "web", Name: "Storefront", ContainerType: "web_app", Description: "Sells things"},
				{ID: "api", Name: "Orders API", ContainerType: "api", Components: []api.Component{
					{ID: "checkout", Name: "Checkout", ComponentType: "service"},
					{ID: "cart", Name: "Cart", ComponentType: "service"},
				}},
				{ID: "mailer", Name: "Mailer", ContainerType: "worker", Components: []api.Component{
					{ID: "tpl", Name: "Templates", ComponentType: "module", CodeElements: []api.CodeElement{
						{ID: "render", Name: "Render", CodeType: "function"},
					}},
					{ID: "smtp", Name: "SMTP Client", ComponentType: "client"},
				}},
			}},
			{ID: "pay", Name: "Stripe", Type: "software_system", IsExternal: true},
		},
		Relationships: []api.Relationship{
			{ID: "r1", SourceID: "web", TargetID: "api", Type: "calls", Description: "places orders"},
			{ID: "r2", SourceID: "checkout", TargetID: "pay", Type: "calls", Description: "charges cards"},
			{ID: "r3", SourceID: "api", TargetID: "mailer", Type: "sends", Description: "asks for receipts"},
			{ID: "r4", SourceID: "smtp", TargetID: "pay", Type: "uses"},
		},
	}
}

func TestCompareElements(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		change func(m *api.Model)
		opts   Options
		want   []Element
	}{
		{
			name:   "nothing changed",
			change: func(*api.Model) {},
		},
		{
			name: "a rename",
			change: func(m *api.Model) {
				m.Systems[0].Containers[1].Name = "Order Service"
			},
			want: []Element{{Status: Modified, ID: "api", Level: "container", Name: "Order Service", Path: "Shop / Order Service",
				Changes: []FieldChange{{Field: FieldName, Before: "Orders API", After: "Order Service"}}}},
		},
		{
			name: "a move between parents",
			change: func(m *api.Model) {
				orders := &m.Systems[0].Containers[1]
				cart := orders.Components[1]
				orders.Components = orders.Components[:1]
				m.Systems[0].Containers[0].Components = []api.Component{cart}
			},
			want: []Element{{Status: Modified, ID: "cart", Level: "component", Name: "Cart", Path: "Shop / Storefront / Cart",
				Changes: []FieldChange{{Field: FieldParent, Before: "Shop / Orders API", After: "Shop / Storefront"}}}},
		},
		{
			name: "a parent removed with its children counts once",
			change: func(m *api.Model) {
				m.Systems[0].Containers = m.Systems[0].Containers[:2]
				m.Relationships = m.Relationships[:2]
			},
			want: []Element{{Status: Removed, ID: "mailer", Level: "container", Name: "Mailer", Path: "Shop / Mailer", Descendants: 3}},
		},
		{
			name: "a child that moved away is not removed with its parent",
			change: func(m *api.Model) {
				smtp := m.Systems[0].Containers[2].Components[1]
				m.Systems[0].Containers = m.Systems[0].Containers[:2]
				m.Systems[0].Containers[1].Components = append(m.Systems[0].Containers[1].Components, smtp)
			},
			want: []Element{
				{Status: Removed, ID: "mailer", Level: "container", Name: "Mailer", Path: "Shop / Mailer", Descendants: 2},
				{Status: Modified, ID: "smtp", Level: "component", Name: "SMTP Client", Path: "Shop / Orders API / SMTP Client",
					Changes: []FieldChange{{Field: FieldParent, Before: "Shop / Mailer", After: "Shop / Orders API"}}},
			},
		},
		{
			name: "an element added with children",
			change: func(m *api.Model) {
				m.Systems = append(m.Systems, api.System{ID: "wh", Name: "Warehouse", Containers: []api.Container{{ID: "wms", Name: "WMS"}}})
			},
			want: []Element{{Status: Added, ID: "wh", Level: "system", Name: "Warehouse", Path: "Warehouse", Descendants: 1}},
		},
		{
			name: "new IDs with the same path match by name",
			change: func(m *api.Model) {
				m.Systems[0].ID = "shop-2"
				m.Systems[0].Containers[1].ID = "api-2"
				m.Systems[0].Containers[1].Components[0].ID = "checkout-2"
				m.Relationships[0].TargetID = "api-2"
				m.Relationships[1].SourceID = "checkout-2"
				m.Relationships[2].SourceID = "api-2"
			},
		},
		{
			name: "a renamed parent keeps its children matched by name under it",
			change: func(m *api.Model) {
				m.Systems[0].Containers[1].Name = "Order Service"
				m.Systems[0].Containers[1].Components[1].ID = "cart-2"
			},
			want: []Element{{Status: Modified, ID: "api", Level: "container", Name: "Order Service", Path: "Shop / Order Service",
				Changes: []FieldChange{{Field: FieldName, Before: "Orders API", After: "Order Service"}}}},
		},
		{
			name: "type, description and external",
			change: func(m *api.Model) {
				m.Systems[0].Containers[0].ContainerType = "spa"
				m.Systems[0].Containers[0].Description = "Sells   things\nonline"
				m.Systems[1].IsExternal = false
			},
			want: []Element{
				{Status: Modified, ID: "pay", Level: "system", Name: "Stripe", Path: "Stripe",
					Changes: []FieldChange{{Field: FieldExternal, Before: "yes", After: "no"}}},
				{Status: Modified, ID: "web", Level: "container", Name: "Storefront", Path: "Shop / Storefront",
					Changes: []FieldChange{
						{Field: FieldType, Before: "web_app", After: "spa"},
						{Field: FieldDescription, Before: "Sells things", After: "Sells things online"},
					}},
			},
		},
		{
			name: "whitespace in a description is no change",
			change: func(m *api.Model) {
				m.Systems[0].Containers[0].Description = "  Sells\tthings "
			},
		},
		{
			name: "technologies are ignored unless asked for",
			change: func(m *api.Model) {
				m.Systems[0].Containers[0].Technologies = []api.Technology{{Name: "React"}}
			},
		},
		{
			name: "technologies when asked for",
			change: func(m *api.Model) {
				m.Systems[0].Containers[0].Technologies = []api.Technology{{Name: "React"}, {Name: "Vite"}}
			},
			opts: Options{Technology: true},
			want: []Element{{Status: Modified, ID: "web", Level: "container", Name: "Storefront", Path: "Shop / Storefront",
				Changes: []FieldChange{{Field: FieldTechnology, After: "React, Vite"}}}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			after := shop()
			testCase.change(after)

			d := Compare(shop(), after, testCase.opts)

			assert.Equal(t, testCase.want, d.Elements)
		})
	}
}

func TestCompareRelationships(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		change func(m *api.Model)
		want   []Relationship
	}{
		{
			name:   "nothing changed",
			change: func(*api.Model) {},
		},
		{
			name: "an added relationship",
			change: func(m *api.Model) {
				m.Relationships = append(m.Relationships, api.Relationship{ID: "r5", SourceID: "cart", TargetID: "checkout", Type: "uses"})
			},
			want: []Relationship{{Status: Added, ID: "r5", SourceID: "cart", TargetID: "checkout", Source: "Cart", Target: "Checkout", Label: "uses"}},
		},
		{
			name: "a relationship removed on its own",
			change: func(m *api.Model) {
				m.Relationships = m.Relationships[1:]
			},
			want: []Relationship{{Status: Removed, ID: "r1", SourceID: "web", TargetID: "api", Source: "Storefront", Target: "Orders API", Label: "places orders"}},
		},
		{
			name: "relationships whose end was removed go with it",
			change: func(m *api.Model) {
				m.Systems[0].Containers = m.Systems[0].Containers[:2]
			},
			want: []Relationship{
				{Status: Removed, ID: "r3", SourceID: "api", TargetID: "mailer", Source: "Orders API", Target: "Mailer", Label: "asks for receipts", WithEnd: true},
				{Status: Removed, ID: "r4", SourceID: "smtp", TargetID: "pay", Source: "SMTP Client", Target: "Stripe", Label: "uses", WithEnd: true},
			},
		},
		{
			name: "a new ID between the same ends is the same relationship",
			change: func(m *api.Model) {
				m.Relationships[0].ID = "r1-new"
			},
		},
		{
			name: "a reworded relationship",
			change: func(m *api.Model) {
				m.Relationships[0].Description = "submits orders"
				m.Relationships[0].Type = "posts"
			},
			want: []Relationship{{Status: Modified, ID: "r1", SourceID: "web", TargetID: "api", Source: "Storefront", Target: "Orders API",
				Label: "submits orders", Changes: []FieldChange{
					{Field: FieldType, Before: "calls", After: "posts"},
					{Field: FieldDescription, Before: "places orders", After: "submits orders"},
				}}},
		},
		{
			name: "a relationship pointed elsewhere",
			change: func(m *api.Model) {
				m.Relationships[1].TargetID = "smtp"
			},
			want: []Relationship{{Status: Modified, ID: "r2", SourceID: "checkout", TargetID: "smtp", Source: "Checkout", Target: "SMTP Client",
				Label: "charges cards", Changes: []FieldChange{{Field: FieldTarget, Before: "Stripe", After: "SMTP Client"}}}},
		},
		{
			name: "ends with new IDs",
			change: func(m *api.Model) {
				m.Systems[1].ID = "stripe"
				m.Relationships[1].ID, m.Relationships[1].TargetID = "r2b", "stripe"
				m.Relationships[3].TargetID = "stripe"
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			after := shop()
			testCase.change(after)

			d := Compare(shop(), after, Options{})

			assert.Equal(t, testCase.want, d.Relationships)
		})
	}
}

func TestSummary(
	t *testing.T,
) {
	after := shop()
	after.Systems[0].Containers = after.Systems[0].Containers[:2]
	after.Systems[0].Containers[0].Name = "Shopfront"
	after.Systems = append(after.Systems, api.System{ID: "wh", Name: "Warehouse"})
	after.Relationships = append(after.Relationships, api.Relationship{ID: "r9", SourceID: "wh", TargetID: "api", Type: "calls"})

	d := Compare(shop(), after, Options{})

	assert.Equal(t, Summary{Added: 1, Removed: 1, Modified: 1, RelationshipsAdded: 1, RelationshipsRemoved: 2}, d.Summary())
	assert.False(t, d.Empty())
	assert.True(t, Compare(shop(), shop(), Options{}).Empty())
}

func TestCompareFromEmpty(
	t *testing.T,
) {
	d := Compare(&api.Model{}, shop(), Options{})

	require.Len(t, d.Elements, 2)
	assert.Equal(t, Element{Status: Added, ID: "shop", Level: "system", Name: "Shop", Path: "Shop", Descendants: 8}, d.Elements[0])
	assert.Len(t, d.Relationships, 4)
	for _, r := range d.Relationships {
		assert.False(t, r.WithEnd, "relationships of new elements are news")
	}
}
