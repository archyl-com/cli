package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestExpired(
	t *testing.T,
) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	testCases := []struct {
		name string
		cred Credential
		want bool
	}{
		{name: "api key never expires", cred: Credential{APIKey: "k", ExpiresAt: now.Add(-time.Hour)}, want: false},
		{name: "no expiry", cred: Credential{AccessToken: "a"}, want: false},
		{name: "valid for an hour", cred: Credential{AccessToken: "a", ExpiresAt: now.Add(time.Hour)}, want: false},
		{name: "within the minute of slack", cred: Credential{AccessToken: "a", ExpiresAt: now.Add(30 * time.Second)}, want: true},
		{name: "past", cred: Credential{AccessToken: "a", ExpiresAt: now.Add(-time.Second)}, want: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, testCase.cred.Expired(now))
		})
	}
}

func TestNewStore(
	t *testing.T,
) {
	dir := t.TempDir()

	t.Setenv("ARCHYL_NO_KEYRING", "1")
	file, ok := NewStore(dir).(*FileStore)
	require.True(t, ok, "ARCHYL_NO_KEYRING selects the file store")
	assert.Equal(t, filepath.Join(dir, "credentials.json"), file.Path)

	t.Setenv("ARCHYL_NO_KEYRING", "")
	ks, ok := NewStore(dir).(*keyringStore)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "credentials.json"), ks.fallback.Path)
}

func TestFileStore(
	t *testing.T,
) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), "nested", "credentials.json")}

	_, err := store.Get("default")
	assert.ErrorIs(t, err, ErrNotFound, "a missing file holds nothing")
	require.NoError(t, store.Delete("default"), "deleting from a missing file is fine")
	_, err = os.Stat(store.Path)
	assert.ErrorIs(t, err, os.ErrNotExist, "a no-op delete does not create the file")

	expires := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.Set("default", &Credential{APIKey: "key-1"}))
	require.NoError(t, store.Set("staging", &Credential{AccessToken: "a", RefreshToken: "r", ClientID: "c", ExpiresAt: expires}))

	got, err := store.Get("default")
	require.NoError(t, err)
	assert.Equal(t, "key-1", got.APIKey)
	got, err = store.Get("staging")
	require.NoError(t, err)
	assert.Equal(t, Credential{AccessToken: "a", RefreshToken: "r", ClientID: "c", ExpiresAt: expires}, *got)

	require.NoError(t, store.Set("default", &Credential{APIKey: "key-2"}))
	got, err = store.Get("default")
	require.NoError(t, err)
	assert.Equal(t, "key-2", got.APIKey, "set overwrites")

	require.NoError(t, store.Delete("default"))
	_, err = store.Get("default")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = store.Get("staging")
	assert.NoError(t, err, "other profiles survive a delete")

	entries, err := os.ReadDir(filepath.Dir(store.Path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp files are left behind")
	assert.Equal(t, "credentials.json", entries[0].Name())
}

func TestFileStorePermissions(
	t *testing.T,
) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "archyl")
	store := &FileStore{Path: filepath.Join(dir, "credentials.json")}
	require.NoError(t, store.Set("default", &Credential{APIKey: "secret"}))

	info, err := os.Stat(store.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	info, err = os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	// A file loosened by hand is tightened on the next write.
	require.NoError(t, os.Chmod(store.Path, 0o644))
	require.NoError(t, store.Set("default", &Credential{APIKey: "secret-2"}))
	info, err = os.Stat(store.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestFileStoreCorrupt(
	t *testing.T,
) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	require.NoError(t, os.WriteFile(store.Path, []byte("{not json"), 0o600))

	_, err := store.Get("default")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
	assert.Contains(t, err.Error(), "decode")

	assert.Error(t, store.Set("default", &Credential{APIKey: "k"}), "a corrupt file is not silently overwritten")
	assert.Error(t, store.Delete("default"))
	raw, err := os.ReadFile(store.Path)
	require.NoError(t, err)
	assert.Equal(t, "{not json", string(raw))
}

func TestFileStoreUnreadable(
	t *testing.T,
) {
	// A directory where the file should be: ReadFile fails with something
	// other than not-exist.
	path := t.TempDir()
	store := &FileStore{Path: path}
	_, err := store.Get("default")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestFileStoreConcurrent(
	t *testing.T,
) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), "credentials.json")}
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	var wg sync.WaitGroup
	for _, name := range names {
		wg.Go(func() {
			assert.NoError(t, store.Set(name, &Credential{APIKey: name}))
		})
	}
	wg.Wait()

	for _, name := range names {
		got, err := store.Get(name)
		require.NoError(t, err)
		assert.Equal(t, name, got.APIKey)
	}
}

func TestKeyringStore(
	t *testing.T,
) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("ARCHYL_NO_KEYRING", "")
	store := NewStore(dir)
	file := &FileStore{Path: filepath.Join(dir, "credentials.json")}

	_, err := store.Get("default")
	assert.ErrorIs(t, err, ErrNotFound)

	// A credential left in the file from before is dropped once the keychain
	// holds the profile.
	require.NoError(t, file.Set("default", &Credential{APIKey: "old"}))
	require.NoError(t, store.Set("default", &Credential{APIKey: "new"}))
	_, err = file.Get("default")
	assert.ErrorIs(t, err, ErrNotFound)

	got, err := store.Get("default")
	require.NoError(t, err)
	assert.Equal(t, "new", got.APIKey)
	raw, err := keyring.Get(keyringService, "default")
	require.NoError(t, err)
	assert.JSONEq(t, `{"apiKey":"new"}`, raw)

	require.NoError(t, store.Delete("default"))
	_, err = store.Get("default")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.NoError(t, store.Delete("default"), "deleting twice is fine")
}

func TestKeyringStoreReadsFileFallback(
	t *testing.T,
) {
	keyring.MockInit()
	dir := t.TempDir()
	store := &keyringStore{fallback: &FileStore{Path: filepath.Join(dir, "credentials.json")}}

	require.NoError(t, store.fallback.Set("ci", &Credential{APIKey: "from-file"}))
	got, err := store.Get("ci")
	require.NoError(t, err)
	assert.Equal(t, "from-file", got.APIKey)

	require.NoError(t, store.Delete("ci"))
	_, err = store.fallback.Get("ci")
	assert.ErrorIs(t, err, ErrNotFound, "logout clears the file copy too")
}

func TestKeyringStoreCorrupt(
	t *testing.T,
) {
	keyring.MockInit()
	store := &keyringStore{fallback: &FileStore{Path: filepath.Join(t.TempDir(), "credentials.json")}}
	require.NoError(t, keyring.Set(keyringService, "default", "{not json"))

	_, err := store.Get("default")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode stored credential")
}

func TestKeyringStoreUnavailable(
	t *testing.T,
) {
	keyring.MockInitWithError(errors.New("no keychain"))
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	store := &keyringStore{fallback: &FileStore{Path: filepath.Join(dir, "credentials.json")}}

	require.NoError(t, store.Set("default", &Credential{APIKey: "k"}), "falls back to the file")
	got, err := store.fallback.Get("default")
	require.NoError(t, err)
	assert.Equal(t, "k", got.APIKey)

	got, err = store.Get("default")
	require.NoError(t, err)
	assert.Equal(t, "k", got.APIKey)

	err = store.Delete("default")
	require.Error(t, err, "a keychain failure other than not-found is reported")
	_, fileErr := store.fallback.Get("default")
	assert.ErrorIs(t, fileErr, ErrNotFound, "the file copy is still removed")
}
