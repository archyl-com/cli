package codeowners

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// shopModel is a small model with every way an element finds its folder.
func shopModel() *graph.Graph {
	return graph.New(&api.Model{Systems: []api.System{
		{ID: "shop", Name: "Shop", Containers: []api.Container{
			{ID: "orders", Name: "Orders API", Components: []api.Component{
				{
					ID: "invoicing", Name: "Invoicing", FilePath: "services/orders/invoicing/invoice.go",
					CodeElements: []api.CodeElement{
						{ID: "pdf", Name: "RenderPDF", FilePath: "services/orders/invoicing/pdf/render.go"},
						{ID: "tax", Name: "Tax", FilePath: "./services/orders/tax/tax.go"},
					},
				},
				{ID: "refunds", Name: "Refunds"},
			}},
			{ID: "web", Name: "Storefront"},
			{ID: "billing", Name: "Billing"},
			{ID: "worker", Name: "Orders Worker"},
			{ID: "indexer", Name: "Search Indexer", Components: []api.Component{
				{ID: "crawl", Name: "Crawl", FilePath: "indexer/crawl/crawl.go"},
				{ID: "rank", Name: "Rank", FilePath: `indexer\rank\rank.go`},
			}},
		}},
		{ID: "stripe", Name: "Stripe", IsExternal: true},
	}})
}

var shopFiles = []string{
	"README.md",
	"services/orders/main.go",
	"services/orders/invoicing/invoice.go",
	"services/orders/invoicing/pdf/render.go",
	"services/orders/tax/tax.go",
	"services/orders/refunds/refund.go",
	"web/storefront/app.tsx",
	"indexer/crawl/crawl.go",
	"indexer/rank/rank.go",
	"stripe/client.go",
}

func team(slug string) Owners {
	return Owners{Teams: []Team{{Name: slug, Slug: slug}}}
}

func TestBuild(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		owners       map[string]Owners
		handles      Handles
		wantLines    []string
		wantInherit  map[string]string
		wantUnplaced []string
		wantUnmapped []Unmapped
		wantOwned    int
	}{
		{
			name: "own owners, inheritance and pruning, shallow first",
			owners: map[string]Owners{
				"shop":      team("platform"),
				"orders":    team("checkout"),
				"invoicing": {People: []Person{{Email: "ada@example.com"}}},
				"stripe":    team("payments"),
			},
			handles: Handles{Org: "acme"},
			wantLines: []string{
				"/indexer/ @acme/platform",
				"/services/orders/ @acme/checkout",
				"/web/storefront/ @acme/platform",
				"/services/orders/invoicing/ ada@example.com",
				"/services/orders/tax/ ada@example.com",
			},
			wantInherit: map[string]string{
				"/indexer/":         "Shop",
				"/web/storefront/":  "Shop",
				"/services/orders/": "",
			},
			wantUnplaced: []string{"Shop"},
			wantOwned:    3,
		},
		{
			name: "unowned containers take their system's owners",
			owners: map[string]Owners{
				"shop":      team("platform"),
				"invoicing": team("billing"),
			},
			handles: Handles{Org: "acme"},
			wantLines: []string{
				"/indexer/ @acme/platform",
				"/services/orders/ @acme/platform",
				"/web/storefront/ @acme/platform",
				"/services/orders/invoicing/ @acme/billing",
				"/services/orders/tax/ @acme/billing",
			},
			wantUnplaced: []string{"Shop"},
			wantOwned:    2,
		},
		{
			name: "elements sharing a folder merge and dedupe their owners",
			owners: map[string]Owners{
				"orders":  {Teams: []Team{{Name: "Checkout", Slug: "checkout"}}, People: []Person{{Email: "ada@example.com"}}},
				"worker":  {Teams: []Team{{Name: "Checkout", Slug: "checkout"}}, People: []Person{{Email: "bob@example.com"}}},
				"refunds": {People: []Person{{Email: "ada@example.com"}, {Email: "bob@example.com"}}},
				"crawl":   team("search"),
				"rank":    team("search"),
			},
			handles: Handles{Org: "acme"},
			wantLines: []string{
				"/indexer/crawl/ @acme/search",
				"/indexer/rank/ @acme/search",
				"/services/orders/ @acme/checkout ada@example.com bob@example.com",
				// Invoicing inherits from Orders API alone, so the merged
				// line above does not speak for it.
				"/services/orders/invoicing/ @acme/checkout ada@example.com",
				"/services/orders/refunds/ ada@example.com bob@example.com",
				"/services/orders/tax/ @acme/checkout ada@example.com",
			},
			wantOwned: 5,
		},
		{
			name: "owners without a handle are reported and a line without any is dropped",
			owners: map[string]Owners{
				"orders":  team("checkout"),
				"web":     {Teams: []Team{{Name: "Front", Slug: "front"}}, People: []Person{{Name: "Grace Hopper"}}},
				"billing": {People: []Person{{Email: "ada@example.com"}}},
			},
			handles: Handles{Teams: map[string]string{"front": "@acme/frontend"}},
			wantLines: []string{
				"/web/storefront/ @acme/frontend",
			},
			wantUnplaced: []string{"Billing"},
			wantUnmapped: []Unmapped{
				{Kind: "team", Name: "checkout", Elements: []string{"Orders API", "Invoicing", "Refunds"}},
				{Kind: "person", Name: "Grace Hopper", Elements: []string{"Storefront"}},
			},
			wantOwned: 3,
		},
		{
			name:      "nobody owns anything",
			owners:    map[string]Owners{},
			wantOwned: 0,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := Build(Input{Graph: shopModel(), Files: shopFiles, Owners: testCase.owners, Handles: testCase.handles})

			var lines []string
			inherit := map[string]string{}
			for _, e := range res.Entries {
				lines = append(lines, e.Pattern+" "+joinOwners(e.Owners))
				inherit[e.Pattern] = e.InheritedFrom
			}
			assert.Equal(t, testCase.wantLines, lines)
			for pattern, from := range testCase.wantInherit {
				assert.Equal(t, from, inherit[pattern], pattern)
			}
			unplaced := []string{}
			for _, e := range res.Unplaced {
				unplaced = append(unplaced, e.Name)
			}
			assert.ElementsMatch(t, testCase.wantUnplaced, unplaced)
			if testCase.wantUnmapped == nil {
				testCase.wantUnmapped = []Unmapped{}
			}
			assert.Equal(t, testCase.wantUnmapped, res.Unmapped)
			assert.Equal(t, testCase.wantOwned, res.Owned)
		})
	}
}

