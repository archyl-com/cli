package decide

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// maxPromptBytes keeps the request under the chat's per-message limit
// (32 KiB) with room to spare.
const maxPromptBytes = 28 * 1024

// Field lengths a reply is cut to: enough for an ADR, not for an essay.
const (
	maxTitle   = 120
	maxSection = 4000
	maxItem    = 400
	maxItems   = 6
	maxADRs    = 60
	// maxTouched is how many touched elements the prompt lists; the rest
	// are counted.
	maxTouched = 40
)

// MaxLinks is the most elements a decision is linked to: one about more is
// about the whole system, and each link is a request.
const MaxLinks = 12

// maxDefaultLinks is the most elements a draft links when nobody chose: a
// change touching more is too broad to guess what the decision is about.
const maxDefaultLinks = 3

// ErrNoDraft is returned when a reply holds no usable draft.
var ErrNoDraft = errors.New("the reply holds no decision draft")

// reply is the JSON object the prompt asks for. Everything but the text
// stays raw: models often answer in a slightly different shape ("ADR-4" for
// 4, objects for names, a paragraph for a list), and one odd field must not
// lose the whole draft.
type reply struct {
	Title        string          `json:"title"`
	Context      string          `json:"context"`
	Decision     string          `json:"decision"`
	Consequences json.RawMessage `json:"consequences"`
	Alternatives json.RawMessage `json:"alternatives"`
	Elements     json.RawMessage `json:"elements"`
	Supersedes   json.RawMessage `json:"supersedes"`
	DuplicateOf  json.RawMessage `json:"duplicateOf"`
}

// Prompt asks for a decision record drafted from the facts, and nothing but
// the facts.
func Prompt(f Facts) string {
	var facts strings.Builder
	write := func(format string, args ...any) { fmt.Fprintf(&facts, format, args...) }

	if decided := strings.TrimSpace(f.Decided); decided != "" {
		write("What the developer says was decided: %s\n", clipText(decided, 2000))
	} else {
		write("The developer did not say what was decided: infer it from the diff.\n")
	}
	if f.Branch != "" {
		write("Branch: %s", f.Branch)
		if f.Base != "" {
			write(" (compared with %s)", f.Base)
		}
		write("\n")
	}

	write("\nElements the change touches (the only ones you may name):\n")
	if len(f.Touched) == 0 {
		write("- none: the changed files are not in the architecture model\n")
	}
	for _, t := range capList(f.Touched, maxTouched) {
		write("- %s (%s", t.Name, t.Type)
		if parent := parentOf(t.Element); parent != "" {
			write(", in %s", parent)
		}
		write(")")
		if len(t.UsedBy) > 0 {
			write(", used by %s", strings.Join(capList(t.UsedBy, 8), ", "))
		}
		write("\n")
	}
	if more := len(f.Touched) - maxTouched; more > 0 {
		write("- and %d more\n", more)
	}

	if len(f.ADRs) > 0 {
		write("\nDecisions already recorded:\n")
		for _, a := range capList(f.ADRs, maxADRs) {
			write("- ADR-%d %s (%s)\n", a.Number, clip(a.Title, maxTitle), a.Status)
		}
	}

	if len(f.Files) > 0 {
		write("\nChanged files:\n")
		for _, path := range filePaths(f, 60) {
			write("- %s\n", path)
		}
	}

	const factsHeading, diffHeading = "\nFacts:\n", "\nThe diff:\n"
	room := maxPromptBytes - len(instructions) - len(factsHeading) - len(diffHeading)
	head := cutAtLine(facts.String(), room)
	diff := cutAtLine(f.Diff, room-len(head))
	if diff == "" {
		return instructions + factsHeading + head
	}
	return instructions + factsHeading + head + diffHeading + diff
}

const instructions = `You are drafting an Architecture Decision Record for a change a developer just made. ` +
	`Below are the facts: what they say was decided, the architecture elements the change touches, ` +
	`the decisions already recorded, the changed files and the diff.

Rules:
- Use only the facts below. Do not invent requirements, numbers, benchmarks, incidents, people or technologies. ` +
	`When the facts do not say why, write what the change shows and leave the rest out.
- Plain English, concrete and short. No marketing words.
- Name elements exactly as they are written below, and only those.
- If a recorded decision is replaced by this one, give its number in "supersedes"; ` +
	`if one already records this same decision, give its number in "duplicateOf". Otherwise 0.
- Reply with one JSON object and nothing else, in this shape:
{"title": "the decision as a short imperative sentence, at most 12 words",
 "context": "2 to 4 sentences: the problem or forces that led to the decision, as the facts show them",
 "decision": "2 to 4 sentences: what was decided and how the change applies it",
 "consequences": {"positive": ["one line each, at most 4"], "negative": ["one line each, at most 4: costs, risks, follow-ups"]},
 "alternatives": [{"option": "another way the facts show or imply", "whyNot": "why the change did not take it"}],
 "elements": ["the names of the touched elements this decision is about"],
 "supersedes": 0,
 "duplicateOf": 0}
`

