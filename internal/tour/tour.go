// Package tour plans a guided walk through a codebase from its C4 model: the
// big picture, then the systems or containers a newcomer should meet, in the
// order work flows through them, each with the files to read first. Shared
// infrastructure (queues, caches, databases, storage) is context, never a
// stop. An AI narration can add the story on top; see Narrate.
package tour

import (
	"sort"
	"strconv"
	"strings"

	"github.com/archyl-com/cli/internal/graph"
)

// Kinds of stop.
const (
	KindOverview = "overview"
	KindElement  = "element"
	KindRecap    = "recap"
)

// DefaultStops is how many elements a tour visits unless told otherwise.
const DefaultStops = 6

// maxLinks caps the relationships listed on a stop.
const maxLinks = 6

// maxKeyParts caps the parts a stop names as the ones to know.
const maxKeyParts = 3

// Tour is the planned walk.
type Tour struct {
	Project string `json:"project"`
	// FocusID is the element the tour is centred on, empty for the whole
	// model.
	FocusID string `json:"focusId,omitempty"`
	Stops   []Stop `json:"stops"`
	// Trace is a path through the stops to follow in the code, from where
	// work comes in.
	Trace []string `json:"trace,omitempty"`
	// TraceTask is the narrated version of the trace: what to follow and
	// what to look for.
	TraceTask string `json:"traceTask,omitempty"`
	// Narrated is set when an AI wrote the story parts.
	Narrated bool `json:"narrated"`
}

// Stop is one page of the tour.
type Stop struct {
	Kind        string   `json:"kind"`
	ElementID   string   `json:"elementId,omitempty"`
	Name        string   `json:"name"`
	Level       string   `json:"level,omitempty"`
	Type        string   `json:"type,omitempty"`
	Path        string   `json:"path,omitempty"`
	Description string   `json:"description,omitempty"`
	Tech        []string `json:"technologies,omitempty"`
	// Story is the narrated role of the stop in the flows.
	Story string `json:"story,omitempty"`
	// Why says why the stop is on the tour, from the model alone.
	Why        string `json:"why,omitempty"`
	Uses       []Link `json:"uses,omitempty"`
	UsesMore   int    `json:"usesMore,omitempty"`
	UsedBy     []Link `json:"usedBy,omitempty"`
	UsedByMore int    `json:"usedByMore,omitempty"`
	// Infrastructure names the shared infrastructure the stop relies on.
	Infrastructure []string `json:"infrastructure,omitempty"`
	// KeyParts are the parts inside worth knowing first.
	KeyParts []Part `json:"keyParts,omitempty"`
	// More counts the parts inside left out of KeyParts.
	More int `json:"more,omitempty"`

	Files     []File     `json:"startHere,omitempty"`
	Owners    []string   `json:"owners,omitempty"`
	Decisions []Decision `json:"decisions,omitempty"`
	Notes     []Note     `json:"notes,omitempty"`
	Rules     []Rule     `json:"rules,omitempty"`

	// Overview only.
	Systems   []string       `json:"systems,omitempty"`
	People    []string       `json:"people,omitempty"`
	Externals []string       `json:"externals,omitempty"`
	Flows     []string       `json:"flows,omitempty"`
	Counts    map[string]int `json:"counts,omitempty"`
}

// Part is an element inside a stop.
type Part struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// Link is a relationship seen from a stop.
type Link struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
}

// File is a file to read first.
type File struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Decision is an ADR that applies to a stop.
type Decision struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Note is something people or agents remembered about a stop.
type Note struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Rule is a conformance rule aimed at a stop.
type Rule struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
}

// Options shape a plan.
type Options struct {
	Project string
	// Focus narrows the tour to one element and what it holds.
	Focus *graph.Node
	// MaxStops caps the element stops; zero means DefaultStops.
	MaxStops int
}

// infraTypes are the element types that carry work rather than do it.
var infraTypes = map[string]bool{
	"message_queue": true, "queue": true, "cache": true, "database": true, "datastore": true,
	"file_storage": true, "storage": true, "infrastructure": true, "library": true,
}

