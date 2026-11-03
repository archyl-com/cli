package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/ui"
)

const codeownersModel = `{"systems":[{"id":"shop","name":"Shop","containers":[` +
	`{"id":"orders","name":"Orders API","components":[{"id":"refunds","name":"Refunds"}]},` +
	`{"id":"web","name":"Storefront"},` +
	`{"id":"billing","name":"Billing"}]}],"relationships":[]}`

// codeownersAPIOptions shapes the fake API.
type codeownersAPIOptions struct {
	// noMap answers the ownership map with the router's 404, as a server
	// without the route does.
	noMap bool
	// owned is false for a project nobody owns anything in.
	owned bool
	// mapStatus and elementStatus, when set, fail the ownership map and the
	// per-element owners with that status.
	mapStatus     int
	elementStatus int
}

// codeownersOwners are the owners of the fake model, as the ownership map
// writes them.
var codeownersOwners = map[string]string{
	"orders": `"owners":[{"email":"ada@example.com","firstName":"Ada","lastName":"Lovelace"}],` +
		`"teamOwners":[{"name":"Checkout","slug":"checkout"}]`,
	"web":     `"owners":[],"teamOwners":[{"name":"Front","slug":"front"}]`,
	"billing": `"owners":[{"email":"bob@example.com"}],"teamOwners":[]`,
}

// codeownersElementOwners are the same owners as the per-element endpoint
// writes them.
var codeownersElementOwners = map[string]string{
	"orders": `{"data":[{"user":{"email":"ada@example.com","firstName":"Ada","lastName":"Lovelace"}}],` +
		`"teamOwners":[{"team":{"name":"Checkout","slug":"checkout"}}]}`,
	"web":     `{"data":[],"teamOwners":[{"team":{"name":"Front","slug":"front"}}]}`,
	"billing": `{"data":[{"user":{"email":"bob@example.com"}}],"teamOwners":[]}`,
}

