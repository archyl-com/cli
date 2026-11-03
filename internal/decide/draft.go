package decide

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// StatusProposed is the status a drafted decision is recorded with: it was
// written by a person and a machine together, and the team has not agreed
// on it yet.
const StatusProposed = "proposed"

// statuses are the ones a draft may be recorded with.
var statuses = []string{StatusProposed, "accepted"}

// Draft is a decision ready to be reviewed and recorded.
type Draft struct {
	Title    string `json:"title"`
	Status   string `json:"status"`
	Context  string `json:"context"`
	Decision string `json:"decision"`
	// Consequences and Alternatives are Markdown, so a person editing the
	// draft can write them as they like.
	Consequences string `json:"consequences"`
	Alternatives string `json:"alternatives,omitempty"`
	// Supersedes is the number of a decision this one replaces.
	Supersedes int `json:"supersedes,omitempty"`
	// DuplicateOf is the number of a decision that already records this one,
	// as the AI judged it.
	DuplicateOf int `json:"duplicateOf,omitempty"`
	// Elements are what the decision is about; it is linked to them.
	Elements []Element `json:"elements"`
}

// Template is a draft with the facts filled in and the reasoning left to
// write: what was decided, the files and elements the change touches. It
// links the touched elements only when they are few (see defaultElements).
func Template(f Facts) *Draft {
	d := &Draft{
		Status:   StatusProposed,
		Decision: clipText(f.Decided, maxSection),
		Elements: defaultElements(f.Elements()),
	}
	d.Title = titleFrom(f.Decided)

	var b strings.Builder
	if names := elementNames(f.Elements(), 10); len(names) > 0 {
		fmt.Fprintf(&b, "The change touches %s.", strings.Join(names, ", "))
	}
	if n := len(f.Files); n > 0 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "Files changed: %s.", strings.Join(filePaths(f, 10), ", "))
	}
	d.Context = b.String()
	return d
}

// Request is the draft as the API records it. The API has no field for the
// alternatives or the decision it supersedes, so they go in the text.
func (d *Draft) Request() api.ADRRequest {
	req := api.ADRRequest{
		Title:        strings.TrimSpace(d.Title),
		Status:       firstNonEmpty(d.Status, StatusProposed),
		Context:      strings.TrimSpace(d.Context),
		Decision:     strings.TrimSpace(d.Decision),
		Consequences: strings.TrimSpace(d.Consequences),
	}
	if d.Supersedes > 0 {
		req.Context = joinParagraphs(req.Context, fmt.Sprintf("Supersedes ADR-%d.", d.Supersedes))
	}
	if alternatives := strings.TrimSpace(d.Alternatives); alternatives != "" {
		req.Decision = joinParagraphs(req.Decision, "### Alternatives considered\n\n"+alternatives)
	}
	return req
}

// titleFrom makes a title out of what the developer said: its first line,
// without a closing period, cut to a readable length.
func titleFrom(decided string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(decided), "\n")
	line = strings.TrimSuffix(strings.TrimSpace(line), ".")
	if line == "" {
		return ""
	}
	r := []rune(line)
	r[0] = unicode.ToUpper(r[0])
	return clip(string(r), maxTitle)
}

// elementNames lists up to limit elements, and how many more there are.
func elementNames(elements []Element, limit int) []string {
	names := make([]string, 0, min(len(elements), limit+1))
	for i, e := range elements {
		if i == limit {
			names = append(names, fmt.Sprintf("and %d more", len(elements)-limit))
			break
		}
		names = append(names, e.Name+" ("+e.Type+")")
	}
	return names
}

// filePaths lists up to limit changed files, and how many more there are.
func filePaths(f Facts, limit int) []string {
	var paths []string
	for i, c := range f.Files {
		if i == limit {
			paths = append(paths, fmt.Sprintf("and %d more", len(f.Files)-limit))
			break
		}
		paths = append(paths, c.Path)
	}
	return paths
}

func joinParagraphs(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "\n\n" + b
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// clip strips control characters, collapses whitespace and cuts s to n
// runes. Every text from outside the CLI goes through it or clipText.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(ui.StripControl(s)), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// clipText strips control characters and cuts s to n runes, keeping its
// line breaks.
func clipText(s string, n int) string {
	s = strings.TrimSpace(ui.StripControl(s))
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
