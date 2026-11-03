package diagram

// Change is how a box or an arrow differs from an earlier state of the
// model. The zero value is unchanged.
type Change string

// Changes a box or an arrow can carry.
const (
	Added   Change = "added"
	Removed Change = "removed"
	Changed Change = "changed"
)

// Badge is the one-character sign of a change: "+", "−" or "~".
func (c Change) Badge() string {
	switch c {
	case Added:
		return "+"
	case Removed:
		return "−"
	case Changed:
		return "~"
	}
	return ""
}

// Link is one relationship of the model and its change, by the elements it
// joins.
type Link struct {
	From   string
	To     string
	Change Change
}

// MarkChanges tags the boxes and arrows of v with the changes of the
// elements and relationships they stand for. A box takes its own element's
// change; a box whose element is unchanged but holds changed elements is
// changed. An arrow merging relationships that were all added (or all
// removed) is added (removed); any other mix with a change is changed.
func (v *View) MarkChanges(elements map[string]Change, links []Link) {
	inner := map[string]bool{}
	for id, c := range elements {
		if rep := v.members[id]; rep != "" && rep != id && c != "" {
			inner[rep] = true
		}
	}
	for i := range v.Nodes {
		n := &v.Nodes[i]
		n.Change = elements[n.ID]
		if n.Change == "" && inner[n.ID] {
			n.Change = Changed
		}
	}

	type pair struct{ a, b string }
	key := func(a, b string) pair {
		if a > b {
			a, b = b, a
		}
		return pair{a, b}
	}
	seen := map[pair]map[Change]bool{}
	for _, l := range links {
		a, b := v.members[l.From], v.members[l.To]
		if a == "" || b == "" || a == b {
			continue
		}
		k := key(a, b)
		if seen[k] == nil {
			seen[k] = map[Change]bool{}
		}
		seen[k][l.Change] = true
	}
	for i := range v.Edges {
		v.Edges[i].Change = edgeChange(seen[key(v.Edges[i].From, v.Edges[i].To)])
	}
}

// edgeChange folds the changes of the relationships an arrow merges.
func edgeChange(changes map[Change]bool) Change {
	switch {
	case len(changes) == 1 && changes[Added]:
		return Added
	case len(changes) == 1 && changes[Removed]:
		return Removed
	case len(changes) == 1 && changes[""], len(changes) == 0:
		return ""
	}
	return Changed
}

// Marked reports whether a box or an arrow of v carries a change.
func (v *View) Marked() bool {
	for _, n := range v.Nodes {
		if n.Change != "" {
			return true
		}
	}
	for _, e := range v.Edges {
		if e.Change != "" {
			return true
		}
	}
	return false
}

// markRank orders changes for an arrow crossing another: the stronger
// colour wins the shared cell.
func markRank(c Change) int8 {
	switch c {
	case Added:
		return 3
	case Changed:
		return 2
	case Removed:
		return 1
	}
	return 0
}
