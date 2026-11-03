package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/incident"
	"github.com/archyl-com/cli/internal/ui"
)

// incidentAPI answers what an incident report reads: the model, decisions,
// owners, history, releases, documentation and memories. A path starting
// with one of failing answers 500.
func incidentAPI(t *testing.T, failing ...string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		for _, prefix := range failing {
			if strings.HasPrefix(path, prefix) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":"boom"}`)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query()
		switch {
		case path == "/projects/p1/c4":
			_, _ = io.WriteString(w, `{"systems":[`+
				`{"id":"u","name":"Customer","type":"person"},`+
				`{"id":"s","name":"Shop","containers":[`+
				`{"id":"web","name":"Storefront","containerType":"web_app"},`+
				`{"id":"api","name":"Orders API","containerType":"api","technologies":[{"name":"Go"}]},`+
				`{"id":"db","name":"Orders DB","containerType":"database"}]}],`+
				`"relationships":[{"id":"r1","sourceId":"u","targetId":"web","type":"uses"},`+
				`{"id":"r2","sourceId":"web","targetId":"api","type":"calls"},`+
				`{"id":"r3","sourceId":"api","targetId":"db","type":"writes_to"}]}`)
		case path == "/projects/p1/adrs":
			_, _ = io.WriteString(w, `{"data":[{"id":"a7","number":7,"title":"Orders are idempotent","status":"accepted","links":[{"elementId":"api"}]}]}`)
		case path == "/c4/containers/api/owners":
			_, _ = io.WriteString(w, `{"data":[{"user":{"email":"jane@shop.test","firstName":"Jane","lastName":"Doe"}}],`+
				`"teamOwners":[{"team":{"name":"Checkout","slug":"checkout"}}]}`)
		case path == "/c4/systems/s/owners":
			_, _ = io.WriteString(w, `{"data":[],"teamOwners":[{"team":{"name":"Platform","slug":"platform"}}]}`)
		case path == "/c4/containers/web/owners":
			_, _ = io.WriteString(w, `{"data":[],"teamOwners":[{"team":{"name":"Web","slug":"web"}}]}`)
		case strings.HasSuffix(path, "/owners"):
			_, _ = io.WriteString(w, `{"data":[],"teamOwners":[]}`)
		case path == "/history":
			if query.Get("entityId") == "db" {
				_, _ = io.WriteString(w, `{"data":[{"id":"h1","action":"update","entityType":"container","entityId":"db",`+
					`"entityName":"Orders DB","userName":"Bob","createdAt":"2026-10-27T10:00:00Z"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
		case path == "/projects/p1/releases":
			if query.Get("containerId") == "api" {
				_, _ = io.WriteString(w, `{"data":[{"id":"r","version":"v1.4.2","status":"deployed",`+
					`"createdAt":"2026-10-27T09:00:00Z","environment":{"name":"production","isProduction":true}}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
		case path == "/docs/":
			if query.Get("elementId") == "s" {
				_, _ = io.WriteString(w, `{"data":[{"id":"d1","title":"Shop on-call runbook"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":[]}`)
		case path == "/projects/p1/harness/memories":
			_, _ = io.WriteString(w, `{"memories":[{"id":"m","kind":"pitfall","content":"Retries must reuse the order key."}]}`)
		default:
			_, _ = io.WriteString(w, `{"id":"p1","name":"Shop"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// incidentRoot is the command tree with incident in it, whether or not the
// root registers it yet.
func incidentRoot(app *App) *cobra.Command {
	root := NewRoot(app)
	if found, _, err := root.Find([]string{"incident"}); err != nil || found == root {
		root.AddCommand(newIncidentCmd(app))
	}
	return root
}

func runIncident(t *testing.T, failing []string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_NO_CACHE", "1")
	t.Setenv("ARCHYL_NO_KEYRING", "1")
	t.Setenv("ARCHYL_HOST", incidentAPI(t, failing...))
	t.Setenv("ARCHYL_API_KEY", "test-key")
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &out, Err: io.Discard}}
	root := incidentRoot(app)
	root.SetArgs(append(args, "--project", "p1"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

func TestIncidentCommand(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		args        []string
		failing     []string
		want        []string
		wantMissing []string
	}{
		{
			name: "piped, the report is Markdown",
			args: []string{"incident", "Orders API"},
			want: []string{
				"# Incident: Orders API",
				"api · Shop / Orders API · Go",
				"- **Owners:** Checkout team, Jane Doe (jane@shop.test)",
				"**2 elements depend on it within 2 hops.**",
				"- Storefront (web app), calls · Web team",
				"- Customer (person), uses · none set",
				"- Orders DB (database), writes to · Platform team (from Shop)",
				"Bob updated Orders DB (container)",
				"**Releases of Orders API:**",
				"- v1.4.2 deployed in production",
				"(on Shop)",
				"Shop on-call runbook",
				"ADR-7 Orders are idempotent",
				"- pitfall: Retries must reuse the order key.",
				"## Who to tell\n\n- Checkout team\n- Web team\n- Jane Doe (jane@shop.test)\n",
			},
			wantMissing: []string{"navailable"},
		},
		{
			name:        "--depth 1 stops at direct dependents",
			args:        []string{"incident", "Orders API", "--depth", "1"},
			want:        []string{"**1 element depends on it within 1 hop.**"},
			wantMissing: []string{"Customer"},
		},
		{
			name:    "a failing section says so, the rest still shows",
			args:    []string{"incident", "Orders API"},
			failing: []string{"/history", "/projects/p1/releases", "/docs"},
			want: []string{
				"## Recent changes\n\n_Unavailable._",
				"**Latest releases in the project:**\n\n_Unavailable._",
				"- docs: _unavailable_",
				"- **Owners:** Checkout team",
				"- pitfall: Retries must reuse the order key.",
			},
		},
		{
			name:    "a failed owner lookup is unknown, never the parent's team",
			args:    []string{"incident", "Orders API"},
			failing: []string{"/c4/containers/api/owners"},
			want: []string{
				"- **Owners:** _Unavailable._",
				"_Incomplete: some owners could not be fetched._",
			},
			wantMissing: []string{"Owners:** Platform", "Checkout"},
		},
		{
			name:    "the Slack message says what it is missing",
			args:    []string{"incident", "Orders API", "--slack"},
			failing: []string{"/history"},
			want:    []string{"_Incomplete: could not fetch changes._"},
		},
		{
			name: "--slack prints only the message",
			args: []string{"incident", "Orders API", "--slack"},
			want: []string{
				":rotating_light: *Incident on <",
				"|Orders API>* (api · Shop / Orders API · Go)",
				"*Impacted (2 within 2 hops):*",
				"*To notify:* Checkout team, Web team, Jane Doe (jane@shop.test)",
			},
			wantMissing: []string{"# Incident"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out, err := runIncident(t, testCase.failing, testCase.args...)
			require.NoError(t, err)
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, out, w)
			}
		})
	}
}

func TestIncidentCommandJSON(
	t *testing.T,
) {
	out, err := runIncident(t, nil, "incident", "Orders API", "--json")
	require.NoError(t, err)

	var got incident.Report
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "api", got.Element.ID)
	assert.Equal(t, 2, got.Depth)
	assert.Equal(t, []string{"Storefront", "Customer"}, []string{got.Dependents[0].Name, got.Dependents[1].Name})
	assert.Equal(t, []string{"Checkout team", "Web team", "Jane Doe (jane@shop.test)"}, got.Notify.Labels())
	assert.Equal(t, "Orders API", got.ReleasesOf)
	assert.Empty(t, got.Unavailable)
}

func TestIncidentCommandErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "an unknown element points at search",
			args:    []string{"incident", "Nope"},
			wantErr: `no element matches "Nope"; find it with archyl search "Nope"`,
		},
		{
			name:    "depth out of range",
			args:    []string{"incident", "Orders API", "--depth", "5"},
			wantErr: "--depth must be between 1 and 4",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runIncident(t, nil, testCase.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.wantErr)
		})
	}
}

func TestDeliverSlack(
	t *testing.T,
) {
	testCases := []struct {
		name       string
		print      bool
		copyIt     bool
		copyErr    error
		wantOut    string
		wantCopied string
		wantErr    string
	}{
		{
			name:    "print only",
			print:   true,
			wantOut: "message\n",
		},
		{
			name:       "copy only keeps stdout empty",
			copyIt:     true,
			wantCopied: "message\n",
		},
		{
			name:       "both",
			print:      true,
			copyIt:     true,
			wantOut:    "message\n",
			wantCopied: "message\n",
		},
		{
			name:    "no clipboard",
			copyIt:  true,
			copyErr: errors.New("no clipboard utility"),
			wantErr: "copy the Slack message: no clipboard utility; print it instead with archyl incident <element> --slack",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out, status bytes.Buffer
			var copied string
			p := &ui.Printer{Out: &out, Err: &status}
			err := deliverSlack(p, "message\n", testCase.print, testCase.copyIt, func(s string) error {
				copied = s
				return testCase.copyErr
			})
			if testCase.wantErr != "" {
				require.EqualError(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.wantOut, out.String())
			assert.Equal(t, testCase.wantCopied, copied)
			if testCase.copyIt {
				assert.Contains(t, status.String(), "copied to the clipboard")
			}
		})
	}
}

func TestIncidentDiagram(
	t *testing.T,
) {
	g := graph.New(&api.Model{
		Systems: []api.System{
			{ID: "s", Name: "Shop", Containers: []api.Container{{ID: "web", Name: "Storefront"}, {ID: "api", Name: "Orders API"}}},
			{ID: "lone", Name: "Lone"},
		},
		Relationships: []api.Relationship{{ID: "r", SourceID: "web", TargetID: "api", Type: "calls"}},
	})
	testCases := []struct {
		name    string
		element string
		columns string
		want    []string
	}{
		{name: "the element and its neighbours", element: "api", columns: "100", want: []string{"Orders API", "Storefront"}},
		{name: "alone, nothing to draw", element: "lone", columns: "100"},
		{name: "too narrow for boxes", element: "api", columns: "12"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("COLUMNS", testCase.columns)
			got := incidentDiagram(&ui.Printer{Out: io.Discard, Err: io.Discard}, g, g.Node(testCase.element))
			if len(testCase.want) == 0 {
				assert.Empty(t, got)
				return
			}
			for _, w := range testCase.want {
				assert.Contains(t, got, w)
			}
		})
	}
}
