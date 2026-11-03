package shell

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/archyl-com/cli/internal/api"
)

// maxActionsPerQuestion bounds how many commands the assistant runs before it
// must answer.
const maxActionsPerQuestion = 4

// maxObservation bounds the command output sent back to the assistant.
const maxObservation = 6000

var actionLine = regexp.MustCompile(`^ACTION:\s*([a-z][a-z0-9-]*)\s*(.*)$`)

// ParseAction reads a reply made of a single "ACTION: name args" line.
func ParseAction(reply string) (name, args string, ok bool) {
	text := strings.TrimSpace(reply)
	text = strings.Trim(text, "`")
	text = strings.TrimSpace(strings.TrimPrefix(text, "text\n"))
	if strings.Contains(text, "\n") {
		first, rest, _ := strings.Cut(text, "\n")
		if strings.TrimSpace(rest) != "" {
			return "", "", false
		}
		text = first
	}
	m := actionLine.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", "", false
	}
	return m[1], strings.TrimSpace(m[2]), true
}

// MaybeAction reports whether a reply still streaming could turn out to be an
// action, so the session holds it back instead of flashing it on screen.
func MaybeAction(partial string) bool {
	t := strings.TrimLeft(partial, " \n`")
	const marker = "ACTION:"
	if len(t) < len(marker) {
		// Too short to tell: hold it back while it could still become one.
		return marker[:len(t)] == t
	}
	return strings.HasPrefix(t, "ACTION:")
}

// Actions lists the commands offered to the assistant.
func Actions() []api.ClientAction {
	var out []api.ClientAction
	for _, c := range Catalog {
		if c.AI {
			out = append(out, api.ClientAction{Name: c.Name, Usage: c.Usage, Description: c.Help, Writes: c.Writes})
		}
	}
	return out
}

// Mentions are the files and elements a message points at.
type Mentions struct {
	Files    []string
	Elements []string
}

var mentionRe = regexp.MustCompile(`(^|\s)([@#])("([^"]+)"|[^\s,;:!?()]+)`)

// ParseMentions finds @files and #elements ("#Payments", `#"Payments API"`).
func ParseMentions(text string) Mentions {
	var m Mentions
	for _, match := range mentionRe.FindAllStringSubmatch(text, -1) {
		value := match[3]
		if match[4] != "" {
			value = match[4]
		}
		value = strings.TrimRight(value, ".")
		if value == "" {
			continue
		}
		if match[2] == "@" {
			m.Files = append(m.Files, value)
		} else {
			m.Elements = append(m.Elements, value)
		}
	}
	return m
}

// Observation is a command's output as the assistant reads it: plain text,
// bounded.
func Observation(command, output string, err error) string {
	text := strings.TrimSpace(ansi.Strip(output))
	if len(text) > maxObservation {
		text = text[:maxObservation] + "\n… (truncated)"
	}
	if text == "" {
		text = "(no output)"
	}
	status := ""
	if err != nil {
		status = " (exited with an error)"
	}
	return fmt.Sprintf("Output of `archyl %s`%s:\n```\n%s\n```", command, status, text)
}

// Preview shortens command output for the scrollback under an action line.
func Preview(output string, lines int) (shown string, hidden int) {
	all := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for len(all) > 0 && strings.TrimSpace(ansi.Strip(all[0])) == "" {
		all = all[1:]
	}
	if len(all) <= lines {
		return strings.Join(all, "\n"), 0
	}
	return strings.Join(all[:lines], "\n"), len(all) - lines
}
