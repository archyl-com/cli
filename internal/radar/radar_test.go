package radar

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func dep(name, ring, note string) api.CheckedDependency {
	d := api.CheckedDependency{Name: name, Version: "1.0.0", FilePath: "go.mod"}
	if ring != "-" {
		d.Technology = &api.RadarTechnology{Name: name, RadarRing: ring, RadarNote: note}
	}
	return d
}

func TestIsManifest(
	t *testing.T,
) {
	testCases := []struct {
		path string
		want bool
		skip bool
	}{
		{path: "go.mod", want: true},
		{path: "web/package.json", want: true},
		{path: "src/App/App.csproj", want: true},
		{path: "web/node_modules/left-pad/package.json", want: true, skip: true},
		{path: "main.go", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			assert.Equal(t, testCase.want, IsManifest(testCase.path))
			assert.Equal(t, testCase.skip, Skip(testCase.path))
		})
	}
}

func TestViolations(
	t *testing.T,
) {
	deps := []api.CheckedDependency{
		dep("github.com/sirupsen/logrus", "hold", "Use slog"),
		dep("github.com/new/thing", "assess", ""),
		dep("github.com/gofiber/fiber", "adopt", ""),
		dep("github.com/unknown/x", "-", ""),
	}
	content := "module x\n\nrequire (\n\tgithub.com/sirupsen/logrus v1.9.3\n)\n"
	v := Violations(deps, func(file, name string) int { return DeclarationLine(content, name) })
	require.Len(t, v, 2)
	assert.Equal(t, "high", v[0].Severity)
	assert.Equal(t, "radar-hold", v[0].RuleID)
	assert.Equal(t, "Use slog", v[0].Suggestion)
	assert.Equal(t, 4, v[0].LineStart)
	assert.Equal(t, "medium", v[1].Severity)
	assert.Equal(t, 0, v[1].LineStart)
}

func TestDeclarationLine(
	t *testing.T,
) {
	assert.Equal(t, 3, DeclarationLine("{\n \"name\": \"x\",\n \"lodash\": \"4\"\n}", "lodash"))
	assert.Equal(t, 2, DeclarationLine("a\n<artifactId>log4j-core</artifactId>", "org.apache.logging.log4j:log4j-core"))
	assert.Equal(t, 0, DeclarationLine("nothing", "missing"))
}

func TestNewHolds(
	t *testing.T,
) {
	before := []api.CheckedDependency{dep("logrus", "hold", "")}
	after := []api.CheckedDependency{dep("logrus", "hold", ""), dep("zap", "hold", ""), dep("slog", "adopt", "")}
	got := NewHolds(before, after)
	require.Len(t, got, 1)
	assert.Equal(t, "zap", got[0].Name, "a hold dependency already there is not the agent's doing")
}
