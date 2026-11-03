// Package decide drafts an Architecture Decision Record from a change: the
// facts gathered from the branch, the prompt that turns them into a draft,
// the parsing of the reply, and the Markdown file the draft is edited in.
package decide

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/repo"
)

// DiffLimit bounds the diff handed to the AI, so the prompt stays under the
// chat's per-message limit.
const DiffLimit = 20 * 1024

// minDiffPart is the smallest piece of a file worth keeping when the diff
// runs out of room: below it, the file is only named.
const minDiffPart = 512

// Facts are everything a draft may draw on.
type Facts struct {
	// Decided is what the developer says was decided; empty lets the draft
	// infer it from the diff.
	Decided string
	Branch  string
	Base    string
	Files   []repo.Change
	// Diff is the change itself, trimmed to DiffLimit.
	Diff    string
	Touched []Touched
	ADRs    []ADRRef
}

// Element is an element of the model a decision can be linked to.
type Element struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Type is the C4 level: system, container, component or code.
	Type string `json:"type"`
	// Path is the element's place in the model, "Shop / Orders API".
	Path string `json:"path,omitempty"`
}

// Touched is an element the change lands in, with what depends on it.
type Touched struct {
	Element
	UsedBy []string `json:"usedBy,omitempty"`
}

// ADRRef is a decision already recorded in the project.
type ADRRef struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// FromImpact lists the elements a change touches, from a locate result.
// Names come from the model, so they are stripped of control characters.
func FromImpact(r *api.LocateResult) []Touched {
	if r == nil || r.Impact == nil {
		return nil
	}
	out := make([]Touched, 0, len(r.Impact.Elements))
	for _, e := range r.Impact.Elements {
		t := Touched{Element: Element{ID: e.ID, Name: clip(e.Name, maxTitle), Type: e.Type, Path: clip(e.Path, maxSection)}}
		for _, d := range e.Dependents {
			t.UsedBy = append(t.UsedBy, clip(d.Name, maxTitle))
		}
		out = append(out, t)
	}
	return out
}

// FromADRs lists the recorded decisions, newest first, their text stripped
// of control characters.
func FromADRs(adrs []api.ADR) []ADRRef {
	out := make([]ADRRef, 0, len(adrs))
	for _, a := range adrs {
		out = append(out, ADRRef{Number: a.Number, Title: clip(a.Title, maxTitle), Status: clip(a.Status, maxItem)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out
}

// Elements are the elements a draft may be linked to: the touched ones.
func (f Facts) Elements() []Element {
	out := make([]Element, 0, len(f.Touched))
	for _, t := range f.Touched {
		out = append(out, t.Element)
	}
	return out
}

// hasADR reports whether the project has a decision with that number.
func (f Facts) hasADR(number int) bool {
	for _, a := range f.ADRs {
		if a.Number == number {
			return true
		}
	}
	return false
}

// TrimDiff joins the files' diffs within limit bytes. Source files come
// first; tests, lock files and generated code only get the room left. A file
// that does not fit is cut, or only named when too little room is left.
func TrimDiff(diffs []repo.FileDiff, limit int) string {
	ordered := append([]repo.FileDiff(nil), diffs...)
	sort.SliceStable(ordered, func(i, j int) bool { return diffRank(ordered[i]) < diffRank(ordered[j]) })

	var b strings.Builder
	var omitted []string
	for _, d := range ordered {
		room := limit - b.Len()
		switch {
		case len(d.Text) <= room:
			b.WriteString(d.Text)
		case room >= minDiffPart:
			b.WriteString(cutAtLine(d.Text, room-len(truncatedNote)))
			b.WriteString(truncatedNote)
		default:
			omitted = append(omitted, d.Path)
		}
	}
	if len(omitted) > 0 {
		b.WriteString("\n(diff left out for: " + strings.Join(omitted, ", ") + ")\n")
	}
	return b.String()
}

const truncatedNote = "… (rest of this file's diff left out)\n"

// cutAtLine cuts s to at most n bytes, at the end of a line when it can and
// never inside a character.
func cutAtLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	cut := s[:runeBoundary(s, n)]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		return cut[:i+1]
	}
	// No line end in reach: keep a byte for one.
	if cut = s[:runeBoundary(s, n-1)]; cut == "" {
		return ""
	}
	return cut + "\n"
}

// runeBoundary moves n back to the start of the character it falls in.
func runeBoundary(s string, n int) int {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// diffRank orders diffs by how much they say about a decision: source code,
// then tests, then generated and lock files.
func diffRank(d repo.FileDiff) int {
	switch {
	case generated(d):
		return 2
	case testFile(d.Path):
		return 1
	}
	return 0
}

func testFile(path string) bool {
	segments := strings.Split(strings.ToLower(path), "/")
	name := segments[len(segments)-1]
	for _, dir := range segments[:len(segments)-1] {
		switch dir {
		case "test", "tests", "__tests__", "testdata", "fixtures", "spec", "e2e":
			return true
		}
	}
	for _, marker := range []string{"_test.", ".test.", ".spec."} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return strings.HasPrefix(name, "test_")
}

func generated(d repo.FileDiff) bool {
	lower := strings.ToLower(d.Path)
	base := lower[strings.LastIndexByte(lower, '/')+1:]
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "cargo.lock", "poetry.lock", "gemfile.lock", "composer.lock":
		return true
	}
	for _, suffix := range []string{".pb.go", "_gen.go", ".gen.go", ".generated.ts", ".min.js", ".min.css", ".snap", ".svg"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, dir := range []string{"vendor/", "node_modules/", "dist/"} {
		if strings.HasPrefix(lower, dir) || strings.Contains(lower, "/"+dir) {
			return true
		}
	}
	return strings.Contains(d.Text, "Code generated") && strings.Contains(d.Text, "DO NOT EDIT")
}
