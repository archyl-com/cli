// Package report renders conformance violations in the formats CI systems
// read: GitHub workflow annotations, GitLab Code Quality and SARIF.
package report

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/archyl-com/cli/internal/api"
)

// Severity ranks Archyl severities so a threshold can be applied.
func Severity(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

// FailOn is the lowest severity that fails a check; 0 never fails.
type FailOn int

// ParseFailOn reads critical, high, medium, low or never.
func ParseFailOn(s string) (FailOn, error) {
	if strings.EqualFold(s, "never") {
		return 0, nil
	}
	if rank := Severity(s); rank > 0 {
		return FailOn(rank), nil
	}
	return 0, fmt.Errorf("--fail-on must be critical, high, medium, low or never, not %q", s)
}

// Fails reports whether any violation reaches the threshold.
func (f FailOn) Fails(violations []api.Violation) bool {
	if f == 0 {
		return false
	}
	for _, v := range violations {
		// A severity this version does not know may be the worst one.
		if rank := Severity(v.Severity); rank == 0 || rank >= int(f) {
			return true
		}
	}
	return false
}

// Message is the one-line text of a violation.
func Message(v api.Violation) string {
	msg := v.Title
	if v.Suggestion != "" {
		msg += " — " + v.Suggestion
	}
	return msg
}

// GitHubAnnotations writes workflow commands that GitHub turns into inline
// annotations on the pull request.
func GitHubAnnotations(w io.Writer, violations []api.Violation, ruleNames map[string]string) error {
	for _, v := range violations {
		level := "notice"
		switch Severity(v.Severity) {
		case 4, 3:
			level = "error"
		case 2:
			level = "warning"
		}
		var props []string
		if v.FilePath != "" {
			props = append(props, "file="+escapeProperty(v.FilePath))
			if v.LineStart > 0 {
				props = append(props, fmt.Sprintf("line=%d", v.LineStart))
			}
			if v.LineEnd > v.LineStart {
				props = append(props, fmt.Sprintf("endLine=%d", v.LineEnd))
			}
		}
		title := "Archyl"
		if name := ruleNames[v.RuleID]; name != "" {
			title += ": " + name
		}
		props = append(props, "title="+escapeProperty(title))
		if _, err := fmt.Fprintf(w, "::%s %s::%s\n", level, strings.Join(props, ","), escapeData(Message(v))); err != nil {
			return err
		}
	}
	return nil
}

func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}

// codeQualityIssue is one entry of a GitLab Code Quality report.
type codeQualityIssue struct {
	Description string              `json:"description"`
	CheckName   string              `json:"check_name"`
	Fingerprint string              `json:"fingerprint"`
	Severity    string              `json:"severity"`
	Location    codeQualityLocation `json:"location"`
}

type codeQualityLocation struct {
	Path  string           `json:"path"`
	Lines codeQualityLines `json:"lines"`
}

type codeQualityLines struct {
	Begin int `json:"begin"`
}

// GitLabCodeQuality writes a Code Quality report, which GitLab shows in the
// merge request widget and diff.
func GitLabCodeQuality(w io.Writer, violations []api.Violation, ruleNames map[string]string) error {
	issues := make([]codeQualityIssue, 0, len(violations))
	for _, v := range violations {
		severity := "info"
		switch Severity(v.Severity) {
		case 4:
			severity = "critical"
		case 3:
			severity = "major"
		case 2:
			severity = "minor"
		}
		line := max(v.LineStart, 1)
		issues = append(issues, codeQualityIssue{
			Description: Message(v),
			CheckName:   firstNonEmpty(ruleNames[v.RuleID], "archyl-conformance"),
			Fingerprint: fingerprint(v),
			Severity:    severity,
			Location:    codeQualityLocation{Path: v.FilePath, Lines: codeQualityLines{Begin: line}},
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(issues)
}

// fingerprint is stable across runs for the same finding, which is how GitLab
// tells new issues from existing ones.
func fingerprint(v api.Violation) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{v.RuleID, v.FilePath, v.Title}, "\x00")))
	return hex.EncodeToString(sum[:16])
}

// SARIF writes a SARIF 2.1.0 log, accepted by GitHub code scanning, Azure
// DevOps and most security dashboards.
func SARIF(w io.Writer, violations []api.Violation, rules []api.Rule, version string) error {
	type message struct {
		Text string `json:"text"`
	}
	type sarifRule struct {
		ID               string  `json:"id"`
		Name             string  `json:"name,omitempty"`
		ShortDescription message `json:"shortDescription"`
		FullDescription  message `json:"fullDescription,omitzero"`
	}
	type region struct {
		StartLine int `json:"startLine,omitempty"`
		EndLine   int `json:"endLine,omitempty"`
	}
	type physicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region *region `json:"region,omitempty"`
	}
	type location struct {
		PhysicalLocation physicalLocation `json:"physicalLocation"`
	}
	type result struct {
		RuleID              string            `json:"ruleId"`
		Level               string            `json:"level"`
		Message             message           `json:"message"`
		Locations           []location        `json:"locations,omitempty"`
		PartialFingerprints map[string]string `json:"partialFingerprints"`
	}

	sarifRules := make([]sarifRule, 0, len(rules))
	for _, r := range rules {
		sarifRules = append(sarifRules, sarifRule{
			ID: r.ID, Name: r.Name,
			ShortDescription: message{Text: firstNonEmpty(r.Name, r.ID)},
			FullDescription:  message{Text: r.Description},
		})
	}
	results := make([]result, 0, len(violations))
	for _, v := range violations {
		level := "note"
		switch Severity(v.Severity) {
		case 4, 3:
			level = "error"
		case 2:
			level = "warning"
		}
		res := result{
			RuleID:              firstNonEmpty(v.RuleID, "archyl-conformance"),
			Level:               level,
			Message:             message{Text: Message(v)},
			PartialFingerprints: map[string]string{"archylFinding/v1": fingerprint(v)},
		}
		if v.FilePath != "" {
			var loc location
			loc.PhysicalLocation.ArtifactLocation.URI = v.FilePath
			if v.LineStart > 0 {
				loc.PhysicalLocation.Region = &region{StartLine: v.LineStart, EndLine: max(v.LineEnd, v.LineStart)}
			}
			res.Locations = []location{loc}
		}
		results = append(results, res)
	}

	log := map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []any{map[string]any{
			"tool": map[string]any{"driver": map[string]any{
				"name":           "archyl",
				"version":        version,
				"informationUri": "https://github.com/archyl-com/cli",
				"rules":          sarifRules,
			}},
			"results": results,
		}},
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(log)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
