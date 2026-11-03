package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseHosted(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		remote   string
		provider string
		want     Hosted
		wantErr  error
	}{
		{
			name:   "github over ssh keeps the case",
			remote: "git@github.com:Acme/Payments-API.git",
			want: Hosted{Provider: "github", Host: "github.com", FullName: "Acme/Payments-API", Name: "Payments-API",
				URL: "https://github.com/Acme/Payments-API", CloneURL: "https://github.com/Acme/Payments-API.git"},
		},
		{
			name:   "gitlab subgroups stay in the owner",
			remote: "https://gitlab.com/acme/platform/api.git",
			want: Hosted{Provider: "gitlab", Host: "gitlab.com", FullName: "acme/platform/api", Name: "api",
				URL: "https://gitlab.com/acme/platform/api", CloneURL: "https://gitlab.com/acme/platform/api.git"},
		},
		{
			name:   "bitbucket",
			remote: "https://user@bitbucket.org/acme/shop",
			want: Hosted{Provider: "bitbucket", Host: "bitbucket.org", FullName: "acme/shop", Name: "shop",
				URL: "https://bitbucket.org/acme/shop", CloneURL: "https://bitbucket.org/acme/shop.git"},
		},
		{
			name:   "azure devops over https",
			remote: "https://acme@dev.azure.com/acme/Shop/_git/checkout",
			want: Hosted{Provider: "azure_devops", Host: "dev.azure.com", FullName: "acme/Shop/checkout", Name: "checkout",
				URL: "https://dev.azure.com/acme/Shop/_git/checkout", CloneURL: "https://dev.azure.com/acme/Shop/_git/checkout"},
		},
		{
			name:   "azure devops over ssh",
			remote: "git@ssh.dev.azure.com:v3/acme/Shop/checkout",
			want: Hosted{Provider: "azure_devops", Host: "ssh.dev.azure.com", FullName: "acme/Shop/checkout", Name: "checkout",
				URL: "https://dev.azure.com/acme/Shop/_git/checkout", CloneURL: "https://dev.azure.com/acme/Shop/_git/checkout"},
		},
		{
			name:   "legacy visualstudio.com over https: the organization is the subdomain",
			remote: "https://acme.visualstudio.com/Shop/_git/checkout",
			want: Hosted{Provider: "azure_devops", Host: "acme.visualstudio.com", FullName: "acme/Shop/checkout", Name: "checkout",
				URL: "https://dev.azure.com/acme/Shop/_git/checkout", CloneURL: "https://dev.azure.com/acme/Shop/_git/checkout"},
		},
		{
			name:   "legacy visualstudio.com with DefaultCollection",
			remote: "https://acme.visualstudio.com/DefaultCollection/Shop/_git/checkout",
			want: Hosted{Provider: "azure_devops", Host: "acme.visualstudio.com", FullName: "acme/Shop/checkout", Name: "checkout",
				URL: "https://dev.azure.com/acme/Shop/_git/checkout", CloneURL: "https://dev.azure.com/acme/Shop/_git/checkout"},
		},
		{
			name:   "legacy visualstudio.com over ssh",
			remote: "acme@vs-ssh.visualstudio.com:v3/acme/Shop/checkout",
			want: Hosted{Provider: "azure_devops", Host: "vs-ssh.visualstudio.com", FullName: "acme/Shop/checkout", Name: "checkout",
				URL: "https://dev.azure.com/acme/Shop/_git/checkout", CloneURL: "https://dev.azure.com/acme/Shop/_git/checkout"},
		},
		{
			name:   "azure devops repository named after its project",
			remote: "https://dev.azure.com/acme/_git/Shop",
			want: Hosted{Provider: "azure_devops", Host: "dev.azure.com", FullName: "acme/Shop/Shop", Name: "Shop",
				URL: "https://dev.azure.com/acme/Shop/_git/Shop", CloneURL: "https://dev.azure.com/acme/Shop/_git/Shop"},
		},
		{
			name:     "self-hosted with an explicit provider",
			remote:   "git@git.acme.internal:platform/api.git",
			provider: "gitlab_private",
			want: Hosted{Provider: "gitlab_private", Host: "git.acme.internal", FullName: "platform/api", Name: "api",
				URL: "https://git.acme.internal/platform/api", CloneURL: "https://git.acme.internal/platform/api.git"},
		},
		{
			name:    "no remote",
			remote:  "",
			wantErr: ErrNoRemote,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := ParseHosted(testCase.remote, testCase.provider)
			if testCase.wantErr != nil {
				assert.ErrorIs(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestParseHostedUnknownHost(
	t *testing.T,
) {
	_, err := ParseHosted("git@git.acme.internal:platform/api.git", "")
	var unsupported *UnsupportedHostError
	require.ErrorAs(t, err, &unsupported)
	assert.Equal(t, "git.acme.internal", unsupported.Host)
	assert.Contains(t, err.Error(), "--provider")
}