func joinOwners(owners []string) string {
	out := ""
	for i, o := range owners {
		if i > 0 {
			out += " "
		}
		out += o
	}
	return out
}

func TestElementFolders(
	t *testing.T,
) {
	g := shopModel()
	dirs := directories(shopFiles)
	testCases := []struct {
		id   string
		want []string
	}{
		{id: "invoicing", want: []string{"services/orders/invoicing", "services/orders/tax"}},
		{id: "refunds", want: []string{"services/orders/refunds"}},
		{id: "orders", want: []string{"services/orders"}},
		{id: "worker", want: []string{"services/orders"}},
		{id: "web", want: []string{"web/storefront"}},
		{id: "indexer", want: []string{"indexer"}},
		{id: "billing"},
		{id: "shop"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.id, func(t *testing.T) {
			assert.Equal(t, testCase.want, elementFolders(g.Node(testCase.id), shopFiles, dirs))
		})
	}
}

func TestElementFoldersSkipsPathsNotInTheRepository(
	t *testing.T,
) {
	g := graph.New(&api.Model{Systems: []api.System{{ID: "s", Name: "S", Containers: []api.Container{
		{ID: "c", Name: "C", Components: []api.Component{
			{ID: "gone", Name: "Gone", FilePath: "old/gone/gone.go"},
			{ID: "root", Name: "Root File", FilePath: "main.go"},
			{ID: "spaced", Name: "Spaced", FilePath: "My Docs/guide.md"},
		}},
	}}}})
	files := []string{"main.go", "My Docs/guide.md"}
	dirs := directories(files)
	assert.Empty(t, elementFolders(g.Node("gone"), files, dirs))
	assert.Empty(t, elementFolders(g.Node("root"), files, dirs))
	assert.Equal(t, []string{"My Docs"}, elementFolders(g.Node("spaced"), files, dirs))

	res := Build(Input{
		Graph:   g,
		Files:   files,
		Owners:  map[string]Owners{"spaced": {People: []Person{{Email: "ada@example.com"}}}},
		Handles: Handles{},
	})
	if assert.Len(t, res.Entries, 1) {
		assert.Equal(t, `/My\ Docs/`, res.Entries[0].Pattern)
	}
}

func TestCommonFolder(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		folders []string
		want    string
	}{
		{name: "none"},
		{name: "one", folders: []string{"a/b"}, want: "a/b"},
		{name: "siblings", folders: []string{"a/b/c", "a/b/d"}, want: "a/b"},
		{name: "a prefix that is not a folder", folders: []string{"app/x", "apps/y"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, commonFolder(testCase.folders))
		})
	}
}
