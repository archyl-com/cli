package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/archyl-com/cli/internal/diagram"
)

// diagramPane is the map's diagram mode: the C4 view the selection sits in,
// drawn with box-drawing characters and scrolled when it is bigger than the
// screen.
type diagramPane struct {
	on       bool
	view     *diagram.View
	layout   *diagram.Layout
	order    []int // boxes in reading order
	selected int   // index into order
	x, y     int   // scroll offset
	lines    []string
	viewW    int
	viewH    int
}

// openDiagram shows the view holding the current element, selected.
func (e *Explorer) openDiagram() {
	n := e.current()
	if n == nil {
		return
	}
	e.showView(diagram.For(e.opts.Graph, n.Parent), n.ID)
	e.diag.on = true
}

// openFocus shows the view centred on the current element.
func (e *Explorer) openFocus() {
	n := e.current()
	if n == nil {
		return
	}
	e.showView(diagram.Focus(e.opts.Graph, n, 1), n.ID)
	e.diag.on = true
}

// showView swaps in a view and selects the box standing for element.
func (e *Explorer) showView(v *diagram.View, element string) {
	d := &e.diag
	d.view = v
	d.layout = diagram.ArrangeText(v, diagram.TextBoxMax)
	d.order = make([]int, len(d.layout.Boxes))
	for i := range d.order {
		d.order[i] = i
	}
	boxes := d.layout.Boxes
	sort.SliceStable(d.order, func(i, j int) bool {
		a, b := boxes[d.order[i]], boxes[d.order[j]]
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.X < b.X
	})
	d.selected, d.x, d.y = 0, 0, 0
	rep := v.Rep(element)
	for i, bi := range d.order {
		if boxes[bi].ID == rep {
			d.selected = i
		}
	}
	e.renderDiagram()
	e.revealBox()
}

func (e *Explorer) selectedBox() *diagram.Box {
	d := &e.diag
	if d.layout == nil || len(d.order) == 0 {
		return nil
	}
	return &d.layout.Boxes[d.order[d.selected]]
}

func (e *Explorer) renderDiagram() {
	d := &e.diag
	selected := ""
	if b := e.selectedBox(); b != nil {
		selected = b.ID
	}
	d.lines = strings.Split(diagram.RenderText(d.layout, diagram.TextOptions{
		Theme:     e.opts.Theme,
		Color:     true,
		Selected:  selected,
		Highlight: d.view.Hops(),
	}), "\n")
}

// revealBox scrolls the selected box into view.
func (e *Explorer) revealBox() {
	d := &e.diag
	b := e.selectedBox()
	if b == nil || d.viewW == 0 {
		return
	}
	if b.X < d.x {
		d.x = max(b.X-2, 0)
	}
	if b.X+b.W > d.x+d.viewW {
		d.x = b.X + b.W - d.viewW + 2
	}
	if b.Y < d.y {
		d.y = max(b.Y-1, 0)
	}
	if b.Y+b.H > d.y+d.viewH {
		d.y = b.Y + b.H - d.viewH + 1
	}
	e.clampScroll()
}

func (e *Explorer) clampScroll() {
	d := &e.diag
	d.x = min(max(d.x, 0), max(d.layout.Width-d.viewW, 0))
	d.y = min(max(d.y, 0), max(len(d.lines)-d.viewH, 0))
}

