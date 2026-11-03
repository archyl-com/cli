package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// TriageOptions configures the pending discovery review.
type TriageOptions struct {
	Theme   ui.Theme
	Project string
	Pending []api.PendingDiscovery
	// Approve adds pending elements to the model; Reject discards them.
	Approve func(ctx context.Context, ids []string) error
	Reject  func(ctx context.Context, ids []string) error
}

// Triage is the full-screen review behind `archyl discover review`.
type Triage struct {
	opts     TriageOptions
	s        styles
	items    []TriageItem
	rows     []triageRow
	selected map[string]bool
	cursor   int
	filter   textinput.Model
	filterOn bool
	busy     bool
	status   string
	err      error
	width    int
	height   int
	Approved int
	Rejected int
}

// TriageItem is a pending discovery with its decoded element.
type TriageItem struct {
	api.PendingDiscovery
	Element api.PendingElement
}

type triageRow struct {
	item    *TriageItem
	matched []int
}

type triageDone struct {
	approve bool
	ids     []string
	err     error
}

// triageGroups orders the review: the C4 levels top-down, then the rest.
var triageGroups = []string{"system", "container", "component", "code", "relationship", "overlay", "api_contract", "adr", "doc"}

func triageGroupRank(t string) int {
	for i, g := range triageGroups {
		if g == t {
			return i
		}
	}
	return len(triageGroups)
}

// TriageGroupTitle is the heading of a group of pending elements.
func TriageGroupTitle(elementType string) string {
	switch elementType {
	case "system":
		return "SYSTEMS"
	case "container":
		return "CONTAINERS"
	case "component":
		return "COMPONENTS"
	case "code":
		return "CODE"
	case "relationship":
		return "RELATIONSHIPS"
	case "overlay":
		return "GROUPS"
	case "api_contract":
		return "API CONTRACTS"
	case "adr":
		return "DECISIONS"
	case "doc":
		return "DOCS"
	}
	return strings.ToUpper(strings.ReplaceAll(elementType, "_", " "))
}

// TriageItems decodes and orders pending discoveries for review.
func TriageItems(pending []api.PendingDiscovery) []TriageItem {
	items := make([]TriageItem, 0, len(pending))
	for _, p := range pending {
		items = append(items, TriageItem{PendingDiscovery: p, Element: p.Element()})
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := triageGroupRank(items[i].ElementType), triageGroupRank(items[j].ElementType)
		if ri != rj {
			return ri < rj
		}
		return strings.ToLower(items[i].Name()) < strings.ToLower(items[j].Name())
	})
	return items
}

// Name is how the item reads in a list.
func (it TriageItem) Name() string {
	e := it.Element
	switch {
	case it.ElementType == "relationship":
		return e.SourceName + " → " + e.TargetName
	case e.Name != "":
		return e.Name
	case e.Title != "":
		return e.Title
	}
	return it.ID
}

// Parent is the element the item would sit under.
func (it TriageItem) Parent() string {
	e := it.Element
	switch it.ElementType {
	case "container":
		return e.SystemName
	case "component":
		return e.ContainerName
	case "code":
		return e.ComponentName
	}
	return ""
}

// NewTriage opens the review on the given pending discoveries.
func NewTriage(opts TriageOptions) *Triage {
	m := &Triage{opts: opts, s: newStyles(opts.Theme), items: TriageItems(opts.Pending), selected: map[string]bool{}}
	m.filter = m.s.input("filter by name")
	m.rebuild()
	return m
}

// Remaining is how many items are still pending.
func (m *Triage) Remaining() int {
	return len(m.items)
}

func (m *Triage) rebuild() {
	m.rows = m.rows[:0]
	query := strings.TrimSpace(m.filter.Value())
	if m.filterOn && query != "" {
		names := make([]string, len(m.items))
		for i := range m.items {
			names[i] = m.items[i].Name()
		}
		matches := fuzzy.Find(query, names)
		// Keep the grouping: matches in item order, not by score.
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].Index < matches[j].Index })
		for _, mt := range matches {
			m.rows = append(m.rows, triageRow{item: &m.items[mt.Index], matched: mt.MatchedIndexes})
		}
	} else {
		for i := range m.items {
			m.rows = append(m.rows, triageRow{item: &m.items[i]})
		}
	}
	m.cursor = min(max(m.cursor, 0), max(len(m.rows)-1, 0))
}

