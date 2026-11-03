package decide

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrEmpty is returned for a file with nothing in it: the person editing it
// cancelled.
var ErrEmpty = errors.New("the draft is empty")

// ErrNoTitle is returned for a draft without a title.
var ErrNoTitle = errors.New("the draft has no title")

// Section headings of the Markdown form, in order.
const (
	headingContext      = "Context"
	headingDecision     = "Decision"
	headingConsequences = "Consequences"
	headingAlternatives = "Alternatives considered"
)

// frontMatter is the head of the Markdown form.
type frontMatter struct {
	Title      string   `yaml:"title"`
	Status     string   `yaml:"status"`
	Supersedes string   `yaml:"supersedes,omitempty"`
	Elements   []string `yaml:"elements"`
}

// Markdown is the draft as a Markdown document with YAML front matter, the
// form it is printed and edited in. Elements are named by their path when
// their name alone is ambiguous among the candidates.
func (d *Draft) Markdown(candidates []Element) string {
	fm := frontMatter{Title: d.Title, Status: firstNonEmpty(d.Status, StatusProposed), Elements: []string{}}
	if d.Supersedes > 0 {
		fm.Supersedes = fmt.Sprintf("ADR-%d", d.Supersedes)
	}
	for _, e := range d.Elements {
		fm.Elements = append(fm.Elements, label(e, candidates))
	}
	head, _ := yaml.Marshal(fm)

	return "---\n" + string(head) + "---\n\n" + d.Body()
}

// Body is the draft's sections as Markdown, without the front matter.
func (d *Draft) Body() string {
	var b strings.Builder
	for i, s := range []struct{ heading, text string }{
		{headingContext, d.Context},
		{headingDecision, d.Decision},
		{headingConsequences, d.Consequences},
		{headingAlternatives, d.Alternatives},
	} {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## " + s.heading + "\n\n")
		if text := strings.TrimSpace(s.text); text != "" {
			b.WriteString(text + "\n")
		}
	}
	return b.String()
}

// EditorFile is the draft as the file a person edits: the Markdown form,
// after a comment that says how the file is read back.
func (d *Draft) EditorFile(candidates []Element) string {
	var b strings.Builder
	b.WriteString("<!--\nEdit the decision, then save and close the file to continue.\n")
	b.WriteString("Delete everything to cancel. Comments like this one are ignored.\n")
	fmt.Fprintf(&b, "status: %s.\n", strings.Join(statuses, " or "))
	if len(candidates) > 0 {
		b.WriteString("elements: the decision is linked to these; pick from\n")
		for _, e := range candidates {
			fmt.Fprintf(&b, "  - %s (%s)\n", label(e, candidates), e.Type)
		}
	}
	b.WriteString("supersedes: optional, the decision this one replaces, like ADR-4.\n-->\n")
	b.WriteString(d.Markdown(candidates))
	return b.String()
}

// Parse reads a draft back from its Markdown form. The comment that opens
// the file, the instructions EditorFile writes, is dropped; any other comment
// is the person's text and kept. Elements are resolved among the facts'
// touched elements; the references that match none are returned apart, so
// the caller can say they will not be linked. An empty file is ErrEmpty.
func Parse(text string, f Facts) (*Draft, []string, error) {
	text = strings.TrimPrefix(strings.ReplaceAll(text, "\r\n", "\n"), "\ufeff")
	text = dropLeadingComment(strings.TrimSpace(text))
	if text == "" {
		return nil, nil, ErrEmpty
	}

	var fm frontMatter
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		head, after, found := cutLine(rest, "---")
		if !found {
			return nil, nil, errors.New("the front matter has no closing --- line")
		}
		if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
			return nil, nil, fmt.Errorf("read the front matter: %w", err)
		}
		body = after
	}

	d := &Draft{
		Title:    clip(fm.Title, maxTitle),
		Status:   strings.ToLower(strings.TrimSpace(fm.Status)),
		Elements: []Element{},
	}
	if d.Status == "" {
		d.Status = StatusProposed
	}
	if !slices.Contains(statuses, d.Status) {
		return nil, nil, fmt.Errorf("status %q is not one of %s", fm.Status, strings.Join(statuses, ", "))
	}
	if s := strings.TrimSpace(fm.Supersedes); s != "" {
		n := ParseADRNumber(s)
		if !f.hasADR(n) {
			return nil, nil, fmt.Errorf("supersedes %q is not a decision of this project", s)
		}
		d.Supersedes = n
	}

	title := readSections(body, d)
	if d.Title == "" {
		d.Title = clip(title, maxTitle)
	}
	if d.Title == "" {
		return nil, nil, ErrNoTitle
	}

	if len(fm.Elements) > MaxLinks {
		return nil, nil, fmt.Errorf("a decision links at most %d elements; keep the ones it is about", MaxLinks)
	}
	var unknown []string
	candidates := f.Elements()
	for _, ref := range fm.Elements {
		e, ok := resolve(stripType(ref), candidates)
		switch {
		case !ok:
			unknown = append(unknown, ref)
		case !containsElement(d.Elements, e):
			d.Elements = append(d.Elements, e)
		}
	}
	return d, unknown, nil
}

