package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleases(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		filter    ReleaseFilter
		wantQuery string
	}{
		{
			name:      "project wide",
			filter:    ReleaseFilter{Limit: 5},
			wantQuery: "pageSize=5",
		},
		{
			name:      "one container",
			filter:    ReleaseFilter{ContainerID: "c1", Limit: 3},
			wantQuery: "containerId=c1&pageSize=3",
		},
		{
			name:      "one system",
			filter:    ReleaseFilter{SystemID: "s1"},
			wantQuery: "systemId=s1",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var gotPath, gotQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
				_, _ = io.WriteString(w, `{"data":[{"id":"r1","version":"v1.2.0","status":"deployed",`+
					`"releasedAt":"2026-10-28T10:00:00Z","createdAt":"2026-10-27T10:00:00Z",`+
					`"environment":{"name":"production","isProduction":true}}],"total":1}`)
			}))
			defer server.Close()

			releases, err := NewClient(server.URL, "", nil).Releases(context.Background(), "p1", testCase.filter)

			require.NoError(t, err)
			assert.Equal(t, "/api/v1/projects/p1/releases", gotPath)
			assert.Equal(t, testCase.wantQuery, gotQuery)
			require.Len(t, releases, 1)
			assert.Equal(t, "v1.2.0", releases[0].Version)
			assert.Equal(t, "production", releases[0].Environment.Name)
			assert.Equal(t, time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC), releases[0].When())
		})
	}
}

func TestReleaseWhen(
	t *testing.T,
) {
	created := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	released := created.Add(time.Hour)
	testCases := []struct {
		name    string
		release ReleaseDetail
		want    time.Time
	}{
		{
			name:    "released",
			release: ReleaseDetail{CreatedAt: created, ReleasedAt: &released},
			want:    released,
		},
		{
			name:    "not out yet",
			release: ReleaseDetail{CreatedAt: created},
			want:    created,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.release.When())
		})
	}
}

func TestElementDocs(
	t *testing.T,
) {
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = io.WriteString(w, `{"data":[{"id":"d1","title":"Orders runbook","content":"…"}],"total":1}`)
	}))
	defer server.Close()

	docs, err := NewClient(server.URL, "", nil).ElementDocs(context.Background(), "container", "c1")

	require.NoError(t, err)
	assert.Equal(t, "/api/v1/docs/", gotPath)
	assert.Equal(t, "elementId=c1&elementType=container", gotQuery)
	assert.Equal(t, []Doc{{ID: "d1", Title: "Orders runbook"}}, docs)
}