func (m *Triage) current() *TriageItem {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].item
}

// Init implements tea.Model.
func (m *Triage) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m *Triage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case triageDone:
		m.finish(msg)
		return m, nil
	case tea.KeyPressMsg:
		if m.filterOn && m.filter.Focused() {
			return m.updateFilter(msg)
		}
		return m.updateList(msg)
	}
	return m, nil
}

func (m *Triage) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterOn = false
		m.filter.SetValue("")
		m.filter.Blur()
		m.rebuild()
		return m, nil
	case "enter", "down", "up":
		// Keep the filter, hand the keys back to the list.
		m.filter.Blur()
		if msg.String() != "enter" {
			return m.updateList(msg)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.rebuild()
	return m, cmd
}

func (m *Triage) updateList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.filterOn {
			m.filterOn = false
			m.filter.SetValue("")
			m.rebuild()
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, max(len(m.rows)-1, 0))
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = max(len(m.rows)-1, 0)
	case "/":
		m.filterOn = true
		m.rebuild()
		return m, m.filter.Focus()
	case "space":
		if it := m.current(); it != nil {
			if m.selected[it.ID] {
				delete(m.selected, it.ID)
			} else {
				m.selected[it.ID] = true
			}
			m.cursor = min(m.cursor+1, max(len(m.rows)-1, 0))
		}
	case "a":
		return m, m.act(true, m.targets())
	case "r":
		return m, m.act(false, m.targets())
	case "A":
		var ids []string
		for _, r := range m.rows {
			ids = append(ids, r.item.ID)
		}
		return m, m.act(true, ids)
	}
	return m, nil
}

// targets is the selection, or the current item when nothing is selected.
func (m *Triage) targets() []string {
	var ids []string
	for _, it := range m.items {
		if m.selected[it.ID] {
			ids = append(ids, it.ID)
		}
	}
	if len(ids) == 0 {
		if it := m.current(); it != nil {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

func (m *Triage) act(approve bool, ids []string) tea.Cmd {
	if m.busy || len(ids) == 0 {
		return nil
	}
	m.busy, m.err = true, nil
	m.status = map[bool]string{true: "Approving ", false: "Rejecting "}[approve] + ui.Plural(len(ids), "element", "elements") + "…"
	call := m.opts.Reject
	if approve {
		call = m.opts.Approve
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return triageDone{approve: approve, ids: ids, err: call(ctx, ids)}
	}
}

func (m *Triage) finish(msg triageDone) {
	m.busy = false
	if msg.err != nil {
		m.err, m.status = msg.err, ""
		return
	}
	done := map[string]bool{}
	for _, id := range msg.ids {
		done[id] = true
		delete(m.selected, id)
	}
	kept := m.items[:0]
	for _, it := range m.items {
		if !done[it.ID] {
			kept = append(kept, it)
		}
	}
	m.items = kept
	if msg.approve {
		m.Approved += len(msg.ids)
		m.status = "✓ Approved " + ui.Plural(len(msg.ids), "element", "elements")
	} else {
		m.Rejected += len(msg.ids)
		m.status = "✓ Rejected " + ui.Plural(len(msg.ids), "element", "elements")
	}
	m.rebuild()
}

// View implements tea.Model.
func (m *Triage) View() tea.View {
	if m.width == 0 {
		return tea.NewView("")
	}
	left := m.s.brand.Render("◆ archyl") + m.s.muted.Render("  review discoveries  ") + m.s.title.Render(m.opts.Project)
	right := m.s.muted.Render(fmt.Sprintf("%d pending", len(m.items)))
	if n := len(m.selected); n > 0 {
		right = m.s.accent.Render(fmt.Sprintf("%d selected", n)) + m.s.muted.Render(" · ") + right
	}
	header := " " + left + strings.Repeat(" ", max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)) + right

	footer := " " + m.s.hints("↑↓", "move", "space", "select", "a", "approve", "r", "reject", "A", "approve all", "/", "filter", "q", "quit")
	switch {
	case m.err != nil:
		footer = " " + lipgloss.NewStyle().Foreground(m.s.t.Danger).Render("✗ "+m.err.Error())
	case m.status != "":
		footer = " " + m.s.accent.Render(m.status) + "   " + footer
	}

	bodyHeight := max(m.height-lipgloss.Height(header)-lipgloss.Height(footer), 6)
	listWidth := min(max(m.width*11/20, 40), m.width-30)
	list := m.s.panel.Width(listWidth).Height(bodyHeight).Render(m.listView(listWidth-4, bodyHeight-2))
	detail := m.s.rule.Width(m.width - listWidth).Height(bodyHeight).Render(m.detailView(m.width-listWidth-5, bodyHeight-2))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, lipgloss.JoinHorizontal(lipgloss.Top, list, detail), footer))
	v.AltScreen = true
	v.WindowTitle = fmt.Sprintf("archyl review · %d pending", len(m.items))
	return v
}

