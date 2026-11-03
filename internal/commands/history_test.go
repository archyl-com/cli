package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/history"
	"github.com/archyl-com/cli/internal/ui"
)

// historySnapshots are the model at REV 1 to 3: the shop's API alone, then a
// worker added, then the API renamed and Mailgun added. Now, the worker is
// gone.
var historySnapshots = map[string]string{
	"1": `{"systems":[{"id":"s","name":"Shop","type":"software_system"},{"id":"x","name":"Stripe","isExternal":true}],` +
		`"containers":[{"id":"api","systemId":"s","name":"API","containerType":"api"}],` +
		`"relationships":[{"id":"r1","sourceId":"api","targetId":"x","type":"calls","description":"charges cards"}]}`,
	"2": `{"systems":[{"id":"s","name":"Shop","type":"software_system"},{"id":"x","name":"Stripe","isExternal":true}],` +
		`"containers":[{"id":"api","systemId":"s","name":"API","containerType":"api"},{"id":"w","systemId":"s","name":"Worker","containerType":"worker"}],` +
		`"relationships":[{"id":"r1","sourceId":"api","targetId":"x","type":"calls","description":"charges cards"},` +
		`{"id":"r2","sourceId":"api","targetId":"w","type":"queues","description":"queues jobs"}]}`,
	"3": `{"systems":[{"id":"s","name":"Shop","type":"software_system"},{"id":"x","name":"Stripe","isExternal":true},{"id":"m","name":"Mailgun","isExternal":true}],` +
		`"containers":[{"id":"api","systemId":"s","name":"Orders API","containerType":"api"},{"id":"w","systemId":"s","name":"Worker","containerType":"worker"}],` +
		`"relationships":[{"id":"r1","sourceId":"api","targetId":"x","type":"calls","description":"charges cards"},` +
		`{"id":"r2","sourceId":"api","targetId":"w","type":"queues","description":"queues jobs"},` +
		`{"id":"r3","sourceId":"w","targetId":"m","type":"sends","description":"sends receipts"}]}`,
}

const historyNow = `{"systems":[{"id":"s","name":"Shop","type":"software_system","containers":[` +
	`{"id":"api","name":"Orders API","containerType":"api","technologies":[{"name":"Go"}]}]},` +
	`{"id":"x","name":"Stripe","isExternal":true},{"id":"m","name":"Mailgun","isExternal":true}],` +
	`"relationships":[{"id":"r1","projectId":"p1","sourceId":"api","targetId":"x","type":"calls","description":"charges cards"},` +
	`{"id":"other","projectId":"p2","sourceId":"m","targetId":"x","type":"calls"}]}`

const historyVersions = `{"data":[` +
	`{"number":3,"userName":"Grace","description":"Rename the API","createdAt":"2026-09-20T10:00:00Z"},` +
	`{"number":2,"userName":"Ada","createdAt":"2026-09-10T10:00:00Z"},` +
	`{"number":1,"userName":"Ada","createdAt":"2026-09-01T10:00:00Z"}],"total":3,"page":1,"pages":1}`

const historyReleases = `{"data":[` +
	`{"id":"b","version":"v1.1.0","releasedAt":"2026-09-15T10:00:00Z","createdAt":"2026-09-15T10:00:00Z"},` +
	`{"id":"a","version":"v1.0.0","releasedAt":"2026-09-05T10:00:00Z","createdAt":"2026-09-05T10:00:00Z"}]}`

