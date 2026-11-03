package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/repo"
)

func TestSplitWhyTarget(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		arg      string
		wantPath string
		wantLine int
	}{
		{name: "path", arg: "a/b.go", wantPath: "a/b.go"},
		{name: "path and line", arg: "a/b.go:42", wantPath: "a/b.go", wantLine: 42},
		{name: "line zero is not a line", arg: "a/b.go:0", wantPath: "a/b.go:0"},
		{name: "colon in the name", arg: "a:b.go", wantPath: "a:b.go"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path, line := splitWhyTarget(testCase.arg)
			assert.Equal(t, testCase.wantPath, path)
			assert.Equal(t, testCase.wantLine, line)
		})
	}
}

func TestMatchGlob(
	t *testing.T,
) {
	testCases := []struct {
		pattern, path string
		want          bool
	}{
		{pattern: "*.go", path: "internal/a/b.go", want: true},
		{pattern: "*.{ts,tsx}", path: "src/App.tsx", want: true},
		{pattern: "*.{ts,tsx}", path: "src/App.go", want: false},
		{pattern: "**/domain/*.go", path: "internal/domain/user.go", want: true},
		{pattern: "internal/**", path: "internal/x/y.go", want: true},
		{pattern: "internal/**", path: "cmd/x.go", want: false},
		{pattern: "internal/**/*_test.go", path: "internal/x/y_test.go", want: true},
		{pattern: "internal/**/*_test.go", path: "internal/x/y.go", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.pattern+" "+testCase.path, func(t *testing.T) {
			assert.Equal(t, testCase.want, matchFileGlob(testCase.pattern, testCase.path))
		})
	}
}

// whyServer fakes the endpoints why reads.
func whyServer(t *testing.T) *httptest.Server {
	t.Helper()
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)
		return v
	}
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /api/v1/projects/p1/harness/locate", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Paths []string `json:"paths"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Paths[0] != "internal/billing/invoice.go" {
			write(w, api.LocateResult{Unmatched: body.Paths})
			return
		}
		write(w, api.LocateResult{
			Files: []api.FileLocation{{Path: body.Paths[0], Elements: []api.LocatedElement{
				{ID: "inv", Type: "component", Name: "Invoicing", Match: "path"},
			}}},
			Impact: &api.BlastRadius{Elements: []api.TouchedElement{{
				Element:    api.Element{ID: "inv", Name: "Invoicing", Type: "component"},
				Owners:     []api.Owner{{Email: "ana@acme.io"}},
				TeamOwners: []api.TeamOwner{{Slug: "billing"}},
			}}},
		})
	})
	mux.HandleFunc("GET /api/v1/conformance/rules", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "p1", r.URL.Query().Get("projectId"))
		write(w, map[string]any{"data": []map[string]any{
			{"id": "r1", "name": "No floats for money", "severity": "high", "ruleType": "required_pattern", "config": map[string]any{"fileGlob": "internal/billing/**"}},
			{"id": "r2", "name": "API layering", "severity": "critical", "ruleType": "layer_boundary", "config": map[string]any{"containerIds": []string{"api"}}},
			{"id": "r3", "name": "Frontend naming", "severity": "low", "ruleType": "naming_convention", "config": map[string]any{"fileGlob": "*.tsx"}},
			{"id": "r4", "name": "Approved tech only", "severity": "medium", "ruleType": "technology_constraint", "config": map[string]any{}},
		}})
	})
	mux.HandleFunc("GET /api/v1/history", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "inv", r.URL.Query().Get("entityId"))
		write(w, map[string]any{"data": []api.HistoryEntry{
			{ID: "h1", UserName: "Ana", Action: "update", EntityName: "Invoicing", CreatedAt: at("2026-10-01T10:00:00Z")},
			{ID: "h2", UserName: "Bo", Action: "create", EntityName: "Invoicing", CreatedAt: at("2026-09-01T10:00:00Z")},
		}})
	})
	mux.HandleFunc("GET /api/v1/projects/p1/harness/memories", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "inv", r.URL.Query().Get("element"))
		write(w, map[string]any{"memories": []api.Memory{
			{ID: "m1", Kind: "note", Content: "Invoices are immutable once sent"},
			{ID: "m2", Kind: "pitfall", Content: "Amounts are in minor units"},
		}})
	})
	return httptest.NewServer(mux)
}

