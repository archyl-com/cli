package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/tour"
	"github.com/archyl-com/cli/internal/ui"
)

// tourAPI answers what a tour reads: the model, decisions, rules, owners
// and memories.
func tourAPI(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/projects/p1/c4":
			_, _ = io.WriteString(w, `{"systems":[`+
				`{"id":"u","name":"Customer","type":"person"},`+
				`{"id":"s","name":"Shop","containers":[`+
				`{"id":"web","name":"Storefront","containerType":"web_app"},`+
				`{"id":"api","name":"Orders API","containerType":"api"}]}],`+
				`"relationships":[{"id":"r1","sourceId":"u","targetId":"web","type":"uses"},`+
				`{"id":"r2","sourceId":"web","targetId":"api","type":"calls","description":"places orders"}]}`)
		case path == "/projects/p1/adrs":
			_, _ = io.WriteString(w, `{"data":[{"id":"a","number":7,"title":"Orders are idempotent","status":"accepted","links":[{"elementId":"api"}]}]}`)
		case path == "/conformance/rules":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case strings.HasSuffix(path, "/owners"):
			_, _ = io.WriteString(w, `{"data":[],"teamOwners":[{"team":{"name":"Checkout","slug":"checkout"}}]}`)
		case path == "/chat":
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), "Reply with one JSON object") {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			story := `{"overview":"Customers order on the Storefront; Orders API takes the orders.",` +
				`"flows":["Customer → Storefront → Orders API: an order is placed"],` +
				`"order":["Storefront","Orders API"],` +
				`"stops":[{"name":"Orders API","story":"It owns orders."}],"trace":"Follow one order from the Storefront."}`
			w.Header().Set("Content-Type", "text/event-stream")
			for _, chunk := range []string{story[:40], story[40:]} {
				data, _ := json.Marshal(map[string]string{"content": chunk})
				_, _ = io.WriteString(w, "data: "+string(data)+"\n\n")
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case path == "/projects/p1/harness/memories":
			if r.URL.Query().Get("element") == "api" {
				_, _ = io.WriteString(w, `{"memories":[{"id":"m","kind":"pitfall","content":"Retries must reuse the order key."}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"memories":[]}`)
		default:
			_, _ = io.WriteString(w, `{"id":"p1","name":"Shop"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func runTour(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runTourErr(t, args...)
	require.NoError(t, err)
	return out
}

// runTourErr runs the CLI against tourAPI and returns stdout and the error.
func runTourErr(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_NO_CACHE", "1")
	t.Setenv("ARCHYL_HOST", tourAPI(t))
	t.Setenv("ARCHYL_API_KEY", "test-key")
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &out, Err: io.Discard}}
	root := NewRoot(app)
	root.SetArgs(append(args, "--project", "p1"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

func TestTourCommand(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		args        []string
		want        []string
		wantMissing []string
	}{
		{
			name: "the story comes from the chat",
			args: []string{"tour"},
			want: []string{
				"the story is written by AI",
				"Customers order on the Storefront; Orders API takes the orders.",
				"- Customer → Storefront → Orders API: an order is placed",
				"It owns orders.",
				"**Try it:** Follow one order from the Storefront.",
				"- **Owned by:** Checkout team",
			},
		},
		{
			name: "piped without AI, it prints the facts for agents",
			args: []string{"tour", "--no-ai"},
			want: []string{
				"# Tour of Shop",
				"## 1. Storefront",
				"## 2. Orders API",
				"- **Owned by:** Checkout team",
				"- ADR-7 Orders are idempotent (accepted)",
				"- pitfall: Retries must reuse the order key.",
				"Customer → Storefront → Orders API",
				"**Why it's here:** Customer works with it directly.",
			},
			wantMissing: []string{"```mermaid"},
		},
		{
			name: "--markdown adds diagrams",
			args: []string{"tour", "--markdown", "--no-ai"},
			want: []string{"```mermaid"},
		},
		{
			name:        "a focus narrows the tour",
			args:        []string{"tour", "Orders API", "--no-ai"},
			want:        []string{"# Tour of Orders API", "## 1. Orders API"},
			wantMissing: []string{"## 2."},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := runTour(t, testCase.args...)
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, out, w)
			}
		})
	}
}