func (m *Triage) listView(width, height int) string {
	var lines []string
	if m.filterOn {
		lines = append(lines, fit(m.filter.View(), width), "")
	}
	if len(m.rows) == 0 {
		if len(m.items) == 0 {
			lines = append(lines, m.s.title.Render("All reviewed."), "", m.s.muted.Render("Press q to leave."))
		} else {
			lines = append(lines, m.s.muted.Render("Nothing matches."))
		}
		return strings.Join(lines, "\n")
	}

	// Render every line, then scroll the window so the cursor stays in view.
	var all []string
	cursorLine, group := 0, ""
	for i, r := range m.rows {
		if r.item.ElementType != group {
			group = r.item.ElementType
			if len(all) > 0 {
				all = append(all, "")
			}
			all = append(all, m.s.section.Render(TriageGroupTitle(group)))
		}
		mark := m.s.subtle.Render("○")
		if m.selected[r.item.ID] {
			mark = m.s.accent.Render("●")
		}
		name := highlightRunes(r.item.Name(), r.matched, m.s.title, m.s.highlight)
		line := mark + " " + m.s.glyph(r.item.ElementType) + " " + name
		if parent := r.item.Parent(); parent != "" {
			line += m.s.muted.Render("  in " + parent)
		}
		if i == m.cursor {
			cursorLine = len(all)
			all = append(all, m.s.selected.Render(fit("› "+ansiStrip(line), width)))
		} else {
			all = append(all, fit("  "+line, width))
		}
	}
	room := max(height-len(lines), 1)
	start := 0
	if cursorLine >= room {
		start = cursorLine - room + 1
	}
	end := min(start+room, len(all))
	return strings.Join(append(lines, all[start:end]...), "\n")
}

func (m *Triage) detailView(width, height int) string {
	it := m.current()
	if it == nil {
		return m.s.muted.Render("Run a discovery with `archyl discover`.")
	}
	e := it.Element
	var lines []string
	add := func(l ...string) { lines = append(lines, l...) }
	add(m.s.element(it.ElementType, it.Name()))
	kind := strings.ReplaceAll(it.ElementType, "_", " ")
	if e.Type != "" && e.Type != it.ElementType {
		kind += " · " + strings.ReplaceAll(e.Type, "_", " ")
	}
	add(m.s.muted.Render(kind), "")

	field := func(label, value string) {
		if value != "" {
			add(fit(m.s.muted.Render(fmt.Sprintf("%-11s", label))+value, width))
		}
	}
	field("in", it.Parent())
	field("technology", e.Technology)
	field("language", e.Language)
	field("file", e.FilePath)
	field("kind", e.RelationshipType)
	field("status", e.Status)
	if e.Signature != "" {
		add("", m.s.section.Render("SIGNATURE"))
		add(wrap(e.Signature, width)...)
	}

	text := e.Description
	if text == "" {
		text = firstNonEmpty(e.Decision, e.Context, e.Content)
	}
	add("")
	if d := wrap(text, width); len(d) > 0 {
		add(d...)
	} else {
		add(m.s.muted.Render("No description."))
	}
	if len(lines) > height {
		lines = append(lines[:max(height-1, 0)], m.s.muted.Render("…"))
	}
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
