package session

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/archyl-com/cli/internal/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoredCredentialStaysOnItsHost(t *testing.T) {
	testCases := []struct {
		name     string
		host     string
		apiKey   string
		wantSent bool
	}{
		{name: "no override", wantSent: true},
		{name: "same host", host: "https://api.archyl.com/", wantSent: true},
		{name: "other host", host: "https://evil.example"},
		{name: "other host with explicit key", host: "https://self.example", apiKey: "arch_ci", wantSent: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("ARCHYL_NO_KEYRING", "1")
			t.Setenv("ARCHYL_PROFILE", "")
			t.Setenv("ARCHYL_API_URL", "")
			t.Setenv("ARCHYL_HOST", testCase.host)
			t.Setenv("ARCHYL_API_KEY", testCase.apiKey)
			require.NoError(t, credentials.NewStore(dir).Set("default", &credentials.Credential{AccessToken: "stored", RefreshToken: "refresh"}))

			s, err := Resolve(dir, Options{})
			require.NoError(t, err)
			req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
			err = s.Apply(context.Background(), req)

			if testCase.wantSent {
				require.NoError(t, err)
				return
			}
			var mismatch *HostMismatchError
			require.True(t, errors.As(err, &mismatch))
			assert.Empty(t, req.Header.Get("Authorization"))
		})
	}
}