// Infra reports whether an element is shared infrastructure or tooling: a
// queue, cache, database, storage or library, or a system made only of them.
func Infra(n *graph.Node) bool {
	if n.Level == "system" {
		if n.External || isPerson(n) || len(n.Children) == 0 {
			return false
		}
		for _, c := range n.Children {
			if !Infra(c) {
				return false
			}
		}
		return true
	}
	return infraTypes[strings.ToLower(n.Kind)]
}

func isPerson(n *graph.Node) bool {
	return strings.EqualFold(n.Kind, "person")
}

// core are the systems the tour is about: yours, neither people, outside
// systems nor shared infrastructure.
func core(g *graph.Graph) []*graph.Node {
	var out []*graph.Node
	for _, sys := range g.Roots {
		if !sys.External && !isPerson(sys) && !Infra(sys) {
			out = append(out, sys)
		}
	}
	return out
}

// Plan picks the stops and their order. It reads only the model, so the same
// model always gives the same tour.
func Plan(g *graph.Graph, opts Options) (*Tour, []*graph.Node) {
	limit := opts.MaxStops
	if limit <= 0 {
		limit = DefaultStops
	}
	t := &Tour{Project: opts.Project}
	if opts.Focus != nil {
		t.FocusID = opts.Focus.ID
	}

	cands := candidates(g, opts.Focus)
	net := newNetwork(g, cands)
	picked := pick(cands, net, opts.Focus, limit)
	ordered := order(picked, net, opts.Focus)

	t.Stops = append(t.Stops, overview(g, opts))
	for i, n := range ordered {
		var prev *graph.Node
		if i > 0 {
			prev = ordered[i-1]
		}
		t.Stops = append(t.Stops, elementStop(g, n, net, prev, opts.Focus))
	}
	t.Trace = trace(ordered, net)
	t.Stops = append(t.Stops, Stop{Kind: KindRecap, Name: "Recap"})
	return t, ordered
}

// candidates are the elements a tour may visit. With several systems of
// your own, the stops are the systems; with one, its containers; for a
// focus, what it holds. Shared infrastructure is left out wherever there is
// something else to see.
func candidates(g *graph.Graph, focus *graph.Node) []*graph.Node {
	if focus != nil {
		switch focus.Level {
		case "system":
			if parts := work(focus.Children); len(parts) > 0 {
				return parts
			}
			if len(focus.Children) > 0 {
				return focus.Children
			}
		case "container", "component":
			return append([]*graph.Node{focus}, work(focus.Children)...)
		}
		return []*graph.Node{focus}
	}
	systems := core(g)
	switch len(systems) {
	case 0:
		return g.Roots
	case 1:
		if parts := work(systems[0].Children); len(parts) > 0 {
			return parts
		}
	}
	return systems
}

// work drops shared infrastructure from a list of elements.
func work(list []*graph.Node) []*graph.Node {
	var out []*graph.Node
	for _, n := range list {
		if !Infra(n) {
			out = append(out, n)
		}
	}
	return out
}

// network is the model's relationships lifted onto the candidates: an
// element stands for everything inside it.
type network struct {
	in       map[*graph.Node]bool
	out      map[*graph.Node][]edge
	incoming map[*graph.Node][]edge
	// callers are the people and outside systems that reach a candidate.
	callers map[*graph.Node][]*graph.Node
	// infra is the shared infrastructure each candidate relies on, kept out
	// of its links.
	infra map[*graph.Node][]string
}

type edge struct {
	other *graph.Node
	label string
}

