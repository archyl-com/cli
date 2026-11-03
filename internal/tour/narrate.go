package tour

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// maxPromptBytes keeps the narration request under the chat's per-message
// limit (32 KiB) with room to spare.
const maxPromptBytes = 28 * 1024

// ErrNoNarration is returned when a reply holds no usable narration.
var ErrNoNarration = errors.New("the reply holds no tour narration")

// Narration is what the AI writes for a tour.
type Narration struct {
	// Overview says what the system does and how work flows through it.
	Overview string `json:"overview"`
	// Flows are the main paths of work, one line each.
	Flows []string `json:"flows"`
	// Order is the stops' names in the order a newcomer should meet them.
	Order []string `json:"order"`
	Stops []struct {
		Name  string `json:"name"`
		Story string `json:"story"`
	} `json:"stops"`
	// Trace is a request to follow through the code.
	Trace string `json:"trace"`
}

// NarrationPrompt asks for the story of a planned tour. It hands over only
// the facts the tour holds, so the narration has nothing else to draw on.
func NarrationPrompt(t *Tour) string {
	var facts strings.Builder
	write := func(format string, args ...any) { fmt.Fprintf(&facts, format, args...) }

	if len(t.Stops) > 0 {
		o := t.Stops[0]
		write("Project: %s\n", t.Project)
		if o.ElementID != "" {
			write("The tour is about %s (%s).\n", o.Name, Describe(o))
		}
		if o.Description != "" {
			write("Description: %s\n", clip(o.Description, 600))
		}
		writeList(&facts, "Systems", o.Systems)
		writeList(&facts, "People", o.People)
		writeList(&facts, "Outside systems", o.Externals)
		writeList(&facts, "Shared infrastructure", o.Infrastructure)
	}
	write("\nStops:\n")
	for _, s := range t.Stops {
		if s.Kind != KindElement {
			continue
		}
		write("\n## %s (%s)\n", s.Name, Describe(s))
		if s.Description != "" {
			write("Description: %s\n", clip(s.Description, 500))
		}
		for _, p := range s.KeyParts {
			write("Part: %s", p.Name)
			if p.Description != "" {
				write(": %s", clip(p.Description, 160))
			}
			write("\n")
		}
		writeLinks(&facts, "Uses", s.Uses, s.UsesMore)
		writeLinks(&facts, "Used by", s.UsedBy, s.UsedByMore)
		writeList(&facts, "Infrastructure", s.Infrastructure)
	}
	if len(t.Trace) > 0 {
		write("\nA path through the stops: %s\n", strings.Join(t.Trace, " → "))
	}

	body := facts.String()
	if len(body) > maxPromptBytes {
		body = strings.ToValidUTF8(body[:maxPromptBytes], "") + "\n(The facts were cut here to fit.)\n"
	}
	return `You are writing a guided tour of a codebase for a developer who just joined the team. ` +
		`Below are the facts from its architecture model: the overview and the stops of the tour, ` +
		`each with what it uses and what uses it. Explain how the system works as a story: ` +
		`where work comes in, what happens to it and where it ends up.

Rules:
- Use only the facts below. Do not invent components, technologies or behaviour.
- Plain English, concrete and short. No marketing words, no Markdown.
- Name elements exactly as they are written below.
- Reply with one JSON object and nothing else, in this shape:
{"overview": "3 or 4 sentences: what the system does and how work flows through it",
 "flows": ["one line per main flow, at most 3, like A → B → C: what happens"],
 "order": ["every stop name, in the order a newcomer should meet them"],
 "stops": [{"name": "stop name", "story": "2 or 3 sentences: its role in the flows and what to understand about it first"}],
 "trace": "one sentence: a concrete piece of work to follow through the code, and what to look for on the way"}

Facts:
` + body
}

func writeList(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	if len(items) > 25 {
		items = append(items[:25:25], fmt.Sprintf("and %d more", len(items)-25))
	}
	fmt.Fprintf(b, "%s: %s\n", label, strings.Join(items, ", "))
}

func writeLinks(b *strings.Builder, label string, links []Link, more int) {
	if len(links) == 0 {
		return
	}
	parts := make([]string, 0, len(links))
	for _, l := range links {
		if l.Label != "" {
			parts = append(parts, l.Name+" ("+l.Label+")")
		} else {
			parts = append(parts, l.Name)
		}
	}
	if more > 0 {
		parts = append(parts, fmt.Sprintf("and %d more", more))
	}
	fmt.Fprintf(b, "%s: %s\n", label, strings.Join(parts, ", "))
}

// ParseNarration reads the JSON object out of a reply, tolerating prose or a
// code fence around it.
func ParseNarration(reply string) (*Narration, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end <= start {
		return nil, ErrNoNarration
	}
	var n Narration
	if err := json.Unmarshal([]byte(reply[start:end+1]), &n); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoNarration, err)
	}
	if strings.TrimSpace(n.Overview) == "" && len(n.Stops) == 0 {
		return nil, ErrNoNarration
	}
	return &n, nil
}

// Apply adds a narration to the tour: the overview and flows, each stop's
// story, the trace, and the order when it names every stop exactly once.
// Stories for names the tour does not have are dropped, and so are stories
// and order when two stops share a name, since a name then says nothing
// about which stop is meant.
func (t *Tour) Apply(n *Narration) {
	unique := t.uniqueNames()
	stories := map[string]string{}
	for _, s := range n.Stops {
		stories[strings.ToLower(strings.TrimSpace(s.Name))] = clip(s.Story, 700)
	}
	if !unique {
		stories = map[string]string{}
	}
	for i := range t.Stops {
		s := &t.Stops[i]
		switch s.Kind {
		case KindOverview:
			s.Story = clip(n.Overview, 900)
			for _, f := range n.Flows[:min(len(n.Flows), 3)] {
				if f = clip(f, 300); f != "" {
					s.Flows = append(s.Flows, f)
				}
			}
		case KindElement:
			s.Story = stories[strings.ToLower(s.Name)]
		}
	}
	if unique {
		t.reorder(n.Order)
	}
	t.TraceTask = clip(n.Trace, 400)
	t.Narrated = true
}

// uniqueNames reports whether every element stop has its own name.
func (t *Tour) uniqueNames() bool {
	seen := map[string]bool{}
	for _, s := range t.Stops {
		if s.Kind != KindElement {
			continue
		}
		key := strings.ToLower(s.Name)
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

// reorder puts the element stops in the given order when it is a
// permutation of their names that keeps the tour's focus first. A reason
// that named the previous stop no longer holds after a move and is dropped.
func (t *Tour) reorder(names []string) {
	byName := map[string]Stop{}
	var first, last []Stop
	for _, s := range t.Stops {
		switch s.Kind {
		case KindElement:
			byName[strings.ToLower(s.Name)] = s
		case KindOverview:
			first = append(first, s)
		default:
			last = append(last, s)
		}
	}
	if len(names) != len(byName) {
		return
	}
	ordered := make([]Stop, 0, len(names))
	used := map[string]bool{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		s, ok := byName[key]
		if !ok || used[key] {
			return
		}
		used[key] = true
		ordered = append(ordered, s)
	}
	if t.FocusID != "" && len(ordered) > 0 && ordered[0].ElementID != t.FocusID {
		return
	}
	for i := range ordered {
		prev := ""
		if i > 0 {
			prev = ordered[i-1].Name
		}
		if strings.HasSuffix(ordered[i].Why, nextReason) && ordered[i].Why != prev+nextReason {
			ordered[i].Why = ""
		}
	}
	t.Stops = append(append(first, ordered...), last...)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
