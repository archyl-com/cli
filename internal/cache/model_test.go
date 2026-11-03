package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func snapshot(name string) ModelSnapshot {
	return ModelSnapshot{
		Project: api.Project{ID: "p1", Name: name},
		Model:   api.Model{Systems: []api.System{{ID: "s", Name: "Billing"}}},
	}
}

// TestModelScopes checks that a snapshot is only ever read back under the
// exact scope and project it was saved with.
func TestModelScopes(
	t *testing.T,
) {
	base := Scope{Profile: "work", Host: "https://api.archyl.com", OrgID: "org-a"}
	testCases := []struct {
		name    string
		scope   Scope
		project string
		hit     bool
	}{
		{name: "same scope", scope: base, project: "p1", hit: true},
		{name: "other project", scope: base, project: "p2"},
		{name: "other profile", scope: Scope{Profile: "perso", Host: base.Host, OrgID: base.OrgID}, project: "p1"},
		{name: "other host", scope: Scope{Profile: base.Profile, Host: "http://localhost:8080", OrgID: base.OrgID}, project: "p1"},
		{name: "other organization", scope: Scope{Profile: base.Profile, Host: base.Host, OrgID: "org-b"}, project: "p1"},
		{name: "API key override", scope: Scope{Profile: base.Profile, Host: base.Host, OrgID: base.OrgID, Identity: "key:x"}, project: "p1"},
	}

	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	require.NoError(t, SaveModel(base, "p1", snapshot("Billing")))

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			entry, err := LoadModel(testCase.scope, testCase.project)
			require.NoError(t, err)
			if !testCase.hit {
				assert.Nil(t, entry)
				return
			}
			require.NotNil(t, entry)
			assert.Equal(t, "Billing", entry.Value.Project.Name)
			assert.Equal(t, "Billing", entry.Value.Model.Systems[0].Name)
		})
	}
}

func TestModelFilesArePrivate(
	t *testing.T,
) {
	dir := t.TempDir()
	t.Setenv("ARCHYL_CACHE_DIR", dir)
	scope := Scope{Profile: "work", Host: "h"}
	require.NoError(t, SaveModel(scope, "p1", snapshot("Billing")))

	files, err := filepath.Glob(filepath.Join(dir, "models", modelFormat, "*", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	info, err := os.Stat(files[0])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.NotContains(t, files[0], "work", "names are hashed")
	assert.NotContains(t, files[0], "p1", "names are hashed")
}

func TestModelInvalidation(
	t *testing.T,
) {
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	work := Scope{Profile: "work", Host: "h"}
	perso := Scope{Profile: "perso", Host: "h"}
	for _, s := range []Scope{work, perso} {
		require.NoError(t, SaveModel(s, "p1", snapshot("Billing")))
		require.NoError(t, SaveModel(s, "p2", snapshot("Shop")))
	}

	require.NoError(t, InvalidateModel(work, "p1"))
	require.NoError(t, InvalidateModel(work, "p1"), "invalidating a miss is fine")
	assertHit(t, work, "p1", false)
	assertHit(t, work, "p2", true)

	require.NoError(t, ClearProfile("work"))
	assertHit(t, work, "p2", false)
	assertHit(t, perso, "p1", true)

	require.NoError(t, ClearModels())
	assertHit(t, perso, "p1", false)
}

func assertHit(t *testing.T, s Scope, projectID string, hit bool) {
	t.Helper()
	entry, err := LoadModel(s, projectID)
	require.NoError(t, err)
	assert.Equal(t, hit, entry != nil, "%s/%s", s.Profile, projectID)
}

func TestClaimRefresh(
	t *testing.T,
) {
	t.Setenv("ARCHYL_CACHE_DIR", t.TempDir())
	s := Scope{Profile: "work", Host: "h"}

	assert.True(t, ClaimRefresh(s, "p1", time.Minute))
	assert.False(t, ClaimRefresh(s, "p1", time.Minute), "one refresh per window")
	assert.True(t, ClaimRefresh(s, "p2", time.Minute), "per project")
	assert.True(t, ClaimRefresh(s, "p1", 0), "the window is over")
}

func TestDisabled(
	t *testing.T,
) {
	testCases := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "0", want: false},
		{value: "false", want: false},
		{value: "1", want: true},
		{value: "true", want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.value, func(t *testing.T) {
			t.Setenv("ARCHYL_NO_CACHE", testCase.value)
			assert.Equal(t, testCase.want, Disabled())
		})
	}
}
