package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const smokeProject = "11111111-2222-3333-4444-555555555555"

// TestSmoke builds the real binary and drives it against a fake Archyl API, the
// way CI and coding agents run it: an API key, a project, no terminal.
func TestSmoke(
	t *testing.T,
) {
	if testing.Short() {
		t.Skip("builds the binary")
	}

	bin := buildBinary(t)
	api := fakeAPI(t)
	repoDir := gitFixture(t)

	testCases := []struct {
		name     string
		args     []string
		wantCode int
		check    func(t *testing.T, stdout string)
	}{
		{
			name: "version",
			args: []string{"--version"},
			check: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "archyl")
			},
		},
		{
			name: "where prints markdown for agents",
			args: []string{"where", "api/handler.go"},
			check: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "HTTP Handlers")
			},
		},
		{
			name:     "check reports violations as json and fails on high",
			args:     []string{"check", "--base", "HEAD~1", "--format", "json"},
			wantCode: 1,
			check: func(t *testing.T, stdout string) {
				var out map[string]any
				require.NoError(t, json.Unmarshal([]byte(stdout), &out), stdout)
				assert.Contains(t, stdout, "No direct database access")
			},
		},
		{
			name: "check passes when the threshold is never",
			args: []string{"check", "--base", "HEAD~1", "--format", "json", "--fail-on", "never"},
		},
		{
			name: "diagram centres on a named element",
			args: []string{"diagram", "Orders DB", "--format", "mermaid"},
			check: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "Orders DB")
				assert.Contains(t, stdout, "Shop API")
				assert.Contains(t, stdout, "touched", "the element in the middle is accented")
			},
		},
		{
			name: "diagram draws a system's containers as mermaid",
			args: []string{"diagram", "Shop", "--inside", "--format", "mermaid"},
			check: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "flowchart")
				assert.Contains(t, stdout, "Shop API")
				assert.Contains(t, stdout, "Orders DB")
				assert.Contains(t, stdout, "reads from")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cmd := exec.Command(bin, testCase.args...)
			cmd.Dir = repoDir
			cmd.Env = append(isolatedEnv(t),
				"ARCHYL_HOST="+api.URL,
				"ARCHYL_API_KEY=arch_smoke",
				"ARCHYL_PROJECT_ID="+smokeProject,
			)
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			err := cmd.Run()
			code := 0
			var exitErr *exec.ExitError
			if err != nil {
				require.ErrorAs(t, err, &exitErr, stderr.String())
				code = exitErr.ExitCode()
			}

			assert.Equal(t, testCase.wantCode, code, "stderr: %s", stderr.String())
			if testCase.check != nil {
				testCase.check(t, stdout.String())
			}
		})
	}
}

func buildBinary(
	t *testing.T,
) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "archyl")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

// isolatedEnv keeps the user's configuration, keychain, git identity and
// update checks out of the run.
func isolatedEnv(
	t *testing.T,
) []string {
	t.Helper()
	home := t.TempDir()
	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"PATH=" + os.Getenv("PATH"),
		"ARCHYL_CONFIG_DIR=" + filepath.Join(home, "config"),
		"ARCHYL_NO_KEYRING=1",
		"ARCHYL_NO_UPDATE_NOTIFIER=1",
		"ARCHYL_NO_CACHE=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"NO_COLOR=1",
	}
	for _, key := range []string{"SYSTEMROOT", "TEMP", "TMP", "LOCALAPPDATA", "APPDATA"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func gitFixture(
	t *testing.T,
) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=smoke", "GIT_AUTHOR_EMAIL=smoke@example.com",
			"GIT_COMMITTER_NAME=smoke", "GIT_COMMITTER_EMAIL=smoke@example.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	write := func(name, content string) {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	git("init", "-q", "-b", "main")
	write("README.md", "smoke\n")
	git("add", ".")
	git("commit", "-q", "-m", "initial")
	write("api/handler.go", "package api\n\nimport \"database/sql\"\n")
	git("add", ".")
	git("commit", "-q", "-m", "handler")
	return dir
}

func fakeAPI(
	t *testing.T,
) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	prefix := "/api/v1/projects/" + smokeProject

	mux.HandleFunc("GET /api/v1/users/me", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, `{"id":"u1","email":"ada@example.com","firstName":"Ada"}`)
	})
	mux.HandleFunc("GET "+prefix, func(w http.ResponseWriter, _ *http.Request) {
		reply(w, `{"id":"`+smokeProject+`","name":"Smoke"}`)
	})
	mux.HandleFunc("GET "+prefix+"/c4", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, `{"systems":[{"id":"s1","name":"Shop","containers":[`+
			`{"id":"c1","name":"Shop API","containerType":"api"},`+
			`{"id":"c2","name":"Orders DB","containerType":"database"}]}],`+
			`"relationships":[{"id":"r1","sourceId":"c1","targetId":"c2","description":"reads from"}]}`)
	})
	mux.HandleFunc("POST "+prefix+"/harness/locate", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, `{"files":[{"path":"api/handler.go","elements":[{"id":"e1","name":"HTTP Handlers","type":"component"}]}],"unmatched":[],"impact":{}}`)
	})
	mux.HandleFunc("POST "+prefix+"/conformance/check", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChangedFiles []struct {
				Path string `json:"path"`
			} `json:"changedFiles"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.ChangedFiles) == 0 {
			reply(w, `{"summary":{},"violations":[],"rules":[]}`)
			return
		}
		reply(w, `{"check":{"id":"c1"},"summary":{"total":1,"high":1},`+
			`"violations":[{"id":"v1","ruleId":"r1","severity":"high","title":"No direct database access",`+
			`"filePath":"api/handler.go","lineStart":3,"lineEnd":3}],`+
			`"rules":[{"id":"r1","name":"No direct database access"}]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("unhandled %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