func whySnapshotFixture() *cache.ModelSnapshot {
	day := func(s string) time.Time {
		v, _ := time.Parse("2006-01-02", s)
		return v
	}
	return &cache.ModelSnapshot{
		Model: api.Model{Systems: []api.System{{ID: "bill", Name: "Billing", Containers: []api.Container{{
			ID: "api", Name: "Billing API", Components: []api.Component{{ID: "inv", Name: "Invoicing", Description: "Issues invoices"}},
		}}}}},
		ADRs: []api.ADR{
			{Number: 1, Title: "Use Postgres", Status: "superseded", Date: day("2025-01-10"), Links: []api.ADRLink{{ElementID: "bill"}}},
			{Number: 4, Title: "Money as integers", Status: "accepted", Date: day("2026-03-02"), Decision: "Store minor units.", Links: []api.ADRLink{{ElementID: "inv"}}},
			{Number: 5, Title: "Event-sourced ledger", Status: "proposed", Date: day("2026-05-01"), Links: []api.ADRLink{{ElementID: "api"}}},
			{Number: 7, Title: "Adopt gRPC", Status: "accepted", Date: day("2026-03-05"), Links: []api.ADRLink{{ElementID: "elsewhere"}}},
			{Number: 8, Title: "Unrelated, long ago", Status: "accepted", Date: day("2024-01-01")},
		},
	}
}

func TestGatherWhy(
	t *testing.T,
) {
	server := whyServer(t)
	defer server.Close()
	client := api.NewClient(server.URL, "", nil)
	commit := &repo.Commit{SHA: "0123456789abcdef", Author: "Ana", Subject: "Switch invoices to minor units",
		Date: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}

	testCases := []struct {
		name  string
		path  string
		line  int
		check func(t *testing.T, r *whyReport)
	}{
		{
			name: "a file in the model, with a line",
			path: "internal/billing/invoice.go",
			line: 42,
			check: func(t *testing.T, r *whyReport) {
				require.Len(t, r.Elements, 1)
				assert.Equal(t, "Billing / Billing API / Invoicing", r.Elements[0].Path)
				assert.Equal(t, "component", r.Elements[0].Level)
				assert.Equal(t, "billing", r.TeamOwners[0].Slug)
				assert.Equal(t, "ana@acme.io", r.Owners[0].Email)

				var numbers []int
				for _, d := range r.Decisions {
					numbers = append(numbers, d.Number)
				}
				assert.Equal(t, []int{4, 5, 1}, numbers, "accepted first, superseded last; ancestors' decisions apply")
				assert.True(t, r.Decisions[0].NearCommit)
				assert.Equal(t, "Invoicing", r.Decisions[0].LinkedTo)
				assert.Equal(t, "Billing API", r.Decisions[1].LinkedTo)

				require.Len(t, r.AroundCommit, 1)
				assert.Equal(t, 7, r.AroundCommit[0].Number, "an unlinked decision dated near the commit")

				var rules []string
				for _, rule := range r.Rules {
					rules = append(rules, rule.Name)
				}
				assert.Equal(t, []string{"API layering", "No floats for money"}, rules, "by severity; the .tsx rule does not aim here")
				assert.Equal(t, 1, r.ProjectWideRules)

				require.Len(t, r.Memories, 2)
				assert.Equal(t, "pitfall", r.Memories[0].Kind, "pitfalls first")
				require.Len(t, r.Changes, 2)
				assert.Equal(t, "h1", r.Changes[0].ID, "newest change first")

				md := r.markdown()
				for _, want := range []string{
					"# Why `internal/billing/invoice.go:42`",
					"- **Invoicing** (component) — `Billing / Billing API / Invoicing`",
					"**Owners:** @billing, ana@acme.io",
					"`01234567` Switch invoices to minor units — Ana, 2026-03-01",
					"- ADR-4 Money as integers (accepted, 2026-03-02) — on Invoicing, decided around the commit: Store minor units.",
					"## Decided around that commit\n\n- ADR-7 Adopt gRPC",
					"- **API layering** (critical) — scoped to Billing API",
					"- **No floats for money** (high) — files matching internal/billing/**",
					"- plus 1 project-wide rule(s)",
					"- 2026-10-01 Ana changed Invoicing",
				} {
					assert.Contains(t, md, want)
				}
			},
		},
		{
			name: "a file outside the model",
			path: "scripts/tmp.sh",
			check: func(t *testing.T, r *whyReport) {
				assert.Empty(t, r.Elements)
				assert.Empty(t, r.Decisions)
				assert.Empty(t, r.Rules)
				assert.Equal(t, 2, r.ProjectWideRules, "the server does not filter on containerIds: a rule scoped only by container applies here too")
				assert.Contains(t, r.markdown(), "Not in the model yet")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r, err := gatherWhy(context.Background(), client, whyInput{
				ProjectID: "p1",
				Path:      testCase.path,
				Line:      testCase.line,
				Snapshot:  func(context.Context) (*cache.ModelSnapshot, error) { return whySnapshotFixture(), nil },
				Commit:    func(context.Context) (*repo.Commit, error) { return commit, nil },
			})
			require.NoError(t, err)
			testCase.check(t, r)
		})
	}
}

