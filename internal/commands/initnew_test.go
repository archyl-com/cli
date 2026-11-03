package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

const (
	initOrg     = "00000000-0000-0000-0000-00000000000a"
	initProject = "00000000-0000-0000-0000-0000000000b1"
)

// fakeInitAPI serves what archyl init --new calls. start answers the
// discovery start; jobStatus is the job's state once polled.
type fakeInitAPI struct {
	mu        sync.Mutex
	calls     []string
	bodies    map[string]map[string]any
	projects  string
	start     func(w http.ResponseWriter)
	jobStatus string
	jobError  string
}

func (f *fakeInitAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
	f.calls = append(f.calls, call)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies[call] = body
	w.Header().Set("Content-Type", "application/json")

	switch call {
	case "GET /projects/":
		_, _ = io.WriteString(w, firstNonEmpty(f.projects, `{"data":[]}`))
	case "GET /organizations/" + initOrg + "/teams":
		_, _ = io.WriteString(w, `{"data":[{"id":"team-view","name":"Readers","role":"viewer"},{"id":"team-edit","name":"Platform","role":"editor"}]}`)
	case "POST /projects/":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"`+initProject+`","name":"`+body["name"].(string)+`","slug":"`+body["slug"].(string)+`"}`)
	case "POST /projects/" + initProject + "/repository":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	case "POST /projects/" + initProject + "/discovery":
		f.start(w)
	case "GET /discovery/jobs/job-1":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "job-1", "status": f.jobStatus, "progress": 100, "error": f.jobError})
	case "GET /projects/" + initProject + "/pending-discoveries":
		_, _ = io.WriteString(w, `{"data":[
			{"id":"p1","discoveryJobId":"job-1","elementType":"system","elementData":{"name":"Shop"}},
			{"id":"p2","discoveryJobId":"job-1","elementType":"container","elementData":{"name":"Checkout API","systemName":"Shop","technology":"Go"}}
		]}`)
	case "POST /projects/" + initProject + "/pending-discoveries/approve":
		_, _ = io.WriteString(w, `{}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"Not Found"}`)
	}
}

func startsJob(w http.ResponseWriter) {
	w.WriteHeader(http.StatusAccepted)
	_, _ = io.WriteString(w, `{"job_id":"job-1","status":"pending","repository":"Acme/Shop","branch":"main"}`)
}

func refuses(status int, message string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
}

// initFixture is a repository with a remote, in an isolated home.
func initFixture(t *testing.T, remote string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ARCHYL_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("ARCHYL_NO_KEYRING", "1")
	t.Setenv("ARCHYL_NO_CACHE", "1")
	t.Setenv("ARCHYL_NO_UPDATE_NOTIFIER", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("ARCHYL_API_KEY", "arch_test")
	t.Setenv("ARCHYL_ORG_ID", initOrg)

	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	if remote != "" {
		out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", remote).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	t.Chdir(dir)
	return dir
}

func TestInitNew(
	t *testing.T,
) {
	discoverPoll = time.Millisecond

	testCases := []struct {
		name        string
		remote      string
		args        []string
		start       func(http.ResponseWriter)
		jobStatus   string
		jobError    string
		wantErr     string
		wantExit    bool
		wantProblem string
		wantCalls   []string
		check       func(t *testing.T, f *fakeInitAPI, out map[string]any)
	}{
		{
			name:      "creates, connects, discovers and approves with --yes",
			remote:    "git@github.com:Acme/Shop.git",
			args:      []string{"init", "--yes", "--json"},
			start:     startsJob,
			jobStatus: "completed",
			wantCalls: []string{
				"GET /projects/",
				"GET /organizations/" + initOrg + "/teams",
				"POST /projects/",
				"POST /projects/" + initProject + "/repository",
				"POST /projects/" + initProject + "/discovery",
				"GET /discovery/jobs/job-1",
				"GET /projects/" + initProject + "/pending-discoveries",
				"POST /projects/" + initProject + "/pending-discoveries/approve",
			},
			check: func(t *testing.T, f *fakeInitAPI, out map[string]any) {
				created := f.bodies["POST /projects/"]
				assert.Equal(t, "Shop", created["name"])
				assert.Equal(t, "team-edit", created["teamId"], "the first team the caller can edit")
				assert.Regexp(t, `^shop-[a-z0-9]+$`, created["slug"])
				linked := f.bodies["POST /projects/"+initProject+"/repository"]
				assert.Equal(t, "github", linked["provider"])
				assert.Equal(t, "Acme/Shop", linked["fullName"])
				assert.Equal(t, "https://github.com/Acme/Shop.git", linked["cloneUrl"])
				assert.Equal(t, "Acme/Shop", f.bodies["POST /projects/"+initProject+"/discovery"]["repository"])
				assert.ElementsMatch(t, []any{"p1", "p2"}, f.bodies["POST /projects/"+initProject+"/pending-discoveries/approve"]["elementIds"])
				discovery := out["discovery"].(map[string]any)
				assert.EqualValues(t, 2, discovery["approved"])
				assert.Equal(t, initProject, out["link"].(map[string]any)["projectId"])
			},
		},
		{
			name:      "--new skips the project list and --no-discover stops after the link",
			remote:    "https://gitlab.com/acme/platform/api.git",
			args:      []string{"init", "--new", "--no-discover", "--name", "Platform API", "--team", "Platform", "--json"},
			wantCalls: []string{"GET /organizations/" + initOrg + "/teams", "POST /projects/", "POST /projects/" + initProject + "/repository"},
			check: func(t *testing.T, f *fakeInitAPI, out map[string]any) {
				assert.Equal(t, "Platform API", f.bodies["POST /projects/"]["name"])
				assert.Equal(t, "gitlab", f.bodies["POST /projects/"+initProject+"/repository"]["provider"])
				assert.Nil(t, out["discovery"])
			},
		},
		{
			name:        "no provider credential keeps the project",
			remote:      "git@github.com:Acme/Shop.git",
			args:        []string{"init", "--new", "--json"},
			start:       refuses(http.StatusConflict, "No github credential for this repository"),
			wantProblem: "credential",
		},
		{
			name:        "the provider refuses the repository before it starts",
			remote:      "git@github.com:Acme/Shop.git",
			args:        []string{"init", "--new", "--json"},
			start:       refuses(http.StatusConflict, "Archyl cannot read Acme/Shop on github with your github sign-in: grant it access"),
			wantExit:    true,
			wantProblem: "access",
		},
		{
			name:        "the plan has no discovery",
			remote:      "git@github.com:Acme/Shop.git",
			args:        []string{"init", "--new", "--json"},
			start:       refuses(http.StatusForbidden, "AI discovery is not available on your current plan."),
			wantProblem: "plan",
		},
		{
			name:        "the provider refuses the repository",
			remote:      "git@github.com:Acme/Shop.git",
			args:        []string{"init", "--new", "--json"},
			start:       startsJob,
			jobStatus:   "failed",
			jobError:    "Failed to fetch repository: failed to get tree: GET https://api.github.com/repos/Acme/Shop/git/trees/main?recursive=1: 404 Not Found []",
			wantExit:    true,
			wantProblem: "access",
		},
		{
			name:    "no remote",
			args:    []string{"init", "--new"},
			wantErr: "no git remote",
		},
		{
			name:    "unknown host needs --provider",
			remote:  "git@git.acme.internal:platform/api.git",
			args:    []string{"init", "--new"},
			wantErr: "--provider",
		},
		{
			name:    "without a terminal nothing is created unasked",
			remote:  "git@github.com:Acme/Shop.git",
			args:    []string{"init"},
			wantErr: "archyl init --new",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := initFixture(t, testCase.remote)
			fake := &fakeInitAPI{bodies: map[string]map[string]any{}, start: testCase.start, jobStatus: testCase.jobStatus, jobError: testCase.jobError}
			server := httptest.NewServer(fake)
			defer server.Close()
			t.Setenv("ARCHYL_HOST", server.URL)

			var stdout bytes.Buffer
			app := &App{Printer: &ui.Printer{Out: &stdout, Err: io.Discard}}
			root := NewRoot(app)
			root.SetArgs(testCase.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()

			if testCase.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), testCase.wantErr)
				assert.NotContains(t, fake.calls, "POST /projects/", "nothing is created")
				return
			}
			if testCase.wantExit {
				var exit *ExitError
				require.ErrorAs(t, err, &exit)
			} else {
				require.NoError(t, err)
			}

			var out map[string]any
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &out), stdout.String())
			if testCase.wantCalls != nil {
				assert.Equal(t, testCase.wantCalls, fake.calls)
			}
			if testCase.wantProblem != "" {
				assert.Equal(t, testCase.wantProblem, out["discovery"].(map[string]any)["problem"])
			}
			link, err := repo.ReadLink(dir)
			require.NoError(t, err, "the link is written whatever the discovery does")
			assert.Equal(t, initProject, link.ProjectID)
			if testCase.check != nil {
				testCase.check(t, fake, out)
			}
		})
	}
}

func TestProjectSlug(
	t *testing.T,
) {
	now := time.UnixMilli(1_700_000_000_000)
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "Payments API", want: "payments-api-loyw3v28"},
		{name: "symbols only", in: "日本", want: "project-loyw3v28"},
		{name: "trimmed", in: "--Shop!!", want: "shop-loyw3v28"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, projectSlug(testCase.in, now))
		})
	}
}

// TestInitNewPlain follows the discovery without a terminal: the tree is
// printed once, then the next steps.
func TestInitNewPlain(
	t *testing.T,
) {
	discoverPoll = time.Millisecond
	initFixture(t, "git@github.com:Acme/Shop.git")
	fake := &fakeInitAPI{bodies: map[string]map[string]any{}, start: startsJob, jobStatus: "completed"}
	server := httptest.NewServer(fake)
	defer server.Close()
	t.Setenv("ARCHYL_HOST", server.URL)
	t.Setenv("NO_COLOR", "1")

	var stdout bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &stdout, Err: io.Discard}}
	root := NewRoot(app)
	root.SetArgs([]string{"init", "--yes"})
	require.NoError(t, root.Execute())

	out := stdout.String()
	for _, want := range []string{
		"Created ◆ Shop",
		"Connected Acme/Shop  GitHub",
		"◆ Shop",
		"└─ ▣ Checkout API",
		"1 system · 1 container",
		"Approved 2 elements",
		"What next",
		"archyl setup",
	} {
		assert.Contains(t, out, want)
	}
}

func TestJobProblem(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		message string
		want    string
	}{
		{name: "github bad credentials", message: "Failed to fetch repository: failed to get tree: GET https://api.github.com/repos/a/b/git/trees/main?recursive=1: 401 Bad credentials []", want: "access"},
		{name: "github not found", message: "Failed to fetch repository: failed to get tree: GET https://api.github.com/repos/a/b/git/trees/main?recursive=1: 404 Not Found []", want: "access"},
		{name: "gitlab not found", message: "Failed to fetch repository: failed to get tree: GET https://gitlab.com/api/v4/projects/a%2Fb/repository/tree: 404 {message: 404 Project Not Found}", want: "access"},
		{name: "bitbucket forbidden", message: "Failed to fetch repository: failed to get tree: status 403", want: "access"},
		{name: "azure devops unauthorized", message: "Failed to fetch repository: failed to get tree: 401 Unauthorized - ", want: "access"},
		{name: "github rate limit", message: "Failed to fetch repository: failed to get tree: GET https://api.github.com/repos/a/b: 403 API rate limit exceeded", want: "failed"},
		{name: "network failure while fetching", message: "Failed to fetch repository: failed to get tree: dial tcp: i/o timeout", want: "failed"},
		{name: "the AI provider cannot be reached", message: "AI analysis failed: failed to access the AI provider: connection refused", want: "failed"},
		{name: "the AI model is missing", message: "AI analysis failed: ollama: 404 model not found", want: "failed"},
		{name: "no reason", message: "", want: "failed"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, jobProblem(testCase.message))
		})
	}
}
