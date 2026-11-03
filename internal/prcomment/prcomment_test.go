package prcomment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetect(
	t *testing.T,
) {
	event := filepath.Join(t.TempDir(), "event.json")
	require.NoError(t, os.WriteFile(event, []byte(`{"pull_request":{"number":42}}`), 0o644))

	testCases := []struct {
		name    string
		env     map[string]string
		want    *Target
		wantErr error
	}{
		{
			name: "github from the ref",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/pull/7/merge", "GITHUB_REPOSITORY": "acme/api", "GITHUB_TOKEN": "t"},
			want: &Target{Provider: "github", APIURL: "https://api.github.com", Repo: "acme/api", Number: 7, Token: "t"},
		},
		{
			name: "github from the event payload",
			env:  map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_EVENT_PATH": event, "GITHUB_REPOSITORY": "acme/api", "GH_TOKEN": "t"},
			want: &Target{Provider: "github", APIURL: "https://api.github.com", Repo: "acme/api", Number: 42, Token: "t"},
		},
		{name: "github push", env: map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/heads/main", "GITHUB_TOKEN": "t"}, wantErr: ErrNoTarget},
		{name: "github without token", env: map[string]string{"GITHUB_ACTIONS": "true", "GITHUB_REF": "refs/pull/7/merge"}, wantErr: ErrNoToken},
		{
			name: "gitlab merge request",
			env:  map[string]string{"GITLAB_CI": "true", "CI_MERGE_REQUEST_IID": "3", "CI_PROJECT_ID": "99", "CI_API_V4_URL": "https://gitlab.com/api/v4", "GITLAB_TOKEN": "g"},
			want: &Target{Provider: "gitlab", APIURL: "https://gitlab.com/api/v4", Repo: "99", Number: 3, Token: "g"},
		},
		{name: "laptop", env: map[string]string{}, wantErr: ErrNoTarget},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := Detect(func(k string) string { return testCase.env[k] })
			if testCase.wantErr != nil {
				assert.ErrorIs(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

// fakeGitHub keeps issue comments in memory.
func fakeGitHub(t *testing.T) (*httptest.Server, *[]map[string]any) {
	var mu sync.Mutex
	comments := []map[string]any{{"id": float64(1), "body": "LGTM"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, "Bearer t", r.Header.Get("Authorization"))
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			_ = json.NewEncoder(w).Encode(comments)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			comments = append(comments, map[string]any{"id": float64(len(comments) + 1), "body": in["body"], "user": map[string]any{"login": actionsBot}})
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/api/issues/comments/"):
			for _, c := range comments {
				if "/repos/acme/api/issues/comments/"+jsonID(c["id"]) == r.URL.Path {
					c["body"] = in["body"]
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return server, &comments
}

func jsonID(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestUpsertEditsInPlace(
	t *testing.T,
) {
	server, comments := fakeGitHub(t)
	defer server.Close()
	target := &Target{Provider: "github", APIURL: server.URL, Repo: "acme/api", Number: 7, Token: "t"}

	require.NoError(t, target.Upsert(context.Background(), "first run"))
	require.Len(t, *comments, 2)
	require.NoError(t, target.Upsert(context.Background(), "second run"))
	require.Len(t, *comments, 2, "the second run edits the comment instead of adding one")
	assert.Equal(t, Marker+"\nsecond run", (*comments)[1]["body"])
	assert.Equal(t, "LGTM", (*comments)[0]["body"], "other comments are untouched")
}