// readSections fills the draft's sections from the body and returns the text
// of a "# " heading, a title written without front matter. A "## " heading
// it does not know stays in the section above it, so nothing written is
// lost; text above every section goes to the context. Headings inside code
// fences are text.
func readSections(body string, d *Draft) string {
	targets := map[string]*string{
		strings.ToLower(headingContext):      &d.Context,
		strings.ToLower(headingDecision):     &d.Decision,
		strings.ToLower(headingConsequences): &d.Consequences,
		strings.ToLower(headingAlternatives): &d.Alternatives,
		"alternatives":                       &d.Alternatives,
	}
	var title string
	var preamble strings.Builder
	current := &preamble
	builders := map[*string]*strings.Builder{}
	fence := ""
	for _, line := range strings.Split(body, "\n") {
		if fence = nextFence(fence, line); fence != "" || isFence(line) {
			current.WriteString(line + "\n")
			continue
		}
		if t, ok := strings.CutPrefix(line, "# "); ok && title == "" && current == &preamble {
			title = strings.TrimSpace(t)
			continue
		}
		if h, ok := strings.CutPrefix(line, "## "); ok {
			if target, known := targets[strings.ToLower(strings.TrimSpace(h))]; known {
				if builders[target] == nil {
					builders[target] = &strings.Builder{}
				}
				current = builders[target]
				continue
			}
		}
		current.WriteString(line + "\n")
	}
	for target, b := range builders {
		*target = strings.TrimSpace(b.String())
	}
	if pre := strings.TrimSpace(preamble.String()); pre != "" {
		d.Context = joinParagraphs(pre, d.Context)
	}
	return title
}

// dropLeadingComment removes the HTML comment the text opens with.
func dropLeadingComment(text string) string {
	rest, ok := strings.CutPrefix(text, "<!--")
	if !ok {
		return text
	}
	if _, after, found := strings.Cut(rest, "-->"); found {
		return strings.TrimSpace(after)
	}
	return text
}

// isFence reports whether a line opens or closes a code fence.
func isFence(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// nextFence is the fence open after line, given the one open before it: a
// fence closes only with the marker that opened it.
func nextFence(open, line string) string {
	if !isFence(line) {
		return open
	}
	marker := strings.TrimSpace(line)[:3]
	switch open {
	case "":
		return marker
	case marker:
		return ""
	}
	return open
}

// cutLine splits s around the first line that is exactly sep.
func cutLine(s, sep string) (before, after string, found bool) {
	lines := strings.SplitAfter(s, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " \t\n") == sep {
			return strings.Join(lines[:i], ""), strings.Join(lines[i+1:], ""), true
		}
	}
	return s, "", false
}

// label names an element in the Markdown form: by name, or by path when
// another candidate has the same name.
func label(e Element, candidates []Element) string {
	same := 0
	for _, c := range candidates {
		if strings.EqualFold(c.Name, e.Name) {
			same++
		}
	}
	if same > 1 && e.Path != "" {
		return e.Path
	}
	return e.Name
}

// typeSuffix matches the "(container)" a person may copy from the list in
// the editor comment.
var typeSuffix = regexp.MustCompile(`\s*\((system|container|component|code)\)$`)

func stripType(ref string) string {
	return typeSuffix.ReplaceAllString(strings.TrimSpace(ref), "")
}