func TestReviewMarkdownDiagram(
	t *testing.T,
) {
	r := &reviewReport{
		Check: &checkRun{Changes: []repo.Change{{Path: "a.go"}}, Result: &api.ConformanceCheckResult{}},
		Impact: &api.LocateResult{Impact: &api.BlastRadius{Elements: []api.TouchedElement{{
			Element:    api.Element{ID: "pay", Name: "Payments", Type: "container"},
			Dependents: []api.ImpactedElement{{Element: api.Element{ID: "chk", Name: "Checkout", Type: "container"}, Distance: 1, RelationshipType: "uses"}},
		}}}},
		Ready: true,
	}
	md := reviewMarkdown(r, "high", "")
	assert.Contains(t, md, "**Impact diagram**\n\n```mermaid\nflowchart LR\n")
	assert.Contains(t, md, `n2 -->|"uses"| n1`)
	assert.Less(t, strings.Index(md, "**Architecture touched**"), strings.Index(md, "```mermaid"))
}

func TestRuleReason(
	t *testing.T,
) {
	lineage := map[string]string{"c-api": "API"}
	layers := []any{
		map[string]any{"name": "domain", "pathPattern": "internal/domain/**"},
		map[string]any{"name": "adapter", "pathPattern": "internal/adapter/**"},
	}
	testCases := []struct {
		name   string
		config map[string]any
		path   string
		reason string
		scoped bool
	}{
		{
			name:   "container and glob both match",
			config: map[string]any{"containerIds": []any{"c-api"}, "fileGlob": "*.sql"},
			path:   "internal/db/q.sql",
			reason: "scoped to API, files matching *.sql",
			scoped: true,
		},
		{
			name:   "the container matches but the glob does not",
			config: map[string]any{"containerIds": []any{"c-api"}, "fileGlob": "*.sql"},
			path:   "internal/db/q.go",
			scoped: true,
		},
		{
			name:   "the glob matches outside the container, as the server checks it",
			config: map[string]any{"containerIds": []any{"c-web"}, "fileGlob": "*.sql"},
			path:   "web/q.sql",
			reason: "files matching *.sql",
			scoped: true,
		},
		{
			name:   "the container alone",
			config: map[string]any{"containerIds": []any{"c-api"}},
			path:   "internal/db/q.go",
			reason: "scoped to API",
			scoped: true,
		},
		{
			name:   "another container alone is not a filter",
			config: map[string]any{"containerIds": []any{"c-web"}},
			path:   "internal/db/q.go",
		},
		{
			name:   "a layer",
			config: map[string]any{"containerIds": []any{"c-api"}, "layers": layers},
			path:   "internal/domain/order.go",
			reason: "scoped to API, layer domain (internal/domain/**)",
			scoped: true,
		},
		{
			name:   "the container matches but no layer does",
			config: map[string]any{"containerIds": []any{"c-api"}, "layers": layers},
			path:   "cmd/main.go",
			scoped: true,
		},
		{
			name: "project-wide",
			path: "a.go",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			reason, scoped := ruleReason(testCase.config, testCase.path, lineage)
			assert.Equal(t, testCase.reason, reason)
			assert.Equal(t, testCase.scoped, scoped)
		})
	}
}
