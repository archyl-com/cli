package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

// ExplorerOptions configures the model explorer.
type ExplorerOptions struct {
	Project string
	Graph   *graph.Graph
	Theme   ui.Theme
	// Focus selects this element on open, expanding its ancestors.
	Focus string
	// Link returns the web URL of an element; empty disables "open".
	Link func(*graph.Node) string
	// Open opens a URL in the browser.
	Open func(string) error
	// Copy puts text on the clipboard.
	Copy func(string) error
}

type row struct {
	node    *graph.Node
	depth   int
	matched []int // rune indexes matched by the filter
}

// Explorer is the full-screen C4 model browser behind `archyl map`.
type Explorer struct {
	opts     ExplorerOptions
	s        styles
	expanded map[string]bool
	rows     []row
	cursor   int
	offset   int
	width    int
	height   int
	filter   textinput.Model
	filterOn bool
	status   string
	diag     diagramPane
}

// NewExplorer opens on the systems, the first one expanded.
func NewExplorer(opts ExplorerOptions) *Explorer {
	e := &Explorer{opts: opts, s: newStyles(opts.Theme), expanded: map[string]bool{}}
	e.filter = e.s.input("filter elements by name")
	if len(opts.Graph.Roots) == 1 {
		e.expanded[opts.Graph.Roots[0].ID] = true
	}
	e.rebuild()
	if n := opts.Graph.Node(opts.Focus); n != nil {
		e.reveal(n)
	}
	return e
}

// GraphUpdatedMsg swaps in a newer model, such as the API's answer to a view
// opened from the cache. Expanded elements and the selection carry over.
type GraphUpdatedMsg struct {
	Graph *graph.Graph
}

// Init implements tea.Model.
func (e *Explorer) Init() tea.Cmd {
	return nil
}

// rebuild recomputes the visible rows from the tree or the filter.
func (e *Explorer) rebuild() {
	query := strings.TrimSpace(e.filter.Value())
	if e.filterOn && query != "" {
		all := e.opts.Graph.All()
		names := make([]string, len(all))
		for i, n := range all {
			names[i] = n.Name
		}
		e.rows = e.rows[:0]
		for _, m := range fuzzy.Find(query, names) {
			e.rows = append(e.rows, row{node: all[m.Index], matched: m.MatchedIndexes})
		}
	} else {
		e.rows = e.rows[:0]
		var walk func(nodes []*graph.Node, depth int)
		walk = func(nodes []*graph.Node, depth int) {
			for _, n := range nodes {
				e.rows = append(e.rows, row{node: n, depth: depth})
				if e.expanded[n.ID] {
					walk(n.Children, depth+1)
				}
			}
		}
		walk(e.opts.Graph.Roots, 0)
	}
	e.cursor = min(max(e.cursor, 0), max(len(e.rows)-1, 0))
}

// reveal expands n's ancestors and puts the cursor on it.
func (e *Explorer) reveal(n *graph.Node) {
	for p := n.Parent; p != nil; p = p.Parent {
		e.expanded[p.ID] = true
	}
	e.filterOn = false
	e.filter.SetValue("")
	e.filter.Blur()
	e.rebuild()
	for i, r := range e.rows {
		if r.node == n {
			e.cursor = i
			break
		}
	}
}

func (e *Explorer) swapGraph(g *graph.Graph) {
	selected := ""
	if n := e.current(); n != nil {
		selected = n.ID
	}
	e.opts.Graph = g
	e.rebuild()
	for i, r := range e.rows {
		if r.node.ID == selected {
			e.cursor = i
			break
		}
	}
	e.status = "model updated"
	if e.diag.on {
		e.refreshDiagram()
	}
}

func (e *Explorer) current() *graph.Node {
	if e.cursor < 0 || e.cursor >= len(e.rows) {
		return nil
	}
	return e.rows[e.cursor].node
}

// Update implements tea.Model.
func (e *Explorer) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		e.width, e.height = msg.Width, msg.Height
		return e, nil
	case GraphUpdatedMsg:
		e.swapGraph(msg.Graph)
		return e, nil
	case tea.KeyPressMsg:
		if e.diag.on {
			return e.updateDiagram(msg)
		}
		if e.filterOn {
			return e.updateFilter(msg)
		}
		return e.updateTree(msg)
	}
	return e, nil
}

func (e *Explorer) updateFilter(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		e.filterOn = false
		e.filter.SetValue("")
		e.filter.Blur()
		e.rebuild()
		return e, nil
	case "enter":
		if n := e.current(); n != nil {
			e.reveal(n)
		}
		return e, nil
	case "up", "ctrl+p":
		e.move(-1)
		return e, nil
	case "down", "ctrl+n":
		e.move(1)
		return e, nil
	case "ctrl+c":
		return e, tea.Quit
	}
	var cmd tea.Cmd
	e.filter, cmd = e.filter.Update(msg)
	e.cursor = 0
	e.rebuild()
	return e, cmd
}

