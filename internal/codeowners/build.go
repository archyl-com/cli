package codeowners

import (
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/pathmatch"
)

// ElementRef names an element in the output.
type ElementRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Level string `json:"level"`
}

// Entry is one line of the generated block: a folder and who owns it.
type Entry struct {
	// Folder is relative to the repository root, without slashes around it.
	Folder string `json:"folder"`
	// Pattern is the folder as CODEOWNERS writes it.
	Pattern  string       `json:"pattern"`
	Elements []ElementRef `json:"elements"`
	Owners   []string     `json:"owners"`
	// InheritedFrom names the ancestor whose owners the line carries, when
	// none of its elements has owners of its own.
	InheritedFrom string `json:"inheritedFrom,omitempty"`
	// Unmapped are the owners left off the line: teams and people without
	// a handle.
	Unmapped []string `json:"unmapped,omitempty"`
}

// Unmapped is an owner that could not be written as a handle.
type Unmapped struct {
	Kind     string   `json:"kind"` // team or person
	Name     string   `json:"name"`
	Elements []string `json:"elements"`
}

// Result is the generated ownership.
type Result struct {
	Entries []Entry `json:"entries"`
	// Unplaced are elements with owners of their own and no folder in the
	// repository.
	Unplaced []ElementRef `json:"unplaced"`
	Unmapped []Unmapped   `json:"unmapped"`
	// Owned counts the elements that have owners of their own.
	Owned int `json:"owned"`
}

// Input is what Build works from.
type Input struct {
	Graph *graph.Graph
	// Files are the repository's files, relative to its root.
	Files []string
	// Owners are keyed by element ID.
	Owners  map[string]Owners
	Handles Handles
}

// Build places each owned element on its folders and resolves the owners'
// handles. An element without owners of its own takes the owners of its
// closest owned ancestor; on a folder shared with an element that has its
// own, those win, and the line is left out when an enclosing folder already
// gives the same owners.
func Build(in Input) Result {
	dirs := directories(in.Files)
	res := Result{Unplaced: []ElementRef{}, Unmapped: []Unmapped{}}
	unmapped := map[string]*Unmapped{}
	var order []string
	byFolder := map[string]*line{}

	for _, n := range in.Graph.All() {
		if n.Level == "code" || isExternal(n) {
			continue
		}
		owners, source := effectiveOwners(n, in.Owners)
		if source == n {
			res.Owned++
		}
		if owners.Empty() {
			continue
		}
		folders := elementFolders(n, in.Files, dirs)
		if len(folders) == 0 {
			if source == n {
				res.Unplaced = append(res.Unplaced, ref(n))
			}
			continue
		}
		handles, missing := resolve(owners, in.Handles)
		for _, m := range missing {
			noteUnmapped(unmapped, &order, m, n.Name)
		}
		for _, folder := range folders {
			l := byFolder[folder]
			if l == nil {
				l = &line{folder: folder}
				byFolder[folder] = l
			}
			l.add(n, source, handles, names(missing))
		}
	}

	entries := make([]Entry, 0, len(byFolder))
	for _, l := range byFolder {
		entries = append(entries, l.entry())
	}
	res.Entries = prune(sortEntries(entries))
	for _, key := range order {
		res.Unmapped = append(res.Unmapped, *unmapped[key])
	}
	return res
}

// line gathers the elements placed on one folder, keeping the owners they
// have apart from those they inherit.
type line struct {
	folder        string
	elements      []ElementRef
	own           bool
	ownHandles    []string
	ownMissing    []string
	inherited     []string
	inhMissing    []string
	inheritedFrom string
}

func (l *line) add(
	n *graph.Node,
	source *graph.Node,
	handles []string,
	missing []string,
) {
	l.elements = append(l.elements, ref(n))
	if source == n {
		l.own = true
		l.ownHandles = appendUnique(l.ownHandles, handles...)
		l.ownMissing = appendUnique(l.ownMissing, missing...)
		return
	}
	l.inherited = appendUnique(l.inherited, handles...)
	l.inhMissing = appendUnique(l.inhMissing, missing...)
	if l.inheritedFrom == "" {
		l.inheritedFrom = source.Name
	}
}

func (l *line) entry() Entry {
	e := Entry{Folder: l.folder, Pattern: Pattern(l.folder), Elements: l.elements}
	if l.own {
		e.Owners, e.Unmapped = l.ownHandles, l.ownMissing
		return e
	}
	e.Owners, e.Unmapped, e.InheritedFrom = l.inherited, l.inhMissing, l.inheritedFrom
	return e
}

// effectiveOwners are the element's own owners, else those of its closest
// owned ancestor, with the element they come from.
func effectiveOwners(n *graph.Node, all map[string]Owners) (Owners, *graph.Node) {
	for cur := n; cur != nil; cur = cur.Parent {
		if o := all[cur.ID]; !o.Empty() {
			return o, cur
		}
	}
	return Owners{}, nil
}

// isExternal reports whether the element or its system lives outside the
// organization, and so outside the repository.
func isExternal(n *graph.Node) bool {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.External {
			return true
		}
	}
	return false
}

// missingOwner is an owner without a handle.
type missingOwner struct {
	kind, name string
}

func names(missing []missingOwner) []string {
	out := make([]string, 0, len(missing))
	for _, m := range missing {
		out = append(out, m.name)
	}
	return out
}