func newNetwork(g *graph.Graph, cands []*graph.Node) *network {
	net := &network{
		in:       map[*graph.Node]bool{},
		out:      map[*graph.Node][]edge{},
		incoming: map[*graph.Node][]edge{},
		callers:  map[*graph.Node][]*graph.Node{},
		infra:    map[*graph.Node][]string{},
	}
	for _, c := range cands {
		net.in[c] = true
	}
	rep := func(n *graph.Node) *graph.Node {
		for cur := n; cur != nil; cur = cur.Parent {
			if net.in[cur] {
				return cur
			}
		}
		return nil
	}
	seen := map[[3]any]bool{}
	link := func(from, to *graph.Node, lbl string, outward bool) {
		key := [3]any{from, to, outward}
		if seen[key] {
			return
		}
		seen[key] = true
		if outward {
			net.out[from] = append(net.out[from], edge{to, lbl})
		} else {
			net.incoming[to] = append(net.incoming[to], edge{from, lbl})
		}
	}
	for _, n := range g.All() {
		for _, e := range g.Outgoing(n.ID) {
			src, dst := rep(n), rep(e.Other)
			lbl := label(e.Rel.Description, e.Rel.Type)
			switch {
			case src != nil && dst != nil:
				if src != dst {
					link(src, dst, lbl, true)
					link(src, dst, lbl, false)
				}
			case src != nil:
				// What a stop relies on outside the tour, listed but not
				// walked; infrastructure goes on a line of its own.
				if name := infraName(e.Other); name != "" {
					if !containsString(net.infra[src], name) {
						net.infra[src] = append(net.infra[src], name)
					}
					continue
				}
				link(src, divergence(e.Other, src), lbl, true)
			case dst != nil:
				if infraName(n) != "" {
					continue
				}
				caller := divergence(n, dst)
				link(caller, dst, lbl, false)
				if !Infra(caller) && !slicesContains(net.callers[dst], caller) {
					net.callers[dst] = append(net.callers[dst], caller)
				}
			}
		}
	}
	return net
}

// divergence is the element n is seen as from other: its ancestor just below
// what the two share, so a sibling container stays a container and another
// system stays a system.
func divergence(n, other *graph.Node) *graph.Node {
	np, op := n.Path(), other.Path()
	i := 0
	for i < len(np)-1 && i < len(op) && np[i] == op[i] {
		i++
	}
	return np[i]
}

// infraName names the shared infrastructure n belongs to: the infra
// container holding it, or n itself; "" when n is not infrastructure.
func infraName(n *graph.Node) string {
	for _, a := range n.Path() {
		if a.Level != "system" && Infra(a) {
			return a.Name
		}
	}
	if Infra(n) {
		return n.Name
	}
	return ""
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func slicesContains(list []*graph.Node, n *graph.Node) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
}

func label(description, relType string) string {
	d := strings.Join(strings.Fields(description), " ")
	if d != "" && len([]rune(d)) <= 40 {
		return d
	}
	return strings.ReplaceAll(relType, "_", " ")
}

// weight ranks how much a newcomer needs an element: how connected it is to
// other stops and to the outside, then how much it holds.
func (net *network) weight(n *graph.Node) int {
	w := min(len(n.Children), 5)
	for _, e := range net.out[n] {
		if !Infra(e.other) {
			w += 2
		}
	}
	for _, e := range net.incoming[n] {
		if !Infra(e.other) {
			w += 2
		}
	}
	return w
}

// entryScore ranks where work comes in: outside systems sending work count
// more than people, who often only look at dashboards.
func (net *network) entryScore(n *graph.Node) int {
	score := 0
	for _, c := range net.callers[n] {
		if isPerson(c) {
			score++
		} else {
			score += 3
		}
	}
	return score
}

// source is the caller a trace starts from: an outside system if one sends
// work in, else a person.
func (net *network) source(n *graph.Node) *graph.Node {
	var person *graph.Node
	for _, c := range net.callers[n] {
		if !isPerson(c) {
			return c
		}
		if person == nil {
			person = c
		}
	}
	return person
}

func (net *network) internalOut(n *graph.Node) []*graph.Node {
	var out []*graph.Node
	for _, e := range net.out[n] {
		if net.in[e.other] {
			out = append(out, e.other)
		}
	}
	return out
}

// calledInside reports whether another kept element calls n.
func (net *network) calledInside(n *graph.Node, keep map[*graph.Node]bool) bool {
	for _, e := range net.incoming[n] {
		if keep[e.other] && e.other != n {
			return true
		}
	}
	return false
}

