package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeCheckRefusesAnswersWithoutResult(t *testing.T) {
	for _, raw := range []string{"", "{}", `{"check":{"id":"x"}}`} {
		_, err := decodeCheck(raw)
		assert.Error(t, err, raw)
	}
	result, err := decodeCheck(`{"summary":{"total":0},"violations":[]}`)
	require.NoError(t, err)
	assert.Empty(t, result.Violations)
}

func TestMergeCountsAModelWideViolationOnce(t *testing.T) {
	result := &ConformanceCheckResult{}
	result.Merge(&ConformanceCheckResult{Violations: []Violation{{ID: "1", RuleID: "r", Severity: "critical"}}})
	result.Merge(&ConformanceCheckResult{Violations: []Violation{{ID: "2", RuleID: "r", Severity: "critical"}, {ID: "3", RuleID: "s", Severity: "high", FilePath: "a.go"}}})
	assert.Len(t, result.Violations, 2)
	assert.Equal(t, 1, result.Summary.Critical)
	assert.Equal(t, 1, result.Summary.High)
	assert.Equal(t, 2, result.Summary.Total)
}