func (e *Explorer) updateDiagram(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	d := &e.diag
	e.status = ""
	switch msg.String() {
	case "q", "ctrl+c":
		return e, tea.Quit
	case "d", "esc":
		d.on = false
		if b := e.selectedBox(); b != nil {
			if n := e.opts.Graph.Node(b.ID); n != nil {
				e.reveal(n)
			}
		}
	case "tab", "n":
		e.moveSelection(1)
	case "shift+tab", "N", "p":
		e.moveSelection(-1)
	case "up", "k":
		d.y--
	case "down", "j":
		d.y++
	case "left", "h":
		d.x -= 4
	case "right", "l":
		d.x += 4
	case "pgup", "ctrl+u":
		d.y -= d.viewH / 2
	case "pgdown", "ctrl+d":
		d.y += d.viewH / 2
	case "enter":
		b := e.selectedBox()
		if b == nil {
			break
		}
		n := e.opts.Graph.Node(b.ID)
		if n == nil || len(n.Children) == 0 || n.Level == "component" || n.Level == "code" {
			e.status = "nothing to open below " + b.Name
			break
		}
		e.showView(diagram.For(e.opts.Graph, n), n.Children[0].ID)
		return e, nil
	case "f":
		b := e.selectedBox()
		if b == nil {
			break
		}
		if n := e.opts.Graph.Node(b.ID); n != nil {
			e.showView(diagram.Focus(e.opts.Graph, n, 1), n.ID)
		}
		return e, nil
	case "backspace", "u":
		if center := e.opts.Graph.Node(d.view.FocusID); center != nil {
			e.showView(diagram.For(e.opts.Graph, center.Parent), center.ID)
			return e, nil
		}
		scope := e.opts.Graph.Node(d.view.ScopeID)
		if scope == nil {
			e.status = "this is the system context"
			break
		}
		e.showView(diagram.For(e.opts.Graph, scope.Parent), scope.ID)
		return e, nil
	case "o":
		if b := e.selectedBox(); b != nil && e.opts.Link != nil && e.opts.Open != nil {
			if n := e.opts.Graph.Node(b.ID); n != nil {
				if err := e.opts.Open(e.opts.Link(n)); err != nil {
					e.status = "could not open the browser"
				} else {
					e.status = "opened " + n.Name + " in the browser"
				}
			}
		}
	case "y":
		if b := e.selectedBox(); b != nil && e.opts.Copy != nil {
			if err := e.opts.Copy(b.ID); err == nil {
				e.status = "copied the ID of " + b.Name
			}
		}
	}
	e.clampScroll()
	return e, nil
}

func (e *Explorer) moveSelection(delta int) {
	d := &e.diag
	if len(d.order) == 0 {
		return
	}
	d.selected = (d.selected + delta + len(d.order)) % len(d.order)
	e.renderDiagram()
	e.revealBox()
}

// refreshDiagram redraws the open view from a newer model.
func (e *Explorer) refreshDiagram() {
	d := &e.diag
	selected := ""
	if b := e.selectedBox(); b != nil {
		selected = b.ID
	}
	var v *diagram.View
	if center := e.opts.Graph.Node(d.view.FocusID); center != nil {
		v = diagram.Focus(e.opts.Graph, center, 1)
	} else if scope := e.opts.Graph.Node(d.view.ScopeID); scope != nil {
		v = diagram.For(e.opts.Graph, scope)
	} else {
		v = diagram.For(e.opts.Graph, nil)
	}
	x, y := d.x, d.y
	e.showView(v, selected)
	d.x, d.y = x, y
	e.clampScroll()
}

// diagramBody draws the view title and the visible part of the diagram.
func (e *Explorer) diagramBody(width, height int) string {
	d := &e.diag
	d.viewW, d.viewH = width-2, height-2
	e.clampScroll()

	var crumbs []string
	scope := e.opts.Graph.Node(d.view.ScopeID)
	if center := e.opts.Graph.Node(d.view.FocusID); center != nil {
		scope = center.Parent
	}
	if scope != nil {
		for _, p := range scope.Path() {
			crumbs = append(crumbs, e.s.glyph(p.Level)+" "+e.s.muted.Render(p.Name))
		}
	}
	title := e.s.title.Render(d.view.Title)
	if len(crumbs) > 0 {
		title += "  " + strings.Join(crumbs, e.s.subtle.Render(" › "))
	}
	if d.layout.Width > d.viewW || len(d.lines) > d.viewH {
		title += e.s.muted.Render("  · scroll ←↑↓→")
	}
	out := []string{" " + fit(title, width-2), ""}

	pad := 0
	if d.layout.Width < d.viewW {
		pad = (d.viewW - d.layout.Width) / 2
	}
	for i := d.y; i < len(d.lines) && i < d.y+d.viewH; i++ {
		out = append(out, " "+strings.Repeat(" ", pad)+ansi.Cut(d.lines[i], d.x, d.x+d.viewW))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