func codeownersAPI(
	t *testing.T,
	opts codeownersAPIOptions,
) string {
	t.Helper()
	owners := func(id string) string {
		if o, ok := codeownersOwners[id]; ok && opts.owned {
			return o
		}
		return `"owners":[],"teamOwners":[]`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/projects/p1/c4":
			_, _ = io.WriteString(w, codeownersModel)
		case path == "/projects/p1/adrs":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case path == "/global/ownership-map" && opts.mapStatus != 0:
			w.WriteHeader(opts.mapStatus)
			_, _ = io.WriteString(w, `{"error":"no"}`)
		case strings.HasSuffix(path, "/owners") && opts.elementStatus != 0:
			w.WriteHeader(opts.elementStatus)
			_, _ = io.WriteString(w, `{"error":"boom"}`)
		case path == "/global/ownership-map" && opts.noMap:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":true,"message":"Not Found"}`)
		case path == "/global/ownership-map":
			var elements []string
			for _, id := range []string{"shop", "orders", "refunds", "web", "billing"} {
				elements = append(elements, `{"id":"`+id+`",`+owners(id)+`}`)
			}
			_, _ = io.WriteString(w, `{"elements":[{"id":"elsewhere","owners":[],"teamOwners":[]},`+strings.Join(elements, ",")+`]}`)
		case strings.HasPrefix(path, "/c4/") && strings.HasSuffix(path, "/owners"):
			body := `{"data":[],"teamOwners":[]}`
			if o, ok := codeownersElementOwners[strings.Split(path, "/")[3]]; ok && opts.owned {
				body = o
			}
			_, _ = io.WriteString(w, body)
		default:
			_, _ = io.WriteString(w, `{"id":"p1","name":"Shop"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// codeownersRepo makes a git work tree, without commits, holding files and
// an origin remote.
func codeownersRepo(
	t *testing.T,
	remote string,
	files map[string]string,
) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	out, err := exec.Command("git", "init", "-q", dir).CombinedOutput()
	require.NoError(t, err, string(out))
	if remote != "" {
		out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", remote).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	return dir
}

// shopTree is the repository the fake model describes.
func shopTree() map[string]string {
	return map[string]string{
		"services/orders/main.go":           "package main\n",
		"services/orders/refunds/refund.go": "package refunds\n",
		"web/storefront/app.tsx":            "export {}\n",
		".archyl.json":                      `{"projectId":"p1","codeowners":{"users":{"ada@example.com":"@ada"}}}`,
	}
}

type codeownersRun struct {
	stdout, stderr string
	err            error
}

func runCodeownersCmd(
	t *testing.T,
	dir string,
	opts codeownersAPIOptions,
	args ...string,
) codeownersRun {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ARCHYL_CONFIG_DIR", t.TempDir())
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	t.Setenv("ARCHYL_NO_CACHE", "1")
	t.Setenv("ARCHYL_NO_KEYRING", "1")
	t.Setenv("ARCHYL_HOST", codeownersAPI(t, opts))
	t.Setenv("ARCHYL_API_KEY", "test-key")
	t.Chdir(dir)

	var stdout, stderr bytes.Buffer
	app := &App{Printer: &ui.Printer{Out: &stdout, Err: &stderr}}
	root := NewRoot(app)
	if found, _, err := root.Find([]string{"codeowners"}); err != nil || found.Name() != "codeowners" {
		root.AddCommand(newCodeownersCmd(app))
	}
	root.SetArgs(append([]string{"codeowners"}, args...))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	return codeownersRun{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func TestCodeownersCommand(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		remote      string
		opts        codeownersAPIOptions
		args        []string
		want        []string
		wantMissing []string
		wantStderr  []string
	}{
		{
			name:   "prints the file with the remote's owner as org",
			remote: "git@github.com:Acme/shop.git",
			opts:   codeownersAPIOptions{owned: true},
			want: []string{
				"# BEGIN archyl codeowners — generated by archyl codeowners, do not edit\n" +
					"# Orders API (container)\n" +
					"/services/orders/ @Acme/checkout @ada\n" +
					"# Storefront (container)\n" +
					"/web/storefront/ @Acme/front\n" +
					"# END archyl codeowners\n",
			},
			wantMissing: []string{"/services/orders/refunds/"},
			wantStderr:  []string{"1 owned element has no folder in this repository: Billing (container)"},
		},
		{
			name:   "--org wins over the remote",
			remote: "git@github.com:Acme/shop.git",
			opts:   codeownersAPIOptions{owned: true},
			args:   []string{"--org", "tf1"},
			want:   []string{"/services/orders/ @tf1/checkout @ada", "/web/storefront/ @tf1/front"},
		},
		{
			name: "without an org, teams are reported",
			opts: codeownersAPIOptions{owned: true},
			want: []string{"/services/orders/ @ada\n"},
			wantStderr: []string{
				"2 owners have no handle and were left out: team Checkout (Orders API, Refunds); team Front (Storefront)",
				"or pass --org",
			},
			wantMissing: []string{"/web/storefront/"},
		},
		{
			name:   "falls back to one call per element on an older server",
			remote: "https://github.com/acme/shop",
			opts:   codeownersAPIOptions{owned: true, noMap: true},
			want:   []string{"/services/orders/ @acme/checkout @ada", "/web/storefront/ @acme/front"},
		},
		{
			name:       "nobody owns anything",
			remote:     "https://github.com/acme/shop",
			opts:       codeownersAPIOptions{},
			wantStderr: []string{"No element of this project has owners yet", "archyl open <element>"},
			wantMissing: []string{
				"BEGIN",
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := codeownersRepo(t, testCase.remote, shopTree())
			run := runCodeownersCmd(t, dir, testCase.opts, testCase.args...)
			require.NoError(t, run.err)
			for _, w := range testCase.want {
				assert.Contains(t, run.stdout, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, run.stdout, w)
			}
			for _, w := range testCase.wantStderr {
				assert.Contains(t, run.stderr, w)
			}
			assert.NoFileExists(t, filepath.Join(dir, ".github", "CODEOWNERS"), "printing never writes")
		})
	}
}

func TestCodeownersWriteAndCheck(
	t *testing.T,
) {
	opts := codeownersAPIOptions{owned: true}
	dir := codeownersRepo(t, "git@github.com:acme/shop.git", shopTree())
	file := filepath.Join(dir, ".github", "CODEOWNERS")

	run := runCodeownersCmd(t, dir, opts, "--check")
	var exit *ExitError
	require.ErrorAs(t, run.err, &exit, "a missing file is out of date")
	assert.Equal(t, 1, exit.Code)
	assert.Contains(t, run.stdout, "+++ b/.github/CODEOWNERS")
	assert.Contains(t, run.stdout, "+/services/orders/ @acme/checkout @ada")
	assert.Contains(t, run.stderr, "archyl codeowners --write")

	run = runCodeownersCmd(t, dir, opts, "--write")
	require.NoError(t, run.err)
	assert.Contains(t, run.stdout, "Wrote .github/CODEOWNERS · 2 lines")
	written, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Contains(t, string(written), "/web/storefront/ @acme/front\n")

	run = runCodeownersCmd(t, dir, opts, "--check")
	require.NoError(t, run.err)
	assert.Contains(t, run.stderr, ".github/CODEOWNERS matches the owners in Archyl")

	run = runCodeownersCmd(t, dir, opts, "--write")
	require.NoError(t, run.err)
	assert.Contains(t, run.stdout, "already matches")

	edited := "* @acme/leads\n" + strings.Replace(string(written), "@acme/front", "@acme/old", 1)
	require.NoError(t, os.WriteFile(file, []byte(edited), 0o644))
	run = runCodeownersCmd(t, dir, opts, "--check")
	require.ErrorAs(t, run.err, &exit)
	assert.Contains(t, run.stdout, "-/web/storefront/ @acme/old\n+/web/storefront/ @acme/front\n")

	run = runCodeownersCmd(t, dir, opts, "--write", "--yes")
	require.NoError(t, run.err)
	rewritten, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(rewritten), "* @acme/leads\n# BEGIN archyl codeowners"), "hand-written lines stay")
	assert.Contains(t, string(rewritten), "/web/storefront/ @acme/front\n")
}

func TestCodeownersExistingFiles(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		remote   string
		existing map[string]string
		args     []string
		wantFile string
		want     string
	}{
		{
			name:     "a root CODEOWNERS without markers gets the block above its rules, CRLF kept",
			remote:   "git@github.com:acme/shop.git",
			existing: map[string]string{"CODEOWNERS": "* @acme/leads\r\n"},
			wantFile: "CODEOWNERS",
			want: "# BEGIN archyl codeowners — generated by archyl codeowners, do not edit\r\n" +
				"# Orders API (container)\r\n/services/orders/ @acme/checkout @ada\r\n" +
				"# Storefront (container)\r\n/web/storefront/ @acme/front\r\n# END archyl codeowners\r\n" +
				"\r\n* @acme/leads\r\n",
		},
		{
			name:     "a GitLab remote without a file writes under .gitlab",
			remote:   "git@gitlab.com:acme/video/shop.git",
			wantFile: ".gitlab/CODEOWNERS",
			want:     "/services/orders/ @acme/video/checkout @ada\n",
		},
		{
			name:     "--file picks the file",
			remote:   "git@github.com:acme/shop.git",
			args:     []string{"--file", "docs/OWNERS"},
			wantFile: "docs/OWNERS",
			want:     "/web/storefront/ @acme/front\n",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tree := shopTree()
			for name, content := range testCase.existing {
				tree[name] = content
			}
			dir := codeownersRepo(t, testCase.remote, tree)
			run := runCodeownersCmd(t, dir, codeownersAPIOptions{owned: true}, append(testCase.args, "--write")...)
			require.NoError(t, run.err)
			got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(testCase.wantFile)))
			require.NoError(t, err)
			assert.Contains(t, string(got), testCase.want)
		})
	}
}

func TestCodeownersJSON(
	t *testing.T,
) {
	dir := codeownersRepo(t, "git@github.com:acme/shop.git", shopTree())
	run := runCodeownersCmd(t, dir, codeownersAPIOptions{owned: true}, "--json")
	require.NoError(t, run.err)

	var got codeownersReport
	require.NoError(t, json.Unmarshal([]byte(run.stdout), &got))
	assert.Equal(t, ".github/CODEOWNERS", got.File)
	assert.False(t, got.InSync)
	require.Len(t, got.Entries, 2)
	assert.Equal(t, "/services/orders/", got.Entries[0].Pattern)
	assert.Equal(t, "Orders API", got.Entries[0].Elements[0].Name)
	assert.Equal(t, []string{"@acme/checkout", "@ada"}, got.Entries[0].Owners)
	require.Len(t, got.Unplaced, 1)
	assert.Equal(t, "Billing", got.Unplaced[0].Name)
	assert.Equal(t, 3, got.Owned)
}

func TestCodeownersUnterminatedBlock(
	t *testing.T,
) {
	tree := shopTree()
	tree[".github/CODEOWNERS"] = "# BEGIN archyl codeowners\n/x/ @y\n"
	dir := codeownersRepo(t, "git@github.com:acme/shop.git", tree)
	run := runCodeownersCmd(t, dir, codeownersAPIOptions{owned: true}, "--write", "--yes")
	require.Error(t, run.err)
	assert.Contains(t, run.err.Error(), "has no END line")
}

func TestRemoteHost(
	t *testing.T,
) {
	testCases := []struct {
		remote     string
		wantOwner  string
		wantGitlab bool
	}{
		{remote: "git@github.com:Acme/shop.git", wantOwner: "Acme"},
		{remote: "https://gitlab.com/acme/video/shop.git", wantOwner: "acme/video", wantGitlab: true},
		{remote: "git@gitlab.example.com:Platform/shop.git", wantOwner: "Platform", wantGitlab: true},
		{remote: "https://code.example.com/team/shop", wantOwner: "team"},
		{remote: "ssh://git@Code.Example.com:2222/Team/Shop.git", wantOwner: "Team"},
		{remote: "git@git.example.com:Video/Team/shop.git", wantOwner: "Video/Team"},
		{remote: "https://GitLab.Example.com/Group/shop.git", wantOwner: "Group", wantGitlab: true},
		{remote: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.remote, func(t *testing.T) {
			owner, gitlab := remoteHost(testCase.remote)
			assert.Equal(t, testCase.wantOwner, owner)
			assert.Equal(t, testCase.wantGitlab, gitlab)
		})
	}
}

func TestCodeownersJSONModes(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		args        []string
		wantInSync  bool
		wantWritten bool
		wantDiff    bool
		wantExit    bool
	}{
		{name: "check out of date", args: []string{"--check"}, wantDiff: true, wantExit: true},
		{name: "write", args: []string{"--write"}, wantInSync: true, wantWritten: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := codeownersRepo(t, "git@github.com:acme/shop.git", shopTree())
			run := runCodeownersCmd(t, dir, codeownersAPIOptions{owned: true}, append(testCase.args, "--json")...)
			var exit *ExitError
			assert.Equal(t, testCase.wantExit, errors.As(run.err, &exit))

			var got codeownersReport
			require.NoError(t, json.Unmarshal([]byte(run.stdout), &got))
			assert.Equal(t, testCase.wantInSync, got.InSync)
			assert.Equal(t, testCase.wantWritten, got.Written)
			assert.Equal(t, testCase.wantDiff, got.Diff != "")
			assert.Empty(t, run.stderr, "--json keeps stderr quiet")
		})
	}
}

func TestCodeownersWithoutOwners(
	t *testing.T,
) {
	stale := "/docs/ @acme/writers\n\n" + codeownersBlockStart + "\n/services/orders/ @acme/checkout\n# END archyl codeowners\n"
	testCases := []struct {
		name     string
		existing string
		args     []string
		wantExit bool
		wantFile string
	}{
		{name: "a stale block fails the check", existing: stale, args: []string{"--check"}, wantExit: true, wantFile: stale},
		{name: "no block passes the check", existing: "/docs/ @acme/writers\n", args: []string{"--check"}, wantFile: "/docs/ @acme/writers\n"},
		{name: "--write removes a stale block", existing: stale, args: []string{"--write"}, wantFile: "/docs/ @acme/writers\n"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tree := shopTree()
			tree[".github/CODEOWNERS"] = testCase.existing
			dir := codeownersRepo(t, "git@github.com:acme/shop.git", tree)
			run := runCodeownersCmd(t, dir, codeownersAPIOptions{}, testCase.args...)
			var exit *ExitError
			assert.Equal(t, testCase.wantExit, errors.As(run.err, &exit), "error: %v", run.err)
			if testCase.wantExit {
				assert.Contains(t, run.stdout, "-/services/orders/ @acme/checkout")
			} else {
				require.NoError(t, run.err)
			}
			got, err := os.ReadFile(filepath.Join(dir, ".github", "CODEOWNERS"))
			require.NoError(t, err)
			assert.Equal(t, testCase.wantFile, string(got))
		})
	}
}

// codeownersBlockStart is the BEGIN line the command writes.
const codeownersBlockStart = "# BEGIN archyl codeowners — generated by archyl codeowners, do not edit"

func TestCodeownersCheckIgnoresLineEndings(
	t *testing.T,
) {
	block := codeownersBlockStart + "\n# Orders API (container)\n/services/orders/ @acme/checkout @ada\n" +
		"# Storefront (container)\n/web/storefront/ @acme/front\n# END archyl codeowners"
	testCases := []struct {
		name     string
		existing string
	}{
		{name: "the END line without a final newline", existing: block},
		{name: "mixed CRLF and LF", existing: strings.Replace(block, "\n", "\r\n", 2) + "\n"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tree := shopTree()
			tree[".github/CODEOWNERS"] = testCase.existing
			dir := codeownersRepo(t, "git@github.com:acme/shop.git", tree)
			run := runCodeownersCmd(t, dir, codeownersAPIOptions{owned: true}, "--check")
			require.NoError(t, run.err, run.stdout)
			assert.Empty(t, run.stdout)
		})
	}
}

func TestCodeownersFileStaysInTheRepository(
	t *testing.T,
) {
	outside := t.TempDir()
	testCases := []struct {
		name    string
		file    string
		symlink string // a folder of the repository linked to outside
		wantErr string
	}{
		{name: "a parent path", file: "../../CODEOWNERS", wantErr: "is outside the repository"},
		{name: "a path climbing out midway", file: "docs/../../CODEOWNERS", wantErr: "is outside the repository"},
		{name: "an absolute path", file: filepath.Join(outside, "CODEOWNERS"), wantErr: "is outside the repository"},
		{name: "a symlinked folder", file: "linked/sub/CODEOWNERS", symlink: "linked", wantErr: "through a symbolic link"},
		{name: "a symlinked file", file: "OWNERS", symlink: "OWNERS", wantErr: "through a symbolic link"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := codeownersRepo(t, "git@github.com:acme/shop.git", shopTree())
			if testCase.symlink != "" {
				require.NoError(t, os.Symlink(outside, filepath.Join(dir, testCase.symlink)))
			}
			run := runCodeownersCmd(t, dir, codeownersAPIOptions{owned: true}, "--write", "--yes", "--file", testCase.file)
			require.Error(t, run.err)
			assert.Contains(t, run.err.Error(), testCase.wantErr)
			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing is written outside")
		})
	}
}

func TestCodeownersOwnerErrors(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		opts    codeownersAPIOptions
		wantErr string
	}{
		{name: "a forbidden map is not hidden by the fallback", opts: codeownersAPIOptions{owned: true, mapStatus: http.StatusForbidden}, wantErr: "HTTP 403"},
		{name: "a failing map", opts: codeownersAPIOptions{owned: true, mapStatus: http.StatusInternalServerError}, wantErr: "HTTP 500"},
		{name: "a failing element call", opts: codeownersAPIOptions{owned: true, noMap: true, elementStatus: http.StatusInternalServerError}, wantErr: "boom (HTTP 500)"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := codeownersRepo(t, "git@github.com:acme/shop.git", shopTree())
			run := runCodeownersCmd(t, dir, testCase.opts)
			require.Error(t, run.err)
			assert.Contains(t, run.err.Error(), testCase.wantErr)
		})
	}
}
