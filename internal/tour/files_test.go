package tour

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStartHere(
	t *testing.T,
) {
	repo := Repo{
		Files: []string{
			"README.md",
			"services/api/main.go",
			"services/api/checkout.go",
			"services/api/catalog.go",
			"services/api/catalog_test.go",
			"services/api/vendor/x/y.go",
			"services/api/internal/deep/thing.go",
			"services/api/go.sum",
			"services/api/BUILD.bazel",
			"services/api/k8s/values.yaml",
			"services/api/config.yaml",
			"services/api/ifaces.go",
			"apps/storefront/src/index.tsx",
			"apps/storefront/src/logo.svg",
			"apps/storefront/src/pages/cart.tsx",
		},
		Churn: map[string]int{"services/api/catalog.go": 5, "apps/storefront/src/pages/cart.tsx": 9},
	}
	g := shop()
	testCases := []struct {
		name    string
		element string
		limit   int
		want    []File
	}{
		{
			name:    "the model's paths, then the folder named after it",
			element: "api",
			limit:   4,
			want: []File{
				{Path: "services/api/checkout.go", Reason: "where the model places Checkout"},
				{Path: "services/api/catalog.go", Reason: "changed in 5 recent commits"},
				{Path: "services/api/main.go", Reason: "entry point"},
				{Path: "services/api/internal/deep/thing.go", Reason: "in services/api/"},
			},
		},
		{
			name:    "a generic word is not needed in the folder name",
			element: "web",
			limit:   2,
			want: []File{
				{Path: "apps/storefront/src/pages/cart.tsx", Reason: "changed in 9 recent commits"},
				{Path: "apps/storefront/src/index.tsx", Reason: "entry point"},
			},
		},
		{
			name:    "nothing to suggest",
			element: "db",
			limit:   4,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got := StartHere(g.Node(testCase.element), repo, testCase.limit)
			assert.Equal(t, testCase.want, got)
		})
	}
	assert.Equal(t, []File{{Path: "README.md", Reason: "what the repository is"}}, Overview(repo))
}

func TestNoise(
	t *testing.T,
) {
	testCases := []struct {
		path string
		want bool
	}{
		{path: "a/b_test.go", want: true},
		{path: "web/src/app.spec.ts", want: true},
		{path: "node_modules/x/index.js", want: true},
		{path: "api/gen/x.pb.go", want: true},
		{path: "yarn.lock", want: true},
		{path: "web/logo.png", want: true},
		{path: "api/wf/pkg/wfapi/BUILD.bazel", want: true},
		{path: "worker/anywf/k8s/wf-worker-anywf/values/aws/values.yaml", want: true},
		{path: "api/smartvision/proto/api.swagger.json", want: true},
		{path: "deploy/helm/values-prod.yaml", want: true},
		{path: "api/smartvision/proto/api.proto"},
		{path: "api/main.go"},
		{path: "web/src/App.tsx"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			assert.Equal(t, testCase.want, noise(testCase.path))
		})
	}
}