// resolve turns owners into handles, teams first since they outlive people.
func resolve(o Owners, h Handles) ([]string, []missingOwner) {
	var handles []string
	var missing []missingOwner
	for _, t := range o.Teams {
		if handle, ok := h.Team(t); ok && validHandle(handle) {
			handles = appendUnique(handles, handle)
			continue
		}
		missing = append(missing, missingOwner{kind: "team", name: t.Name})
	}
	for _, p := range o.People {
		if handle, ok := h.Person(p); ok && validHandle(handle) {
			handles = appendUnique(handles, handle)
			continue
		}
		missing = append(missing, missingOwner{kind: "person", name: firstNonEmpty(p.Name, p.Email)})
	}
	return handles, missing
}

func noteUnmapped(
	into map[string]*Unmapped,
	order *[]string,
	m missingOwner,
	element string,
) {
	key := m.kind + "\x00" + m.name
	u := into[key]
	if u == nil {
		u = &Unmapped{Kind: m.kind, Name: m.name}
		into[key] = u
		*order = append(*order, key)
	}
	u.Elements = appendUnique(u.Elements, element)
}

// elementFolders are the folders of the repository that belong to an element: the
// ones holding the files the model records for it and its code, else the
// folder named after it, else, for a container or system, the deepest
// folder holding everything its parts record. A folder must hold at least
// one of files; the repository root never qualifies.
func elementFolders(
	n *graph.Node,
	files []string,
	dirs map[string]bool,
) []string {
	if recorded := recordedFolders(n, false, dirs); len(recorded) > 0 {
		return recorded
	}
	if folder := pathmatch.Folder(n.Name, files); folder != "" {
		return []string{folder}
	}
	if n.Level == "component" {
		return nil
	}
	if common := commonFolder(recordedFolders(n, true, dirs)); common != "" {
		return []string{common}
	}
	return nil
}

// recordedFolders are the folders of the paths recorded on n and its code
// elements, or on everything inside n when deep is set, keeping only the
// outermost of nested ones.
func recordedFolders(
	n *graph.Node,
	deep bool,
	dirs map[string]bool,
) []string {
	var found []string
	var walk func(m *graph.Node)
	walk = func(m *graph.Node) {
		if dir := folderOf(m.FilePath); dir != "" && dirs[dir] {
			found = append(found, dir)
		}
		for _, c := range m.Children {
			if deep || c.Level == "code" {
				walk(c)
			}
		}
	}
	walk(n)
	return outermost(found)
}

// folderOf is the folder of a recorded file path, "" at the root.
func folderOf(file string) string {
	file = strings.TrimSpace(strings.ReplaceAll(file, `\`, "/"))
	if file == "" {
		return ""
	}
	dir := path.Clean(path.Dir(strings.TrimPrefix(file, "/")))
	if dir == "." || dir == "/" || strings.HasPrefix(dir, "..") {
		return ""
	}
	return strings.TrimPrefix(dir, "./")
}

// outermost drops duplicates and folders nested in another one of the set.
func outermost(folders []string) []string {
	sort.Slice(folders, func(i, j int) bool {
		return depth(folders[i]) < depth(folders[j]) || depth(folders[i]) == depth(folders[j]) && folders[i] < folders[j]
	})
	var out []string
	for _, f := range folders {
		if !slices.ContainsFunc(out, func(kept string) bool { return within(f, kept) }) {
			out = append(out, f)
		}
	}
	return out
}

// commonFolder is the deepest folder holding all of folders, "" for none or
// the root.
func commonFolder(folders []string) string {
	if len(folders) == 0 {
		return ""
	}
	common := strings.Split(folders[0], "/")
	for _, f := range folders[1:] {
		segs := strings.Split(f, "/")
		i := 0
		for i < len(common) && i < len(segs) && common[i] == segs[i] {
			i++
		}
		common = common[:i]
	}
	return strings.Join(common, "/")
}

// directories are every folder that holds a file, at any depth.
func directories(files []string) map[string]bool {
	dirs := map[string]bool{}
	for _, f := range files {
		for dir := path.Dir(f); dir != "." && dir != "/" && !dirs[dir]; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	return dirs
}

// sortEntries orders lines shallow first: CODEOWNERS applies the last
// matching line, so a deeper folder must come after the one holding it.
func sortEntries(out []Entry) []Entry {
	sort.Slice(out, func(i, j int) bool {
		di, dj := depth(out[i].Folder), depth(out[j].Folder)
		if di != dj {
			return di < dj
		}
		return out[i].Folder < out[j].Folder
	})
	return out
}

// prune drops inherited lines that say what an enclosing line already says,
// and lines left without any handle: an empty CODEOWNERS line would clear
// the owners an enclosing line gives.
func prune(entries []Entry) []Entry {
	var kept []Entry
	for _, e := range entries {
		if len(e.Owners) == 0 {
			continue
		}
		if e.InheritedFrom != "" {
			if parent := enclosing(kept, e.Folder); parent != nil && sameOwners(parent.Owners, e.Owners) {
				continue
			}
		}
		kept = append(kept, e)
	}
	return kept
}

// enclosing is the deepest entry whose folder holds folder.
func enclosing(entries []Entry, folder string) *Entry {
	for i := len(entries) - 1; i >= 0; i-- {
		if within(folder, entries[i].Folder) {
			return &entries[i]
		}
	}
	return nil
}

func sameOwners(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, s := range a {
		if !slices.Contains(b, s) {
			return false
		}
	}
	return true
}

// within reports whether folder is parent or inside it.
func within(folder, parent string) bool {
	return folder == parent || strings.HasPrefix(folder, parent+"/")
}

func depth(folder string) int {
	return strings.Count(folder, "/")
}

func ref(n *graph.Node) ElementRef {
	return ElementRef{ID: n.ID, Name: n.Name, Level: n.Level}
}

func appendUnique(list []string, items ...string) []string {
	for _, item := range items {
		if !slices.Contains(list, item) {
			list = append(list, item)
		}
	}
	return list
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