// historyAPI answers what archyl history reads. versions replaces the list
// of versions when set.
func historyAPI(t *testing.T, versions string) string {
	t.Helper()
	if versions == "" {
		versions = historyVersions
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/projects/p1":
			_, _ = io.WriteString(w, `{"id":"p1","name":"Shop"}`)
		case path == "/projects/p1/c4":
			_, _ = io.WriteString(w, historyNow)
		case path == "/projects/p1/versions":
			_, _ = io.WriteString(w, versions)
		case strings.HasPrefix(path, "/projects/p1/versions/"):
			n := strings.TrimPrefix(path, "/projects/p1/versions/")
			snap, ok := historySnapshots[n]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprintf(w, `{"number":%s,"createdAt":"2026-09-01T10:00:00Z","snapshotData":%s}`, n, snap)
		case path == "/projects/p1/releases":
			_, _ = io.WriteString(w, historyReleases)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// runHistoryCmd runs archyl history against historyAPI and returns stdout.
func runHistoryCmd(
	t *testing.T,
	versions string,
	args ...string,
) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_NO_CACHE", "1")
	t.Setenv("ARCHYL_HOST", historyAPI(t, versions))
	t.Setenv("ARCHYL_API_KEY", "test-key")
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &out, Err: io.Discard}}
	root := NewRoot(app)
	root.SetArgs(append(append([]string{"history"}, args...), "--project", "p1"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

func TestHistoryCommand(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		args        []string
		want        []string
		wantMissing []string
	}{
		{
			name: "without a range, the version before the latest to now",
			want: []string{
				"## Architecture changes REV 2 → now\n",
				"REV 2 (2026-09-10) → now · elements: 1 added, 1 removed, 1 modified; relationships: 1 removed",
				"### Added\n\n- **Mailgun** (system)\n",
				"### Removed\n\n- **Worker** (container in Shop)\n",
				"### Modified\n\n- **Orders API** (container in Shop): renamed from \"API\"\n",
				"- and 1 relationship removed with the elements above",
			},
			wantMissing: []string{"```mermaid", "technology", "other"},
		},
		{
			name: "between two releases",
			args: []string{"v1.0.0..v1.1.0"},
			want: []string{
				"## Architecture changes v1.0.0 → v1.1.0\n",
				"v1.0.0 (REV 1, 2026-09-01) → v1.1.0 (REV 2, 2026-09-10)",
				"- **Worker** (container in Shop)",
				"- added: API → Worker (queues jobs)",
			},
		},
		{
			name: "a lone release runs to now",
			args: []string{"1.0.0"},
			want: []string{"## Architecture changes v1.0.0 → now"},
		},
		{
			name: "a lone point runs to now",
			args: []string{"rev1"},
			want: []string{"## Architecture changes REV 1 → now", "### Modified"},
		},
		{
			name:        "an element scopes the changes",
			args:        []string{"1..3", "Mailgun"},
			want:        []string{"## Architecture changes in Mailgun, REV 1 → REV 3", "- **Mailgun** (system)", "- added: Worker → Mailgun (sends receipts)"},
			wantMissing: []string{"Orders API"},
		},
		{
			name: "a lone element",
			args: []string{"Orders API"},
			want: []string{"## Architecture changes in Orders API, REV 2 → now", "renamed from"},
		},
		{
			name: "--markdown adds the diagram",
			args: []string{"1..3", "--markdown"},
			want: []string{"```mermaid\nflowchart TB\n", "<b>+ Worker</b>", "<b>~ Orders API</b>"},
		},
		{
			name:        "--format mermaid writes the diagram alone",
			args:        []string{"2..", "--format", "mermaid"},
			want:        []string{"flowchart TB\n", "<b>− Worker</b>", "classDef removed"},
			wantMissing: []string{"##"},
		},
		{
			name: "--format text draws it",
			args: []string{"2..", "--format", "text"},
			want: []string{"Containers of Shop · REV 2 → now", "━ ~ ━", "╌ − ╌", "+ added"},
		},
		{
			name: "no changes",
			args: []string{"3..", "Stripe"},
			want: []string{"· no changes", "No architecture changes."},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out, err := runHistoryCmd(t, "", testCase.args...)
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

func TestHistoryCommandJSON(
	t *testing.T,
) {
	out, err := runHistoryCmd(t, "", "v1.0.0..", "--json")
	require.NoError(t, err)

	var got struct {
		From    history.Resolved `json:"from"`
		To      history.Resolved `json:"to"`
		Summary history.Summary  `json:"summary"`
		Changes history.Diff     `json:"changes"`
		Diagram struct {
			Title string `json:"title"`
			Nodes []struct {
				ID     string `json:"id"`
				Change string `json:"change"`
			} `json:"nodes"`
		} `json:"diagram"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "v1.0.0", got.From.Label)
	assert.Equal(t, 1, got.From.Number())
	assert.True(t, got.To.IsNow())
	assert.Equal(t, history.Summary{Added: 1, Modified: 1}, got.Summary)
	require.Len(t, got.Changes.Elements, 2)
	assert.Equal(t, "Containers of Shop · v1.0.0 → now", got.Diagram.Title)
	changes := map[string]string{}
	for _, n := range got.Diagram.Nodes {
		changes[n.ID] = n.Change
	}
	assert.Equal(t, map[string]string{"api": "changed", "x": "", "m": "added"}, changes)
}

func TestHistoryCommandWritesFiles(
	t *testing.T,
) {
	testCases := []struct {
		file string
		want string
	}{
		{file: "changes.svg", want: "<svg"},
		{file: "changes.mmd", want: "flowchart TB"},
		{file: "changes.md", want: "```mermaid"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.file, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, testCase.file)
			_, err := runHistoryCmd(t, "", "1..", "-o", path)
			require.NoError(t, err)
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(raw), testCase.want)
		})
	}
}

func TestHistoryCommandErrors(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		versions string
		args     []string
		wantErr  string
	}{
		{
			name:     "a project without versions",
			versions: `{"data":[],"pages":0}`,
			wantErr:  "no versions of this project's model yet: Archyl records one when the model is edited",
		},
		{
			name:    "an unknown release",
			args:    []string{"v1.2.0.."},
			wantErr: `no release "v1.2.0"; the closest are v1.1.0, v1.0.0`,
		},
		{
			name:    "a version not kept",
			args:    []string{"7..now"},
			wantErr: "there is no REV 7: Archyl keeps REV 1 to REV 3",
		},
		{
			name:    "a date before every version",
			args:    []string{"2026-08-01.."},
			wantErr: "no version at or before 2026-08-01",
		},
		{
			name:    "both ends the same",
			args:    []string{"2..2026-09-12"},
			wantErr: "nothing to compare: both ends are REV 2 (2026-09-10); widen the range, such as archyl history 1..now",
		},
		{
			name:    "an unknown element",
			args:    []string{"1..", "Nope"},
			wantErr: `no element matches "Nope" at either end of the range; find one with archyl search Nope`,
		},
		{
			name:    "--inside without an element",
			args:    []string{"1..", "--inside"},
			wantErr: "name an element: archyl history <range> <element> --inside",
		},
		{
			name:    "--inside an element without children",
			args:    []string{"1..", "Stripe", "--inside"},
			wantErr: "Stripe holds no elements to draw inside it",
		},
		{
			name:    "--play outside a terminal",
			args:    []string{"--play"},
			wantErr: "--play needs a terminal",
		},
		{
			name:    "--play with --json",
			args:    []string{"--play", "--json"},
			wantErr: "--play replays full screen; it does not go with --json",
		},
		{
			name:    "--markdown with a format",
			args:    []string{"--markdown", "--format", "svg"},
			wantErr: "--markdown prints the whole report",
		},
		{
			name:    "a broken range",
			args:    []string{"2026-02-30.."},
			wantErr: "is not a date",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := runHistoryCmd(t, testCase.versions, testCase.args...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), testCase.wantErr)
		})
	}
}