func TestTourCommandJSON(
	t *testing.T,
) {
	var got tour.Tour
	require.NoError(t, json.Unmarshal([]byte(runTour(t, "tour", "--json", "--no-ai")), &got))
	require.Len(t, got.Stops, 4)
	assert.Equal(t, tour.KindOverview, got.Stops[0].Kind)
	assert.Equal(t, "Orders API", got.Stops[2].Name)
	assert.Equal(t, []string{"Checkout team"}, got.Stops[2].Owners)
	assert.Equal(t, []string{"Customer", "Storefront", "Orders API"}, got.Trace)
}

func TestTourDecisions(
	t *testing.T,
) {
	g := graph.New(&api.Model{Systems: []api.System{
		{ID: "a", Name: "A", Containers: []api.Container{{ID: "a1", Name: "A1"}}},
		{ID: "b", Name: "B"},
		{ID: "c", Name: "C"},
	}})
	stops := []*graph.Node{g.Node("a"), g.Node("b"), g.Node("c")}
	link := func(ids ...string) []api.ADRLink {
		out := make([]api.ADRLink, len(ids))
		for i, id := range ids {
			out[i] = api.ADRLink{ElementID: id}
		}
		return out
	}
	adrs := []api.ADR{
		{Number: 1, Title: "Lint everything", Status: "accepted", Links: link("a1", "b", "c")},
		{Number: 2, Title: "A owns invoices", Status: "proposed", Links: link("a1")},
		{Number: 3, Title: "A is event sourced", Status: "accepted", Links: link("a")},
	}
	wide := wideDecisions(adrs, stops)

	testCases := []struct {
		name string
		node string
		want []int
	}{
		{name: "a project-wide decision is left to the big picture, accepted first", node: "a", want: []int{3, 2}},
		{name: "nothing specific", node: "b"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var got []int
			for _, d := range tourDecisions(adrs, g.Node(testCase.node), wide) {
				got = append(got, d.Number)
			}
			assert.Equal(t, testCase.want, got)
		})
	}
	assert.Equal(t, map[int]bool{1: true}, wide)
	assert.Empty(t, wideDecisions(adrs, stops[:2]), "with fewer stops than the threshold, nothing is project-wide")
}

func TestAddEntryPoints(
	t *testing.T,
) {
	tr := &tour.Tour{Stops: []tour.Stop{
		{Kind: tour.KindOverview, Files: []tour.File{{Path: "README.md", Reason: "what the repository is"}}},
		{Kind: tour.KindElement, Name: "Ingest", Files: []tour.File{
			{Path: "api/ingest/handler.go", Reason: "changed in 3 recent commits"},
			{Path: "api/ingest/main.go", Reason: "ingest-api · entry point"},
		}},
		{Kind: tour.KindElement, Name: "Stats", Files: []tour.File{{Path: "stats/x.go", Reason: "in stats/"}}},
		{Kind: tour.KindRecap},
	}}
	addEntryPoints(tr)
	assert.Equal(t, []tour.File{
		{Path: "README.md", Reason: "what the repository is"},
		{Path: "api/ingest/main.go", Reason: "where Ingest starts"},
	}, tr.Stops[0].Files)

	focused := &tour.Tour{Stops: []tour.Stop{{Kind: tour.KindOverview, ElementID: "x"}, tr.Stops[1]}}
	addEntryPoints(focused)
	assert.Empty(t, focused.Stops[0].Files, "a focused tour's big picture is the element itself")
}

func TestOwnerLabels(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		owners []api.Owner
		teams  []api.TeamOwner
		want   []string
	}{
		{name: "a team gets the word once", teams: []api.TeamOwner{{Name: "Checkout"}, {Name: "Platform Team"}}, want: []string{"Checkout team", "Platform Team"}},
		{name: "a team without a name uses its slug", teams: []api.TeamOwner{{Slug: "video"}}, want: []string{"video team"}},
		{name: "a person without a name is their email", owners: []api.Owner{{Email: "ada@example.com"}, {FirstName: "Grace", LastName: "Hopper"}}, want: []string{"ada@example.com", "Grace Hopper"}},
		{name: "nothing to name", owners: []api.Owner{{}}, teams: []api.TeamOwner{{}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, ownerLabels(testCase.owners, testCase.teams))
		})
	}
}
