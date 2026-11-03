package repo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeRemote(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		remote string
		want   string
	}{
		{name: "scp-like ssh", remote: "git@github.com:Acme/API.git", want: "github.com/acme/api"},
		{name: "https with .git", remote: "https://github.com/acme/api.git", want: "github.com/acme/api"},
		{name: "https without .git", remote: "https://github.com/acme/api", want: "github.com/acme/api"},
		{name: "ssh url with port", remote: "ssh://git@gitlab.example.com:2222/team/api.git", want: "gitlab.example.com/team/api"},
		{name: "https with credentials", remote: "https://user:token@github.com/acme/api/", want: "github.com/acme/api"},
		{name: "empty", remote: "  ", want: ""},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, NormalizeRemote(testCase.remote))
		})
	}
}