func (e *Explorer) updateTree(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	e.status = ""
	n := e.current()
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return e, tea.Quit
	case "up", "k":
		e.move(-1)
	case "down", "j":
		e.move(1)
	case "pgup", "ctrl+u":
		e.move(-e.listHeight() / 2)
	case "pgdown", "ctrl+d":
		e.move(e.listHeight() / 2)
	case "home", "g":
		e.cursor = 0
	case "end", "G":
		e.cursor = len(e.rows) - 1
	case "right", "l", "enter":
		if n != nil && len(n.Children) > 0 {
			if e.expanded[n.ID] {
				e.move(1)
			} else {
				e.expanded[n.ID] = true
				e.rebuild()
			}
		}
	case "space":
		if n != nil && len(n.Children) > 0 {
			e.expanded[n.ID] = !e.expanded[n.ID]
			e.rebuild()
		}
	case "left", "h":
		if n == nil {
			break
		}
		if e.expanded[n.ID] {
			e.expanded[n.ID] = false
			e.rebuild()
		} else if n.Parent != nil {
			e.reveal(n.Parent)
		}
	case "E":
		for _, node := range e.opts.Graph.All() {
			if len(node.Children) > 0 {
				e.expanded[node.ID] = true
			}
		}
		e.rebuild()
	case "C":
		e.expanded = map[string]bool{}
		e.rebuild()
	case "/":
		e.filterOn = true
		return e, e.filter.Focus()
	case "d":
		e.openDiagram()
	case "f":
		e.openFocus()
	case "u":
		// Jump to the first element this one uses.
		if n != nil {
			if out := e.opts.Graph.Outgoing(n.ID); len(out) > 0 {
				e.reveal(out[0].Other)
			}
		}
	case "b":
		// Jump back up to the first element using this one.
		if n != nil {
			if in := e.opts.Graph.Incoming(n.ID); len(in) > 0 {
				e.reveal(in[0].Other)
			}
		}
	case "o":
		if n != nil && e.opts.Link != nil && e.opts.Open != nil {
			if err := e.opts.Open(e.opts.Link(n)); err != nil {
				e.status = "could not open the browser"
			} else {
				e.status = "opened " + n.Name + " in the browser"
			}
		}
	case "y":
		if n != nil && e.opts.Copy != nil {
			if err := e.opts.Copy(n.ID); err == nil {
				e.status = "copied the ID of " + n.Name
			}
		}
	}
	return e, nil
}

func (e *Explorer) move(delta int) {
	e.cursor = min(max(e.cursor+delta, 0), max(len(e.rows)-1, 0))
}

func (e *Explorer) listHeight() int {
	return max(e.height-4, 3)
}

// View implements tea.Model.
func (e *Explorer) View() tea.View {
	if e.width == 0 {
		return tea.NewView("")
	}
	header := e.header()
	footer := e.footer()
	bodyHeight := max(e.height-lipgloss.Height(header)-lipgloss.Height(footer), 5)

	var body string
	if e.diag.on {
		body = e.diagramBody(e.width, bodyHeight)
	} else {
		leftWidth := min(max(e.width*2/5, 30), e.width-30)
		rightWidth := e.width - leftWidth
		body = lipgloss.JoinHorizontal(lipgloss.Top, e.treePane(leftWidth, bodyHeight), e.detailPane(rightWidth, bodyHeight))
	}

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
	v.AltScreen = true
	v.WindowTitle = "archyl map · " + e.opts.Project
	return v
}

