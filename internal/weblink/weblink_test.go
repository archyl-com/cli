package weblink

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

func TestAppURL(
	t *testing.T,
) {
	testCases := []struct {
		name, host, explicit, want string
	}{
		{name: "cloud", host: "https://api.archyl.com", want: "https://app.archyl.com"},
		{name: "explicit wins", host: "http://localhost:8081", explicit: "http://app.archyl.local:8080/", want: "http://app.archyl.local:8080"},
		{name: "single-domain self-hosted", host: "https://archyl.example.com/", want: "https://archyl.example.com"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, AppURL(testCase.host, testCase.explicit))
		})
	}
}

func TestElement(
	t *testing.T,
) {
	g := graph.New(&api.Model{Systems: []api.System{{ID: "s", Name: "S", Containers: []api.Container{
		{ID: "c", Name: "C", Components: []api.Component{{ID: "k", Name: "K"}}},
	}}}})
	assert.Equal(t,
		"https://app.archyl.com/project/p?containerId=c&select=k&selectType=component&systemId=s",
		Element("https://app.archyl.com", "p", g.Node("k")))
	assert.Equal(t, "https://app.archyl.com/project/p/decisions?expandId=a", ADR("https://app.archyl.com", "p", "a"))
}