func TestSplitHistoryArgs(
	t *testing.T,
) {
	testCases := []struct {
		name string
		args []string
		want historyArgs
	}{
		{name: "nothing", want: historyArgs{}},
		{name: "a range", args: []string{"v1..v2"}, want: historyArgs{span: "v1..v2"}},
		{name: "a point", args: []string{"30d"}, want: historyArgs{span: "30d"}},
		{name: "a recorded release", args: []string{"v1.0.0"}, want: historyArgs{span: "v1.0.0"}},
		{name: "an element", args: []string{"Billing API"}, want: historyArgs{element: "Billing API"}},
		{name: "both", args: []string{"12..", "Billing"}, want: historyArgs{span: "12..", element: "Billing"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			releases := []api.ReleaseDetail{{Version: "v1.0.0"}}
			assert.Equal(t, testCase.want, splitHistoryArgs(testCase.args, releases))
		})
	}
}

func TestPrintHistoryReport(
	t *testing.T,
) {
	model := func(n string) *api.Model {
		v := api.Version{Number: 1, Snapshot: []byte(historySnapshots[n])}
		m, err := v.Model()
		require.NoError(t, err)
		return m
	}
	from := history.Resolved{Label: "REV 1", Version: &api.Version{Number: 1}}
	to := history.Resolved{Label: "now"}
	testCases := []struct {
		name        string
		before      string
		after       string
		element     string
		want        []string
		wantMissing []string
	}{
		{
			name:   "changes",
			before: "1",
			after:  "3",
			want: []string{
				"history  REV 1 (", "→ now",
				"elements: 2 added, 1 modified; relationships: 2 added",
				"━ + ━", "+ added   ~ changed   − removed",
				"  Added\n    + ◆ Mailgun  system\n    + ▣ Worker  container in Shop\n",
				"  Modified\n    ~ ▣ Orders API  container in Shop\n      renamed from \"API\"\n",
				"  Relationships\n    + Orders API → Worker  queues jobs\n",
			},
		},
		{
			name:   "removed with its relationships",
			before: "3",
			after:  "2",
			want:   []string{"  Removed\n    − ◆ Mailgun  system\n", "and 1 relationship removed with the elements above"},
		},
		{
			name:        "scoped to an element",
			before:      "1",
			after:       "3",
			element:     "Worker",
			want:        []string{"history  Worker  REV 1", "+ ▣ Worker  container in Shop"},
			wantMissing: []string{"Orders API  container"},
		},
		{
			name:        "nothing changed",
			before:      "2",
			after:       "2",
			want:        []string{"no changes", "No architecture changes between REV 1 and now.", "archyl history 30d.."},
			wantMissing: []string{"Added", "┌"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r, err := buildReport(model(testCase.before), model(testCase.after), from, to, testCase.element, false)
			require.NoError(t, err)
			var out bytes.Buffer
			printHistoryReport(&ui.Printer{Out: &out, Err: io.Discard}, r)
			for _, w := range testCase.want {
				assert.Contains(t, out.String(), w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, out.String(), w)
			}
		})
	}
}

