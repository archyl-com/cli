package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// fakeArchyl answers the DSL and discovery endpoints and records what the
// CLI sent.
type fakeArchyl struct {
	mu       sync.Mutex
	diff     api.DSLDiff
	calls    []string
	bodies   map[string]string
	pending  []api.PendingDiscovery
	jobPolls int
	// oldServer makes the DSL schema predate included files.
	oldServer bool
}

func (f *fakeArchyl) serve(t *testing.T) string {
	t.Helper()
	f.bodies = map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		f.calls = append(f.calls, r.Method+" "+path)
		f.bodies[path] = string(raw)

		var body any
		switch {
		case path == "/dsl/schema":
			if f.oldServer {
				_, _ = io.WriteString(w, `{"properties":{"systems":{}}}`)
			} else {
				_, _ = io.WriteString(w, `{"properties":{"systems":{},"containers":{}}}`)
			}
			return
		case strings.HasSuffix(path, "/dsl/export"):
			_, _ = io.WriteString(w, "version: \"1.0\"\nsystems:\n  - name: Shop\n")
			return
		case strings.HasSuffix(path, "/dsl/diff"):
			body = f.diff
		case strings.HasSuffix(path, "/dsl/ingest"):
			body = map[string]any{"import": api.DSLImport{ContainersCreated: 1}}
		case strings.HasSuffix(path, "/discovery") && r.Method == http.MethodPost:
			if strings.Contains(string(raw), "acme/free") {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":true,"message":"AI discovery is not available on your current plan. Please upgrade to Business or Scale to use this feature."}`)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			body = map[string]any{"job_id": "job-1", "status": "pending", "repository": "acme/shop", "branch": "main"}
		case strings.HasPrefix(path, "/discovery/jobs/"):
			f.jobPolls++
			body = api.DiscoveryJob{ID: "job-1", Status: "completed", Progress: 100, CurrentStep: "Done"}
		case strings.HasSuffix(path, "/pending-discoveries"):
			body = map[string]any{"data": f.pending}
		case strings.HasSuffix(path, "/pending-discoveries/approve"), strings.HasSuffix(path, "/pending-discoveries/reject"):
			body = map[string]any{"message": "ok"}
		default:
			body = api.Project{ID: "p1", Name: "Shop", Repositories: []api.Repository{{FullName: "acme/shop"}}}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakeArchyl) called(call string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

// runArchyl runs the CLI against the fake from a directory holding
// archyl.yaml, and returns stdout.
func runArchyl(t *testing.T, f *fakeArchyl, args ...string) (string, error) {
	t.Helper()
	return runArchylIn(t, f, map[string]string{"archyl.yaml": "version: \"1.0\"\nsystems:\n  - name: Shop\n"}, args...)
}

// runArchylIn is runArchyl from a directory holding these files.
func runArchylIn(t *testing.T, f *fakeArchyl, files map[string]string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_HOST", f.serve(t))
	t.Setenv("ARCHYL_API_KEY", "test-key")
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	t.Chdir(dir)

	var out bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &out, Err: io.Discard}}
	root := NewRoot(app)
	root.SetArgs(append(args, "--project", "p1"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

func TestDSLDiff(
	t *testing.T,
) {
	changes := api.DSLDiff{Changes: []api.DSLChange{
		{Action: "modified", ElementType: "system", ElementName: "Shop", Field: "description", DSLValue: "Online store", ModelValue: "Online shop"},
		{Action: "added", ElementType: "container", ElementName: "Shop.Worker"},
		{Action: "removed", ElementType: "container", ElementName: "Shop.Legacy"},
		{Action: "added", ElementType: "relationship", ElementName: "Shop.API → Shop.Worker", DSLValue: "enqueues"},
	}}

	testCases := []struct {
		name     string
		diff     api.DSLDiff
		args     []string
		wantCode int
		want     []string
	}{
		{
			name: "grouped by level",
			diff: changes,
			args: []string{"dsl", "diff"},
			want: []string{"Systems", "~ ◆ Shop", "description  Online shop → Online store", "Containers", "+ ▣ Shop.Worker",
				"− ▣ Shop.Legacy  only in Archyl, push keeps it", "Relationships", "+ Shop.API → Shop.Worker  enqueues",
				"2 to add · 1 to change · 1 only in Archyl"},
		},
		{name: "--exit-code fails on a difference", diff: changes, args: []string{"dsl", "diff", "--exit-code"}, wantCode: 1},
		{name: "--exit-code passes in sync", diff: api.DSLDiff{InSync: true}, args: []string{"dsl", "diff", "--exit-code"}, want: []string{"matches the model"}},
		{name: "--json", diff: changes, args: []string{"dsl", "diff", "--json"}, want: []string{`"action": "removed"`}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f := &fakeArchyl{diff: testCase.diff}
			out, err := runArchyl(t, f, testCase.args...)

			if testCase.wantCode != 0 {
				var exit *ExitError
				require.True(t, errors.As(err, &exit))
				assert.Equal(t, testCase.wantCode, exit.Code)
			} else {
				require.NoError(t, err)
			}
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			assert.Contains(t, f.bodies["/projects/p1/dsl/diff"], "name: Shop", "the file is sent for the server-side dry run")
		})
	}
}

func TestDSLPushAndPull(
	t *testing.T,
) {
	testCases := []struct {
		name string
		args []string
		call string
		want string
	}{
		{name: "push imports without asking off a terminal", args: []string{"dsl", "push"}, call: "POST /projects/p1/dsl/ingest", want: "Pushed archyl.yaml · created 1 container"},
		{name: "sync is push", args: []string{"sync"}, call: "POST /projects/p1/dsl/ingest", want: "Pushed archyl.yaml"},
		{name: "pull to stdout", args: []string{"dsl", "pull", "--file", "-"}, call: "GET /projects/p1/dsl/export", want: "- name: Shop"},
		{name: "pull leaves a matching file alone", args: []string{"dsl", "pull"}, call: "GET /projects/p1/dsl/export", want: "already matches the model"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f := &fakeArchyl{}
			out, err := runArchyl(t, f, testCase.args...)

			require.NoError(t, err)
			assert.True(t, f.called(testCase.call), "calls: %v", f.calls)
			assert.Contains(t, out, testCase.want)
			assert.False(t, f.called("POST /projects/p1/dsl/diff"), "no preview without a terminal")
		})
	}
}

func TestDSLPushSendsIncludedFiles(
	t *testing.T,
) {
	files := map[string]string{
		"archyl.yaml":              "version: \"1.0\"\ninclude: [services/api/archyl.yaml]\nsystems:\n  - name: Shop\n",
		"services/api/archyl.yaml": "containers:\n  - name: API\n    parent_system: Shop\n",
	}
	testCases := []struct {
		name string
		args []string
		call string
		want string
	}{
		{name: "push", args: []string{"dsl", "push"}, call: "/projects/p1/dsl/ingest", want: "Pushed archyl.yaml and 1 included file"},
		{name: "diff", args: []string{"dsl", "diff"}, call: "/projects/p1/dsl/diff", want: "archyl.yaml and 1 included file"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			f := &fakeArchyl{}
			out, err := runArchylIn(t, f, files, testCase.args...)
			require.NoError(t, err)
			assert.Contains(t, out, testCase.want)

			var sent api.DSLSource
			require.NoError(t, json.Unmarshal([]byte(f.bodies[testCase.call]), &sent))
			assert.Equal(t, "archyl.yaml", sent.Path)
			assert.Equal(t, files["archyl.yaml"], sent.Content)
			assert.Equal(t, map[string]string{"services/api/archyl.yaml": files["services/api/archyl.yaml"]}, sent.Files)
		})
	}

	f := &fakeArchyl{}
	_, err := runArchylIn(t, f, map[string]string{"archyl.yaml": "version: \"1.0\"\ninclude: [missing.yaml]\n"}, "dsl", "push")
	require.ErrorContains(t, err, "archyl.yaml includes missing.yaml, which does not exist")
	assert.False(t, f.called("POST /projects/p1/dsl/ingest"), "nothing is pushed without its includes")

	old := &fakeArchyl{oldServer: true}
	_, err = runArchylIn(t, old, files, "dsl", "push")
	require.ErrorContains(t, err, "ignores the files archyl.yaml includes")
	assert.False(t, old.called("POST /projects/p1/dsl/ingest"), "an older server is not sent a partial model")
}

func TestDiscover(
	t *testing.T,
) {
	pendingSystem, _ := json.Marshal(map[string]string{"name": "Shop", "description": "The shop"})
	pendingRel, _ := json.Marshal(map[string]string{"sourceName": "API", "targetName": "DB"})
	pending := []api.PendingDiscovery{
		{ID: "r1", ElementType: "relationship", ElementData: pendingRel},
		{ID: "s1", ElementType: "system", ElementData: pendingSystem},
	}

	testCases := []struct {
		name    string
		args    []string
		want    []string
		call    string
		body    string
		wantErr string
	}{
		{name: "waits, then lists what awaits review", args: []string{"discover"}, want: []string{"acme/shop discovered", "2 elements await review"}, call: "GET /discovery/jobs/job-1"},
		{name: "--no-wait --json returns the job", args: []string{"discover", "--no-wait", "--json"}, want: []string{`"id": "job-1"`}, body: `"repository":"acme/shop"`},
		{name: "plan gating reads the server's message", args: []string{"discover", "--repo", "acme/free"}, wantErr: "not available on your current plan"},
		{name: "review lists by level off a terminal", args: []string{"discover", "review"}, want: []string{"SYSTEMS", "◆ Shop", "RELATIONSHIPS", "API → DB"}},
		{name: "review --approve-all", args: []string{"discover", "review", "--approve-all"}, want: []string{"Approved 2 elements"}, call: "POST /projects/p1/pending-discoveries/approve", body: `"s1"`},
		{name: "review --json", args: []string{"discover", "review", "--json"}, want: []string{`"level": "system"`, `"name": "API → DB"`}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			saved := discoverPoll
			discoverPoll = 0
			t.Cleanup(func() { discoverPoll = saved })
			f := &fakeArchyl{pending: pending}
			out, err := runArchyl(t, f, testCase.args...)

			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
				return
			}
			require.NoError(t, err)
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			if testCase.call != "" {
				assert.True(t, f.called(testCase.call), "calls: %v", f.calls)
			}
			if testCase.body != "" {
				all, _ := json.Marshal(f.bodies)
				assert.Contains(t, strings.ReplaceAll(string(all), `\"`, `"`), testCase.body)
			}
		})
	}
}
