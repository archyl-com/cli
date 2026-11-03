package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersions(
	t *testing.T,
) {
	testCases := []struct {
		name      string
		pages     map[string]string
		want      []int
		wantPages []string
	}{
		{
			name: "one page, sorted oldest first",
			pages: map[string]string{
				"1": `{"data":[{"number":3,"userName":"Ada"},{"number":2},{"number":1}],"total":3,"page":1,"pages":1}`,
			},
			want:      []int{1, 2, 3},
			wantPages: []string{"page=1&pageSize=100"},
		},
		{
			name: "every page",
			pages: map[string]string{
				"1": `{"data":[{"number":4},{"number":3}],"pages":2}`,
				"2": `{"data":[{"number":2},{"number":1}],"pages":2}`,
			},
			want:      []int{1, 2, 3, 4},
			wantPages: []string{"page=1&pageSize=100", "page=2&pageSize=100"},
		},
		{
			name:      "no versions",
			pages:     map[string]string{"1": `{"data":[],"pages":0}`},
			wantPages: []string{"page=1&pageSize=100"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var queries []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/projects/p1/versions", r.URL.Path)
				queries = append(queries, r.URL.RawQuery)
				_, _ = io.WriteString(w, testCase.pages[r.URL.Query().Get("page")])
			}))
			defer server.Close()

			versions, err := NewClient(server.URL, "", nil).Versions(context.Background(), "p1")

			require.NoError(t, err)
			var got []int
			for _, v := range versions {
				got = append(got, v.Number)
			}
			assert.Equal(t, testCase.want, got)
			assert.Equal(t, testCase.wantPages, queries)
		})
	}
}

func TestVersionModel(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		snapshot string
		wantErr  string
		check    func(t *testing.T, m *Model)
	}{
		{
			name: "nests the flat snapshot",
			snapshot: `{"systems":[{"id":"s","name":"Shop","type":"software_system"},{"id":"x","name":"Stripe","isExternal":true}],` +
				`"containers":[{"id":"api","systemId":"s","name":"API","containerType":"api"},{"id":"lost","systemId":"gone","name":"Lost"}],` +
				`"components":[{"id":"co","containerId":"api","name":"Checkout","componentType":"service"}],` +
				`"codeElements":[{"id":"fn","componentId":"co","name":"Charge","codeType":"function"}],` +
				`"relationships":[{"id":"r","sourceId":"co","targetId":"x","type":"calls","description":"charges"}],"overlays":[]}`,
			check: func(t *testing.T, m *Model) {
				require.Len(t, m.Systems, 2)
				assert.True(t, m.Systems[1].IsExternal)
				require.Len(t, m.Systems[0].Containers, 1, "a container without its system is left out")
				ctr := m.Systems[0].Containers[0]
				assert.Equal(t, "api", ctr.ContainerType)
				require.Len(t, ctr.Components, 1)
				require.Len(t, ctr.Components[0].CodeElements, 1)
				assert.Equal(t, "Charge", ctr.Components[0].CodeElements[0].Name)
				require.Len(t, m.Relationships, 1)
				assert.Equal(t, "charges", m.Relationships[0].Description)
			},
		},
		{
			name:     "an empty model",
			snapshot: `{"systems":[],"containers":null}`,
			check: func(t *testing.T, m *Model) {
				assert.Empty(t, m.Systems)
			},
		},
		{
			name:    "no snapshot",
			wantErr: "came without its snapshot",
		},
		{
			name:     "a broken snapshot",
			snapshot: `{"systems":{}}`,
			wantErr:  "read the snapshot of version 7",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			v := Version{Number: 7, Snapshot: []byte(testCase.snapshot)}

			m, err := v.Model()

			if testCase.wantErr != "" {
				require.ErrorContains(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			testCase.check(t, m)
		})
	}
}

func TestVersion(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/projects/p1/versions/12", r.URL.Path)
		_, _ = io.WriteString(w, `{"number":12,"userName":"Ada","description":"Split billing",`+
			`"createdAt":"2026-10-01T09:00:00Z","snapshotData":{"systems":[{"id":"s","name":"Shop"}]}}`)
	}))
	defer server.Close()

	v, err := NewClient(server.URL, "", nil).Version(context.Background(), "p1", 12)

	require.NoError(t, err)
	assert.Equal(t, "Ada", v.Author)
	assert.Equal(t, "Split billing", v.Description)
	m, err := v.Model()
	require.NoError(t, err)
	assert.Equal(t, "Shop", m.Systems[0].Name)
}

func TestProjectModel(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/projects/p1/c4", r.URL.Path)
		_, _ = io.WriteString(w, `{"systems":[{"id":"s","name":"Shop","technologies":[{"name":"Go"}]}],"relationships":[`+
			`{"id":"own","projectId":"p1","sourceId":"s","targetId":"s"},`+
			`{"id":"other","projectId":"p2","sourceId":"s","targetId":"s"},`+
			`{"id":"unscoped","sourceId":"s","targetId":"s"}]}`)
	}))
	defer server.Close()

	m, err := NewClient(server.URL, "", nil).ProjectModel(context.Background(), "p1")

	require.NoError(t, err)
	assert.Equal(t, "Go", m.Systems[0].Technologies[0].Name)
	var ids []string
	for _, r := range m.Relationships {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"own", "unscoped"}, ids, "other projects' relationships are left out")
}
