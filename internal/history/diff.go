// Package history compares two states of a C4 model: what was added,
// removed and modified between them, and one model holding both sides for a
// diagram with the changes marked. It also resolves the points a range of
// history names: versions, releases, dates and durations.
package history

import (
	"slices"
	"sort"
	"strings"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// Status is how an element or a relationship changed.
type Status string

// Statuses of a change.
const (
	Added    Status = "added"
	Removed  Status = "removed"
	Modified Status = "modified"
)

// Fields a modification names.
const (
	FieldName        = "name"
	FieldParent      = "parent"
	FieldDescription = "description"
	FieldType        = "type"
	FieldTechnology  = "technology"
	FieldExternal    = "external"
	FieldSource      = "source"
	FieldTarget      = "target"
)

// FieldChange is one property of an element or a relationship that changed.
type FieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// Element is an element added, removed or modified. An element added or
// removed with its parent is counted in the parent's Descendants, not
// listed.
type Element struct {
	Status Status `json:"status"`
	// ID is the element's ID after the change, or before it when removed.
	ID    string `json:"id"`
	Level string `json:"level"`
	Name  string `json:"name"`
	// Path names the element from its system: "Shop / Orders API".
	Path        string        `json:"path"`
	Changes     []FieldChange `json:"changes,omitempty"`
	Descendants int           `json:"descendants,omitempty"`
}

// Relationship is a relationship added, removed or modified.
type Relationship struct {
	Status   Status        `json:"status"`
	ID       string        `json:"id"`
	SourceID string        `json:"sourceId"`
	TargetID string        `json:"targetId"`
	Source   string        `json:"source"`
	Target   string        `json:"target"`
	Label    string        `json:"label,omitempty"`
	Changes  []FieldChange `json:"changes,omitempty"`
	// WithEnd is set on a relationship removed with one of its ends: the
	// element's removal says it already.
	WithEnd bool `json:"withEnd,omitempty"`
}

// Summary counts the changes of a diff. Elements added or removed with
// their parent count once, with the parent.
type Summary struct {
	Added                 int `json:"added"`
	Removed               int `json:"removed"`
	Modified              int `json:"modified"`
	RelationshipsAdded    int `json:"relationshipsAdded"`
	RelationshipsRemoved  int `json:"relationshipsRemoved"`
	RelationshipsModified int `json:"relationshipsModified"`
}

// Options tune a comparison.
type Options struct {
	// Technology compares technologies. Versions record none, so leave it
	// off unless both models come from the live API.
	Technology bool
}

// Diff is what changed from one model to another.
type Diff struct {
	Elements      []Element      `json:"elements"`
	Relationships []Relationship `json:"relationships"`

	before, after *graph.Graph
	// forward maps an element before to its match after, back the reverse.
	forward, back map[string]*graph.Node
	// merged maps an element before to its ID in the scene.
	merged map[string]string
	// relStatus is the change of each relationship after, and gone the
	// relationships before that are no more.
	relStatus map[string]Status
	gone      []api.Relationship
	// kept are the relationships after whose ends are both known.
	kept []api.Relationship
}

// Compare diffs two models. Elements match by ID, then by their path of
// names from the system down; relationships by ID, then by their ends.
func Compare(
	before *api.Model,
	after *api.Model,
	opts Options,
) *Diff {
	d := &Diff{
		before: graph.New(before), after: graph.New(after),
		forward: map[string]*graph.Node{}, back: map[string]*graph.Node{},
		merged: map[string]string{}, relStatus: map[string]Status{},
	}
	d.match()
	d.compareElements(opts)
	d.compareRelationships(before.Relationships, after.Relationships, opts)
	return d
}

// match pairs the elements of both sides: by ID at the same level, then,
// top down, by name under the matched parent.
func (d *Diff) match() {
	for _, a := range d.before.All() {
		if b := d.after.Node(a.ID); b != nil && b.Level == a.Level {
			d.pair(a, b)
		}
	}
	type key struct{ parent, level, name string }
	keyOf := func(n *graph.Node, parent string) key {
		return key{parent, n.Level, normalize(strings.ToLower(n.Name))}
	}
	waiting := map[key][]*graph.Node{}
	for _, a := range d.before.All() {
		if d.forward[a.ID] == nil {
			parent := ""
			if a.Parent != nil {
				parent = a.Parent.ID
			}
			k := keyOf(a, parent)
			waiting[k] = append(waiting[k], a)
		}
	}
	// All lists parents before their children, so a parent is matched
	// before its children look for it.
	for _, b := range d.after.All() {
		if d.back[b.ID] != nil {
			continue
		}
		parent := ""
		if b.Parent != nil {
			a := d.back[b.Parent.ID]
			if a == nil {
				continue
			}
			parent = a.ID
		}
		k := keyOf(b, parent)
		if len(waiting[k]) == 0 {
			continue
		}
		d.pair(waiting[k][0], b)
		waiting[k] = waiting[k][1:]
	}
	for _, a := range d.before.All() {
		switch b := d.forward[a.ID]; {
		case b != nil:
			d.merged[a.ID] = b.ID
		case d.after.Node(a.ID) != nil:
			// The ID was reused at another level: keep the two apart.
			d.merged[a.ID] = "removed:" + a.ID
		default:
			d.merged[a.ID] = a.ID
		}
	}
}

func (d *Diff) pair(a, b *graph.Node) {
	d.forward[a.ID], d.back[b.ID] = b, a
}

// compareElements lists added, removed and modified elements, folding the
// elements added or removed with their parent into it.
func (d *Diff) compareElements(opts Options) {
	added := func(n *graph.Node) bool { return d.back[n.ID] == nil }
	removed := func(n *graph.Node) bool { return d.forward[n.ID] == nil }
	for _, b := range d.after.All() {
		a := d.back[b.ID]
		if a == nil {
			if b.Parent == nil || !added(b.Parent) {
				d.Elements = append(d.Elements, element(Added, b.ID, b, descendants(b, added)))
			}
			continue
		}
		if changes := d.fieldChanges(a, b, opts); len(changes) > 0 {
			e := element(Modified, b.ID, b, 0)
			e.Changes = changes
			d.Elements = append(d.Elements, e)
		}
	}
	for _, a := range d.before.All() {
		if removed(a) && (a.Parent == nil || !removed(a.Parent)) {
			d.Elements = append(d.Elements, element(Removed, d.merged[a.ID], a, descendants(a, removed)))
		}
	}
	sort.SliceStable(d.Elements, func(i, j int) bool {
		a, b := d.Elements[i], d.Elements[j]
		if a.Status != b.Status {
			return statusRank(a.Status) < statusRank(b.Status)
		}
		if ra, rb := graph.LevelRank(a.Level), graph.LevelRank(b.Level); ra != rb {
			return ra < rb
		}
		return a.Path < b.Path
	})
}

func element(
	status Status,
	id string,
	n *graph.Node,
	descendants int,
) Element {
	return Element{Status: status, ID: id, Level: n.Level, Name: n.Name, Path: n.PathString(), Descendants: descendants}
}

// descendants counts the elements under n that same selects, stopping at
// those it does not: a child that moved away is not removed with its parent.
func descendants(n *graph.Node, same func(*graph.Node) bool) int {
	count := 0
	for _, c := range n.Children {
		if same(c) {
			count += 1 + descendants(c, same)
		}
	}
	return count
}

// fieldChanges compares a matched pair.
func (d *Diff) fieldChanges(
	a *graph.Node,
	b *graph.Node,
	opts Options,
) []FieldChange {
	var out []FieldChange
	add := func(field, before, after string) {
		if before != after {
			out = append(out, FieldChange{Field: field, Before: before, After: after})
		}
	}
	add(FieldName, a.Name, b.Name)
	if !d.sameParent(a, b) {
		out = append(out, FieldChange{Field: FieldParent, Before: parentPath(a), After: parentPath(b)})
	}
	add(FieldType, a.Kind, b.Kind)
	if normalize(a.Description) != normalize(b.Description) {
		out = append(out, FieldChange{Field: FieldDescription, Before: normalize(a.Description), After: normalize(b.Description)})
	}
	if a.External != b.External {
		out = append(out, FieldChange{Field: FieldExternal, Before: yesNo(a.External), After: yesNo(b.External)})
	}
	if opts.Technology {
		add(FieldTechnology, techList(a.Tech), techList(b.Tech))
	}
	return out
}

// sameParent reports whether a and b sit under matching parents.
func (d *Diff) sameParent(a, b *graph.Node) bool {
	if a.Parent == nil || b.Parent == nil {
		return a.Parent == nil && b.Parent == nil
	}
	return d.forward[a.Parent.ID] == b.Parent
}

func parentPath(n *graph.Node) string {
	if n.Parent == nil {
		return ""
	}
	return n.Parent.PathString()
}

// compareRelationships lists added, removed and modified relationships.
func (d *Diff) compareRelationships(
	before []api.Relationship,
	after []api.Relationship,
	opts Options,
) {
	before, after = known(d.before, before), known(d.after, after)
	d.kept = after
	matched := make([]bool, len(after))
	pairs := map[int]int{}
	index := map[string]int{}
	for j, r := range after {
		index[r.ID] = j
	}
	for i, r := range before {
		if j, ok := index[r.ID]; ok && !matched[j] {
			pairs[i], matched[j] = j, true
		}
	}
	// Then by ends, the closest first: same type and description, same
	// type, then any.
	sameEnds := func(a, b api.Relationship) bool {
		return d.mergedID(a.SourceID) == b.SourceID && d.mergedID(a.TargetID) == b.TargetID
	}
	tests := []func(a, b api.Relationship) bool{
		func(a, b api.Relationship) bool {
			return sameEnds(a, b) && a.Type == b.Type && normalize(a.Description) == normalize(b.Description)
		},
		func(a, b api.Relationship) bool { return sameEnds(a, b) && a.Type == b.Type },
		sameEnds,
	}
	for _, test := range tests {
		for i, a := range before {
			if _, ok := pairs[i]; ok {
				continue
			}
			for j, b := range after {
				if !matched[j] && test(a, b) {
					pairs[i], matched[j] = j, true
					break
				}
			}
		}
	}

	for j, b := range after {
		if matched[j] {
			continue
		}
		d.relStatus[b.ID] = Added
		d.Relationships = append(d.Relationships, d.relationship(Added, b, d.after, false))
	}
	for i, a := range before {
		j, ok := pairs[i]
		if !ok {
			d.gone = append(d.gone, a)
			withEnd := d.forward[a.SourceID] == nil || d.forward[a.TargetID] == nil
			r := d.relationship(Removed, a, d.before, withEnd)
			r.SourceID, r.TargetID = d.merged[a.SourceID], d.merged[a.TargetID]
			d.Relationships = append(d.Relationships, r)
			continue
		}
		b := after[j]
		if changes := d.relChanges(a, b, opts); len(changes) > 0 {
			d.relStatus[b.ID] = Modified
			r := d.relationship(Modified, b, d.after, false)
			r.Changes = changes
			d.Relationships = append(d.Relationships, r)
		}
	}
	sort.SliceStable(d.Relationships, func(i, j int) bool {
		a, b := d.Relationships[i], d.Relationships[j]
		if a.Status != b.Status {
			return statusRank(a.Status) < statusRank(b.Status)
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Target < b.Target
	})
}

// known keeps the relationships whose ends are both in g.
func known(g *graph.Graph, rels []api.Relationship) []api.Relationship {
	var out []api.Relationship
	for _, r := range rels {
		if g.Node(r.SourceID) != nil && g.Node(r.TargetID) != nil {
			out = append(out, r)
		}
	}
	return out
}

// mergedID is the ID an element before has after the change, or in the
// scene when it was removed.
func (d *Diff) mergedID(id string) string {
	if m, ok := d.merged[id]; ok {
		return m
	}
	return id
}

func (d *Diff) relationship(
	status Status,
	r api.Relationship,
	g *graph.Graph,
	withEnd bool,
) Relationship {
	return Relationship{
		Status: status, ID: r.ID, SourceID: r.SourceID, TargetID: r.TargetID,
		Source: g.Node(r.SourceID).Name, Target: g.Node(r.TargetID).Name,
		Label: label(r), WithEnd: withEnd,
	}
}

// relChanges compares a matched pair of relationships.
func (d *Diff) relChanges(
	a api.Relationship,
	b api.Relationship,
	opts Options,
) []FieldChange {
	var out []FieldChange
	if d.mergedID(a.SourceID) != b.SourceID {
		out = append(out, FieldChange{Field: FieldSource, Before: d.before.Node(a.SourceID).Name, After: d.after.Node(b.SourceID).Name})
	}
	if d.mergedID(a.TargetID) != b.TargetID {
		out = append(out, FieldChange{Field: FieldTarget, Before: d.before.Node(a.TargetID).Name, After: d.after.Node(b.TargetID).Name})
	}
	if a.Type != b.Type {
		out = append(out, FieldChange{Field: FieldType, Before: a.Type, After: b.Type})
	}
	if normalize(a.Description) != normalize(b.Description) {
		out = append(out, FieldChange{Field: FieldDescription, Before: normalize(a.Description), After: normalize(b.Description)})
	}
	if opts.Technology {
		ta, tb := relTech(a), relTech(b)
		if ta != tb {
			out = append(out, FieldChange{Field: FieldTechnology, Before: ta, After: tb})
		}
	}
	return out
}

// Empty reports whether nothing changed.
func (d *Diff) Empty() bool {
	return len(d.Elements) == 0 && len(d.Relationships) == 0
}

// Summary counts the changes.
func (d *Diff) Summary() Summary {
	var s Summary
	for _, e := range d.Elements {
		switch e.Status {
		case Added:
			s.Added++
		case Removed:
			s.Removed++
		case Modified:
			s.Modified++
		}
	}
	for _, r := range d.Relationships {
		switch r.Status {
		case Added:
			s.RelationshipsAdded++
		case Removed:
			s.RelationshipsRemoved++
		case Modified:
			s.RelationshipsModified++
		}
	}
	return s
}

func statusRank(s Status) int {
	switch s {
	case Added:
		return 0
	case Removed:
		return 1
	}
	return 2
}

// label is a relationship in words: its description, else its type.
func label(r api.Relationship) string {
	if d := normalize(r.Description); d != "" {
		return d
	}
	return strings.ReplaceAll(r.Type, "_", " ")
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func techList(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return strings.Join(sorted, ", ")
}

func relTech(r api.Relationship) string {
	names := make([]string, 0, len(r.Technologies))
	for _, t := range r.Technologies {
		names = append(names, t.Name)
	}
	return techList(names)
}
