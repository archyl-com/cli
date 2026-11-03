package guard

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProposedContent(
	t *testing.T,
) {
	current := "package billing\n\nimport \"db\"\nimport \"db\"\n"
	read := func(string) ([]byte, error) { return []byte(current), nil }
	missing := func(string) ([]byte, error) { return nil, errors.New("no file") }

	testCases := []struct {
		name   string
		input  HookInput
		read   func(string) ([]byte, error)
		want   string
		wantOK bool
	}{
		{
			name:   "write uses the new content",
			input:  HookInput{ToolName: "Write", ToolInput: ToolInput{FilePath: "a.go", Content: "new"}},
			read:   missing,
			want:   "new",
			wantOK: true,
		},
		{
			name:   "edit replaces the first occurrence",
			input:  HookInput{ToolName: "Edit", ToolInput: ToolInput{FilePath: "a.go", OldString: `"db"`, NewString: `"repo"`}},
			read:   read,
			want:   "package billing\n\nimport \"repo\"\nimport \"db\"\n",
			wantOK: true,
		},
		{
			name:   "edit with replace_all replaces every occurrence",
			input:  HookInput{ToolName: "Edit", ToolInput: ToolInput{FilePath: "a.go", OldString: `"db"`, NewString: `"repo"`, ReplaceAll: true}},
			read:   read,
			want:   "package billing\n\nimport \"repo\"\nimport \"repo\"\n",
			wantOK: true,
		},
		{
			name: "multi-edit applies edits in order",
			input: HookInput{ToolName: "MultiEdit", ToolInput: ToolInput{FilePath: "a.go", Edits: []Edit{
				{OldString: "billing", NewString: "invoicing"},
				{OldString: `"db"`, NewString: `"repo"`, ReplaceAll: true},
			}}},
			read:   read,
			want:   "package invoicing\n\nimport \"repo\"\nimport \"repo\"\n",
			wantOK: true,
		},
		{
			name:  "edit of a missing file is skipped",
			input: HookInput{ToolName: "Edit", ToolInput: ToolInput{FilePath: "a.go", OldString: "x", NewString: "y"}},
			read:  missing,
		},
		{
			name:  "other tools are skipped",
			input: HookInput{ToolName: "Bash", ToolInput: ToolInput{FilePath: "a.go"}},
			read:  read,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := ProposedContent(testCase.input, testCase.read)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestDecide(
	t *testing.T,
) {
	findings := []Finding{
		{Severity: "critical", Title: "Domain imports infrastructure", Suggestion: "Depend on the port"},
		{Severity: "high", Title: "Handler talks to the database"},
		{Severity: "low", Title: "ignored"},
	}

	testCases := []struct {
		name         string
		critical     int
		high         int
		level        Level
		wantDecision string
	}{
		{name: "critical blocks", critical: 1, high: 1, level: BlockCritical, wantDecision: "deny"},
		{name: "high warns at critical level", critical: 0, high: 1, level: BlockCritical, wantDecision: "allow"},
		{name: "high blocks at high level", critical: 0, high: 1, level: BlockHigh, wantDecision: "deny"},
		{name: "clean says nothing", level: BlockCritical, wantDecision: ""},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := Decide("internal/billing/a.go", testCase.critical, testCase.high, findings, testCase.level).Output()
			if testCase.wantDecision == "" {
				assert.Nil(t, out)
				return
			}
			var parsed HookOutput
			require.NoError(t, json.Unmarshal(out, &parsed))
			if testCase.wantDecision == "deny" {
				require.NotNil(t, parsed.HookSpecificOutput)
				assert.Equal(t, "PreToolUse", parsed.HookSpecificOutput.HookEventName)
				assert.Equal(t, "deny", parsed.HookSpecificOutput.PermissionDecision)
				assert.Contains(t, parsed.HookSpecificOutput.PermissionDecisionReason, "- [critical] Domain imports infrastructure — Depend on the port")
				assert.NotContains(t, parsed.HookSpecificOutput.PermissionDecisionReason, "ignored")
			} else {
				assert.Nil(t, parsed.HookSpecificOutput, "a warning must not answer the permission prompt")
				assert.Contains(t, parsed.SystemMessage, "Handler talks to the database")
			}
		})
	}
}

func TestParseLevel(
	t *testing.T,
) {
	assert.Equal(t, BlockCritical, ParseLevel(""))
	assert.Equal(t, BlockHigh, ParseLevel(" HIGH "))
	assert.Equal(t, BlockOff, ParseLevel("off"))
	assert.Equal(t, BlockCritical, ParseLevel("nonsense"))
}

func TestWarningLeavesThePermissionPromptAlone(t *testing.T) {
	raw := Decision{Warning: "Archyl Guard: 1 warning"}.Output()
	assert.JSONEq(t, `{"systemMessage":"Archyl Guard: 1 warning"}`, string(raw))
}
