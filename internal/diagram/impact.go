package diagram

import "github.com/archyl-com/cli/internal/api"

// FromImpact builds the highlight of a located change: the elements its
// files land in at distance 0, their dependents at their hop count.
func FromImpact(r *api.LocateResult) Highlight {
	h := Highlight{}
	if r == nil {
		return h
	}
	mark := func(id string, d int) {
		if cur, ok := h[id]; !ok || d < cur {
			h[id] = d
		}
	}
	for _, f := range r.Files {
		if len(f.Elements) > 0 {
			mark(f.Elements[0].ID, 0)
		}
	}
	if r.Impact != nil {
		for _, e := range r.Impact.Elements {
			mark(e.ID, 0)
			for _, d := range e.Dependents {
				mark(d.ID, max(d.Distance, 1))
			}
		}
	}
	return h
}