func (e *Explorer) header() string {
	counts := e.opts.Graph.Count()
	var parts []string
	for _, level := range []string{"system", "container", "component", "code"} {
		if c := counts[level]; c > 0 {
			parts = append(parts, e.s.glyph(level)+" "+e.s.muted.Render(fmt.Sprint(c)))
		}
	}
	left := e.s.brand.Render("◆ archyl") + e.s.muted.Render("  map  ") + e.s.title.Render(e.opts.Project)
	right := strings.Join(parts, "  ")
	gap := max(e.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (e *Explorer) footer() string {
	if e.filterOn {
		return " " + e.s.hints("↑↓", "move", "enter", "go to", "esc", "clear")
	}
	if e.status != "" {
		return " " + e.s.accent.Render("✓ "+e.status)
	}
	if e.diag.on {
		return " " + e.s.hints("tab", "select", "enter", "drill down", "f", "focus", "⌫", "up a level", "←↑↓→", "scroll", "o", "open in web", "d", "tree", "q", "quit")
	}
	return " " + e.s.hints("↑↓", "move", "→/←", "open/close", "/", "filter", "u/b", "uses/used by", "d", "diagram", "f", "focus", "o", "open in web", "q", "quit")
}

func (e *Explorer) treePane(width, height int) string {
	inner := width - 4
	lines := make([]string, 0, height)
	listHeight := height - 2
	if e.filterOn {
		lines = append(lines, fit(e.filter.View(), inner), e.s.subtle.Render(strings.Repeat("─", inner)))
		listHeight -= 2
	}

	if e.cursor < e.offset {
		e.offset = e.cursor
	}
	if e.cursor >= e.offset+listHeight {
		e.offset = e.cursor - listHeight + 1
	}
	e.offset = min(e.offset, max(len(e.rows)-listHeight, 0))

	if len(e.rows) == 0 {
		lines = append(lines, e.s.muted.Render("nothing matches"))
	}
	for i := e.offset; i < len(e.rows) && i < e.offset+listHeight; i++ {
		r := e.rows[i]
		chevron := "  "
		if len(r.node.Children) > 0 {
			chevron = "▸ "
			if e.expanded[r.node.ID] {
				chevron = "▾ "
			}
		}
		if e.filterOn {
			chevron = ""
		}
		name := highlightRunes(r.node.Name, r.matched, e.s.title, e.s.highlight)
		line := strings.Repeat("  ", r.depth) + e.s.muted.Render(chevron) + e.s.glyph(r.node.Level) + " " + name
		if e.filterOn && r.node.Parent != nil {
			line += "  " + e.s.muted.Render(r.node.Parent.Name)
		}
		if i == e.cursor {
			line = e.s.selected.Render(fit("› "+ansiStrip(line), inner))
		} else {
			line = fit("  "+line, inner)
		}
		lines = append(lines, line)
	}

	return e.s.panel.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (e *Explorer) detailPane(width, height int) string {
	n := e.current()
	inner := width - 4
	if n == nil {
		return e.s.rule.Width(width).Height(height).Render(e.s.muted.Render("Empty model."))
	}
	g := e.opts.Graph
	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }

	var crumbs []string
	for _, p := range n.Path() {
		crumbs = append(crumbs, e.s.glyph(p.Level)+" "+e.s.muted.Render(p.Name))
	}
	add(fit(strings.Join(crumbs, e.s.subtle.Render(" › ")), inner), "")

	title := e.s.glyph(n.Level) + " " + lipgloss.NewStyle().Bold(true).Foreground(e.s.t.Text).Render(n.Name)
	kind := n.Level
	if n.Kind != "" {
		kind = strings.ReplaceAll(n.Kind, "_", " ")
	}
	if n.External {
		kind += " · external"
	}
	add(title+"  "+lipgloss.NewStyle().Foreground(e.s.t.LevelColor(n.Level)).Render(kind), "")

	if d := wrap(n.Description, inner); len(d) > 0 {
		add(d...)
		add("")
	}

	var facts [][2]string
	if n.FilePath != "" {
		facts = append(facts, [2]string{"path", e.s.accent.Render(n.FilePath)})
	}
	if n.Signature != "" {
		facts = append(facts, [2]string{"signature", n.Signature})
	}
	if len(n.Tech) > 0 {
		facts = append(facts, [2]string{"tech", strings.Join(n.Tech, e.s.muted.Render(" · "))})
	}
	if len(n.Tags) > 0 {
		facts = append(facts, [2]string{"tags", e.s.muted.Render("#" + strings.Join(n.Tags, " #"))})
	}
	if len(n.Children) > 0 {
		counts := map[string]int{}
		for _, c := range n.Children {
			counts[c.Level]++
		}
		var parts []string
		for _, level := range []string{"container", "component", "code"} {
			if c := counts[level]; c > 0 {
				parts = append(parts, e.s.glyph(level)+" "+ui.Plural(c, level, pluralLevel(level)))
			}
		}
		facts = append(facts, [2]string{"contains", strings.Join(parts, "  ")})
	}
	for _, f := range facts {
		add(fit(e.s.muted.Render(fmt.Sprintf("%-10s", f[0]))+f[1], inner))
	}
	if len(facts) > 0 {
		add("")
	}

	relations := func(title, arrow string, edges []graph.Edge) {
		if len(edges) == 0 {
			return
		}
		add(e.s.section.Render(fmt.Sprintf("%s  %d", strings.ToUpper(title), len(edges))))
		for i, edge := range edges {
			if i == 6 {
				add(e.s.muted.Render(fmt.Sprintf("  +%d more", len(edges)-6)))
				break
			}
			verb := strings.ReplaceAll(edge.Rel.Type, "_", " ")
			line := "  " + e.s.muted.Render(arrow) + " " + e.s.element(edge.Other.Level, edge.Other.Name) + "  " + e.s.muted.Render(verb)
			if edge.Rel.Description != "" {
				line += e.s.muted.Render(" · " + edge.Rel.Description)
			}
			add(fit(line, inner))
		}
		add("")
	}
	relations("uses", "→", g.Outgoing(n.ID))
	relations("used by", "←", g.Incoming(n.ID))

	if hops := g.Dependents(n.ID, 3); len(hops) > 0 {
		far := 0
		for _, h := range hops {
			far = max(far, h.Distance)
		}
		add(e.s.section.Render("BLAST RADIUS"))
		add(fit("  "+lipgloss.NewStyle().Foreground(e.s.t.Warning).Bold(true).Render(fmt.Sprint(len(hops)))+
			e.s.muted.Render(fmt.Sprintf(" %s depend on it, up to %d hops away", pluralWord(len(hops), "element", "elements"), far)), inner))
	}

	if len(lines) > height-2 {
		lines = append(lines[:height-3], e.s.muted.Render("…"))
	}
	return e.s.rule.Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func pluralLevel(level string) string {
	if level == "code" {
		return "code elements"
	}
	return level + "s"
}

func pluralWord(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
