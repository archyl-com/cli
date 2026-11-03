// Package guard is the PreToolUse hook that checks what a coding agent is
// about to write against the project's conformance rules. It fails open: no
// configuration, no network or an unexpected payload lets the edit through.
package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// HookInput is the part of Claude Code's PreToolUse payload the guard reads.
type HookInput struct {
	ToolName  string    `json:"tool_name"`
	ToolInput ToolInput `json:"tool_input"`
}

// ToolInput carries Write, Edit and MultiEdit arguments.
type ToolInput struct {
	FilePath   string `json:"file_path"`
	Content    string `json:"content"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
	Edits      []Edit `json:"edits"`
}

// Edit is one replacement of a MultiEdit.
type Edit struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// ProposedContent returns the file as it would be after the tool runs, and
// false when the tool is not a file write the guard understands.
func ProposedContent(in HookInput, readFile func(string) ([]byte, error)) (string, bool) {
	switch in.ToolName {
	case "Write":
		return in.ToolInput.Content, in.ToolInput.Content != ""
	case "Edit", "MultiEdit":
		current, err := readFile(in.ToolInput.FilePath)
		if err != nil {
			return "", false
		}
		content := string(current)
		edits := in.ToolInput.Edits
		if len(edits) == 0 {
			edits = []Edit{{OldString: in.ToolInput.OldString, NewString: in.ToolInput.NewString, ReplaceAll: in.ToolInput.ReplaceAll}}
		}
		for _, e := range edits {
			if e.OldString == "" {
				continue
			}
			n := 1
			if e.ReplaceAll {
				n = -1
			}
			content = strings.Replace(content, e.OldString, e.NewString, n)
		}
		return content, content != ""
	}
	return "", false
}

// Level decides which severities block an edit.
type Level string

const (
	BlockCritical Level = "critical"
	BlockHigh     Level = "high"
	BlockOff      Level = "off"
)

// ParseLevel reads ARCHYL_GUARD_BLOCK; anything unknown means critical.
func ParseLevel(s string) Level {
	switch Level(strings.ToLower(strings.TrimSpace(s))) {
	case BlockHigh:
		return BlockHigh
	case BlockOff:
		return BlockOff
	}
	return BlockCritical
}

// Finding is a violation as the guard reports it.
type Finding struct {
	Severity   string
	Title      string
	Suggestion string
}

// Decision is the hook's answer.
type Decision struct {
	// Deny blocks the edit; Reason tells the agent why.
	Deny   bool
	Reason string
	// Warning is shown without blocking.
	Warning string
}

// Decide applies the block level to the counts and findings of a check.
func Decide(path string, critical, high int, findings []Finding, level Level) Decision {
	details := formatFindings(findings)
	blocking := critical
	if level == BlockHigh {
		blocking += high
	}
	if blocking > 0 {
		return Decision{
			Deny: true,
			Reason: fmt.Sprintf("Archyl Guard: this change violates the project's architecture rules for %s:\n%s\n\n"+
				"Adjust the change to respect these rules, or ask the user whether to override them.", path, details),
		}
	}
	if critical+high > 0 {
		return Decision{Warning: fmt.Sprintf("Archyl Guard: %d high-severity architecture warning(s) on %s:\n%s", critical+high, path, details)}
	}
	return Decision{}
}

func formatFindings(findings []Finding) string {
	var lines []string
	for _, f := range findings {
		if f.Severity != "critical" && f.Severity != "high" {
			continue
		}
		if len(lines) == 5 {
			break
		}
		line := fmt.Sprintf("- [%s] %s", f.Severity, f.Title)
		if f.Suggestion != "" {
			line += " — " + f.Suggestion
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// HookOutput is Claude Code's PreToolUse response.
type HookOutput struct {
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
	SystemMessage      string        `json:"systemMessage,omitempty"`
}

type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

// Output renders a decision as hook JSON, or nil when there is nothing to say.
func (d Decision) Output() []byte {
	var out *HookOutput
	switch {
	case d.Deny:
		out = &HookOutput{HookSpecificOutput: &hookSpecific{"PreToolUse", "deny", d.Reason}}
	case d.Warning != "":
		// No permission decision: "allow" would skip the user's prompt, for
		// the very edits the rules flagged.
		out = &HookOutput{SystemMessage: d.Warning}
	default:
		return nil
	}
	raw, _ := json.Marshal(out)
	return raw
}

// ReadFile is os.ReadFile, injectable in tests.
var ReadFile = os.ReadFile
