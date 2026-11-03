package report

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

var sample = []api.Violation{
	{RuleID: "r1", Severity: "critical", Title: "Domain imports infra", Suggestion: "Use the port", FilePath: "internal/domain/a.go", LineStart: 4},
	{RuleID: "r2", Severity: "medium", Title: "Name, with: punctuation\nsecond line", FilePath: "web/b.ts"},
	{RuleID: "r2", Severity: "low", Title: "Model-wide note"},
}

var names = map[string]string{"r1": "Layering", "r2": "Naming"}

func TestFailOn(
	t *testing.T,
) {
	testCases := []struct {
		threshold string
		want      bool
	}{
		{threshold: "critical", want: true},
		{threshold: "high", want: true},
		{threshold: "never", want: false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.threshold, func(t *testing.T) {
			failOn, err := ParseFailOn(testCase.threshold)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, failOn.Fails(sample))
		})
	}

	failOn, _ := ParseFailOn("high")
	assert.False(t, failOn.Fails(sample[1:]), "medium and low stay under a high threshold")
	_, err := ParseFailOn("blocker")
	assert.Error(t, err)
}

func TestGitHubAnnotations(
	t *testing.T,
) {
	var buf bytes.Buffer
	require.NoError(t, GitHubAnnotations(&buf, sample, names))
	assert.Equal(t,
		"::error file=internal/domain/a.go,line=4,title=Archyl%3A Layering::Domain imports infra — Use the port\n"+
			"::warning file=web/b.ts,title=Archyl%3A Naming::Name, with: punctuation%0Asecond line\n"+
			"::notice title=Archyl%3A Naming::Model-wide note\n",
		buf.String())
}

func TestGitLabCodeQuality(
	t *testing.T,
) {
	var buf bytes.Buffer
	require.NoError(t, GitLabCodeQuality(&buf, sample, names))

	var issues []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &issues))
	require.Len(t, issues, 3)
	assert.Equal(t, "critical", issues[0]["severity"])
	assert.Equal(t, "Layering", issues[0]["check_name"])
	assert.Equal(t, float64(4), issues[0]["location"].(map[string]any)["lines"].(map[string]any)["begin"])
	assert.Equal(t, float64(1), issues[1]["location"].(map[string]any)["lines"].(map[string]any)["begin"], "GitLab needs a line")
	assert.NotEqual(t, issues[1]["fingerprint"], issues[2]["fingerprint"])

	var again bytes.Buffer
	require.NoError(t, GitLabCodeQuality(&again, sample, names))
	assert.Equal(t, buf.String(), again.String(), "fingerprints are stable across runs")
}

func TestSARIF(
	t *testing.T,
) {
	var buf bytes.Buffer
	rules := []api.Rule{{ID: "r1", Name: "Layering"}, {ID: "r2", Name: "Naming"}}
	require.NoError(t, SARIF(&buf, sample, rules, "1.0.0"))

	var log struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Rules []map[string]any `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID    string           `json:"ruleId"`
				Level     string           `json:"level"`
				Locations []map[string]any `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &log))
	assert.Equal(t, "2.1.0", log.Version)
	run := log.Runs[0]
	assert.Len(t, run.Tool.Driver.Rules, 2)
	assert.Equal(t, []string{"error", "warning", "note"}, []string{run.Results[0].Level, run.Results[1].Level, run.Results[2].Level})
	assert.Len(t, run.Results[0].Locations, 1)
	assert.Empty(t, run.Results[2].Locations, "a finding without a file has no location")
}

func TestFailsOnUnknownSeverity(t *testing.T) {
	assert.True(t, FailOn(4).Fails([]api.Violation{{Severity: "blocker"}}))
	assert.False(t, FailOn(0).Fails([]api.Violation{{Severity: "blocker"}}))
}