// ParseReply reads a draft out of a reply, tolerating prose or a code fence
// around the JSON. Its text is stripped of control characters. Elements and
// decision numbers the facts do not hold are dropped, and at most MaxLinks
// elements are kept; when none is left, the draft links what defaultElements
// gives.
func ParseReply(text string, f Facts) (*Draft, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, ErrNoDraft
	}
	var r reply
	if err := json.Unmarshal([]byte(text[start:end+1]), &r); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoDraft, err)
	}
	d := &Draft{
		Title:        clip(r.Title, maxTitle),
		Status:       StatusProposed,
		Context:      clipText(r.Context, maxSection),
		Decision:     clipText(r.Decision, maxSection),
		Consequences: consequences(r.Consequences),
		Alternatives: alternatives(r.Alternatives),
	}
	if d.Title == "" || d.Decision == "" {
		return nil, ErrNoDraft
	}
	if n := adrNumber(r.Supersedes); f.hasADR(n) {
		d.Supersedes = n
	}
	if n := adrNumber(r.DuplicateOf); f.hasADR(n) {
		d.DuplicateOf = n
	}

	candidates := f.Elements()
	for _, ref := range elementRefs(r.Elements) {
		if len(d.Elements) == MaxLinks {
			break
		}
		if e, ok := resolve(ref, candidates); ok && !containsElement(d.Elements, e) {
			d.Elements = append(d.Elements, e)
		}
	}
	if len(d.Elements) == 0 {
		d.Elements = defaultElements(candidates)
	}
	return d, nil
}

// defaultElements are what a draft links when nobody chose: everything the
// change touches when that is a few elements, otherwise nothing.
func defaultElements(candidates []Element) []Element {
	if len(candidates) > maxDefaultLinks {
		return []Element{}
	}
	return candidates
}

// consequences renders the reply's consequences as Markdown: positive and
// negative lists, or the text as given.
func consequences(raw json.RawMessage) string {
	var lists struct {
		Positive []string `json:"positive"`
		Negative []string `json:"negative"`
	}
	if json.Unmarshal(raw, &lists) != nil {
		return rawText(raw)
	}
	var parts []string
	if items := bullets(lists.Positive); items != "" {
		parts = append(parts, "**Positive**\n\n"+items)
	}
	if items := bullets(lists.Negative); items != "" {
		parts = append(parts, "**Negative**\n\n"+items)
	}
	return strings.Join(parts, "\n\n")
}

// alternatives renders the reply's alternatives as a Markdown list. Each is
// an object, or a plain string.
func alternatives(raw json.RawMessage) string {
	var objects []struct {
		Option string `json:"option"`
		WhyNot string `json:"whyNot"`
	}
	if json.Unmarshal(raw, &objects) == nil {
		var items []string
		for _, o := range objects {
			option, why := clip(o.Option, maxItem), clip(o.WhyNot, maxItem)
			switch {
			case option == "":
				continue
			case why != "":
				items = append(items, option+": "+why)
			default:
				items = append(items, option)
			}
		}
		return bullets(items)
	}
	var strs []string
	if json.Unmarshal(raw, &strs) == nil {
		return bullets(strs)
	}
	return rawText(raw)
}

// rawText is a JSON string's text, or nothing for any other value.
func rawText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return clipText(s, maxSection)
}

// bullets is a Markdown list of the non-empty items, at most maxItems.
func bullets(items []string) string {
	var lines []string
	for _, item := range items {
		if item = clip(item, maxItem); item != "" && len(lines) < maxItems {
			lines = append(lines, "- "+item)
		}
	}
	return strings.Join(lines, "\n")
}

// adrNumber reads a decision number given as 4, "4" or "ADR-4"; anything
// else is 0.
func adrNumber(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return max(n, 0)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return ParseADRNumber(s)
	}
	return 0
}

// ParseADRNumber reads "ADR-4", "adr 4", "#4" or "4"; anything else is 0.
func ParseADRNumber(s string) int {
	s = strings.TrimSpace(strings.ToUpper(s))
	if rest, ok := strings.CutPrefix(s, "ADR"); ok {
		s = strings.TrimLeft(rest, "-: ")
	} else {
		s = strings.TrimPrefix(s, "#")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// elementRefs reads the reply's elements: names, or objects with a name.
func elementRefs(raw json.RawMessage) []string {
	var names []string
	if json.Unmarshal(raw, &names) == nil {
		return names
	}
	var objects []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &objects) != nil {
		return nil
	}
	for _, o := range objects {
		names = append(names, firstNonEmpty(o.ID, o.Name))
	}
	return names
}

// resolve finds the element a reference names: its ID, its path, or a name
// only one candidate has.
func resolve(ref string, candidates []Element) (Element, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Element{}, false
	}
	var byName []Element
	for _, e := range candidates {
		switch {
		case e.ID == ref, e.Path != "" && strings.EqualFold(e.Path, ref):
			return e, true
		case strings.EqualFold(e.Name, ref):
			byName = append(byName, e)
		}
	}
	if len(byName) == 1 {
		return byName[0], true
	}
	return Element{}, false
}

func containsElement(elements []Element, e Element) bool {
	for _, have := range elements {
		if have.ID == e.ID {
			return true
		}
	}
	return false
}

// parentOf is where an element sits, from its path.
func parentOf(e Element) string {
	if i := strings.LastIndex(e.Path, " / "); i > 0 {
		return e.Path[:i]
	}
	return ""
}

// capList keeps the first n items.
func capList[T any](items []T, n int) []T {
	return items[:min(len(items), n)]
}