func TestReplayOptions(
	t *testing.T,
) {
	at := func(n int) history.Resolved {
		return history.Resolved{Label: "REV", Version: &api.Version{Number: n}}
	}
	now := history.Resolved{Label: "now"}
	testCases := []struct {
		name       string
		from, to   history.Resolved
		element    string
		inside     bool
		wantFrames []int
		wantTitle  string
		wantErr    string
	}{
		{name: "every version, then now", from: at(1), to: now, wantFrames: []int{1, 2, 3, 0}, wantTitle: "Containers of Shop"},
		{name: "between two versions", from: at(2), to: at(3), wantFrames: []int{2, 3}, wantTitle: "Containers of Shop"},
		{name: "centred on an element", from: at(1), to: at(3), element: "Worker", wantFrames: []int{1, 2, 3}, wantTitle: "System context"},
		{name: "backwards", from: at(3), to: at(1), wantErr: "--play runs forward in time"},
		{name: "from now", from: now, to: at(1), wantErr: "--play runs forward in time"},
		{name: "an unknown element", from: at(1), to: now, element: "Nope", wantErr: `no element matches "Nope"`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			client := api.NewClient(historyAPI(t, ""), "", nil)
			src, err := loadHistory(t.Context(), client, "p1")
			require.NoError(t, err)

			opts, err := replayOptions(t.Context(), &ui.Printer{Out: io.Discard, Err: io.Discard}, client, "p1",
				src, testCase.from, testCase.to, testCase.element, testCase.inside)

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "Shop", opts.Project)
			var numbers []int
			for _, f := range opts.Frames {
				n := 0
				if f.Version != nil {
					n = f.Version.Number
				}
				numbers = append(numbers, n)
			}
			assert.Equal(t, testCase.wantFrames, numbers)
			assert.Equal(t, testCase.wantTitle, opts.Frames[0].View.Title)
			assert.Empty(t, opts.Note)
		})
	}
}