// pick keeps the focus and the heaviest candidates.
func pick(cands []*graph.Node, net *network, focus *graph.Node, limit int) []*graph.Node {
	ranked := append([]*graph.Node(nil), cands...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if (ranked[i] == focus) != (ranked[j] == focus) {
			return ranked[i] == focus
		}
		return net.weight(ranked[i]) > net.weight(ranked[j])
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// order walks the picked elements the way work flows: the focus, then where
// work comes in from outside, then along relationships, heaviest first;
// what no path reaches comes last.
func order(picked []*graph.Node, net *network, focus *graph.Node) []*graph.Node {
	keep := map[*graph.Node]bool{}
	for _, n := range picked {
		keep[n] = true
	}
	byWeight := func(list []*graph.Node) {
		sort.SliceStable(list, func(i, j int) bool {
			wi, wj := net.weight(list[i]), net.weight(list[j])
			if wi != wj {
				return wi > wj
			}
			return list[i].Name < list[j].Name
		})
	}

	var starts []*graph.Node
	if focus != nil && keep[focus] {
		starts = append(starts, focus)
	}
	var entries []*graph.Node
	for _, n := range picked {
		if n != focus && net.entryScore(n) > 0 {
			entries = append(entries, n)
		}
	}
	byWeight(entries)
	sort.SliceStable(entries, func(i, j int) bool {
		return net.entryScore(entries[i]) > net.entryScore(entries[j])
	})
	starts = append(starts, entries...)
	if len(entries) == 0 {
		// Nothing comes in from outside: start where no other stop calls.
		var sources []*graph.Node
		for _, n := range picked {
			if n != focus && !net.calledInside(n, keep) {
				sources = append(sources, n)
			}
		}
		byWeight(sources)
		starts = append(starts, sources...)
	}

	var out []*graph.Node
	visited := map[*graph.Node]bool{}
	var walk func(n *graph.Node)
	walk = func(n *graph.Node) {
		if visited[n] || !keep[n] {
			return
		}
		visited[n] = true
		out = append(out, n)
		next := net.internalOut(n)
		byWeight(next)
		for _, m := range next {
			walk(m)
		}
	}
	for _, s := range starts {
		walk(s)
	}
	rest := append([]*graph.Node(nil), picked...)
	byWeight(rest)
	for _, n := range rest {
		walk(n)
	}
	return out
}

func overview(g *graph.Graph, opts Options) Stop {
	s := Stop{Kind: KindOverview, Name: opts.Project, Counts: g.Count()}
	if f := opts.Focus; f != nil {
		s.Name, s.ElementID, s.Level, s.Type = f.Name, f.ID, f.Level, f.Kind
		s.Path, s.Description, s.Tech = f.PathString(), f.Description, f.Tech
		return s
	}
	var descriptions []string
	for _, sys := range g.Roots {
		switch {
		case isPerson(sys):
			s.People = append(s.People, sys.Name)
		case sys.External:
			s.Externals = append(s.Externals, sys.Name)
		case Infra(sys):
			for _, c := range sys.Children {
				s.Infrastructure = append(s.Infrastructure, c.Name)
			}
		default:
			s.Systems = append(s.Systems, sys.Name)
			if sys.Description != "" {
				descriptions = append(descriptions, sys.Description)
			}
		}
	}
	if len(descriptions) == 1 {
		s.Description = descriptions[0]
	}
	return s
}

func elementStop(g *graph.Graph, n *graph.Node, net *network, prev, focus *graph.Node) Stop {
	s := Stop{
		Kind: KindElement, ElementID: n.ID, Name: n.Name, Level: n.Level, Type: n.Kind,
		Path: n.PathString(), Description: n.Description, Tech: n.Tech,
	}
	s.Uses, s.UsesMore = capLinks(net.out[n])
	s.Infrastructure = append([]string(nil), net.infra[n]...)
	sort.Strings(s.Infrastructure)
	s.UsedBy, s.UsedByMore = capLinks(net.incoming[n])
	parts := keyParts(g, n)
	for _, p := range parts {
		s.KeyParts = append(s.KeyParts, Part{ID: p.ID, Name: p.Name, Type: p.Kind, Description: p.Description})
	}
	s.More = len(n.Children) - len(parts)
	s.Why = why(n, net, prev, focus, parts)
	return s
}

// capLinks lists relationships, capped: other parts of the work first, then
// people, then infrastructure.
func capLinks(edges []edge) ([]Link, int) {
	rank := func(n *graph.Node) int {
		switch {
		case Infra(n):
			return 2
		case isPerson(n):
			return 1
		}
		return 0
	}
	sorted := append([]edge(nil), edges...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank(sorted[i].other) < rank(sorted[j].other) })
	var out []Link
	for i, e := range sorted {
		if i == maxLinks {
			return out, len(sorted) - maxLinks
		}
		out = append(out, Link{Name: e.other.Name, Label: e.label})
	}
	return out, 0
}

// keyParts are the most connected parts inside n that do the work.
func keyParts(g *graph.Graph, n *graph.Node) []*graph.Node {
	parts := work(n.Children)
	if len(parts) == 0 {
		parts = append(parts, n.Children...)
	}
	degree := make(map[*graph.Node]int, len(parts))
	for _, p := range parts {
		degree[p] = connections(g, p)
	}
	sort.SliceStable(parts, func(i, j int) bool { return degree[parts[i]] > degree[parts[j]] })
	return parts[:min(len(parts), maxKeyParts)]
}

// connections counts the relationships of n and everything inside it.
func connections(g *graph.Graph, n *graph.Node) int {
	total := len(g.Outgoing(n.ID)) + len(g.Incoming(n.ID))
	for _, c := range n.Children {
		total += connections(g, c)
	}
	return total
}

func why(n *graph.Node, net *network, prev, focus *graph.Node, parts []*graph.Node) string {
	var people, systems []string
	for _, c := range net.callers[n] {
		if isPerson(c) {
			people = append(people, c.Name)
		} else {
			systems = append(systems, c.Name)
		}
	}
	switch {
	case n == focus:
		return "The element this tour is about."
	case len(systems) > 0:
		return "Where work comes in from outside: " + list(systems) + " " + verb(len(systems), "sends", "send") + " work to it."
	case prev != nil && slicesContains(net.internalOut(prev), n):
		return prev.Name + nextReason
	case len(people) > 0:
		return list(people) + " " + verb(len(people), "works", "work") + " with it directly."
	case len(net.incoming[n]) >= 2:
		return itoa(len(net.incoming[n])) + " parts of the system depend on it."
	case len(parts) > 0:
		names := make([]string, len(parts))
		for i, p := range parts {
			names[i] = p.Name
		}
		return "Its main parts are " + list(names) + "."
	}
	return "Part of " + parentName(n) + "."
}

// nextReason ends the reason of a stop reached from the previous one.
const nextReason = " hands work to it next."

func list(names []string) string {
	if len(names) > 4 {
		names = append(names[:3:3], itoa(len(names)-3)+" more")
	}
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func parentName(n *graph.Node) string {
	if n.Parent != nil {
		return n.Parent.Name
	}
	return "the model"
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// trace is a path to follow in the code: from where work comes in, through
// the stops along relationships.
func trace(ordered []*graph.Node, net *network) []string {
	if len(ordered) == 0 {
		return nil
	}
	start := ordered[0]
	var path []string
	if src := net.source(start); src != nil {
		path = append(path, src.Name)
	}
	keep := map[*graph.Node]bool{}
	for _, n := range ordered {
		keep[n] = true
	}
	seen := map[*graph.Node]bool{}
	for cur := start; cur != nil && len(path) < 6; {
		path = append(path, cur.Name)
		seen[cur] = true
		var next *graph.Node
		for _, m := range net.internalOut(cur) {
			if keep[m] && !seen[m] {
				next = m
				break
			}
		}
		cur = next
	}
	if len(path) < 2 {
		return nil
	}
	return path
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
