package diagram

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/archyl-com/cli/internal/ui"
)

// Box sizes of the text renderer, in cells.
const (
	textBoxHeight = 4
	textBoxMin    = 14
	// TextBoxMax is the widest a box gets before its name is shortened.
	TextBoxMax   = 30
	textLabelMax = 24
)

// TextSpacing is the text renderer's spacing, in cells.
var TextSpacing = Spacing{Margin: 1, NodeGap: 3, LaneWidth: 1, PadTop: 1, TrackGap: 1, PadBottom: 2, MinChannel: 3, SegmentGap: 1}

// TextOptions styles the text renderer.
type TextOptions struct {
	Theme ui.Theme
	// Color styles the output; without it the drawing is plain characters.
	Color bool
	// Highlight marks touched elements and their dependents.
	Highlight Highlight
	// Selected is the ID of a box drawn as the selection.
	Selected string
	// Width is the room available; a wider drawing falls back to a list.
	// Zero means unbounded.
	Width int
}

// ArrangeText lays a view out in cells, boxes at most maxBox wide.
func ArrangeText(v *View, maxBox int) *Layout {
	return Arrange(v, func(n Node) (int, int) {
		w := max(ansi.StringWidth(boxTitle(n))+4+ansi.StringWidth(childMark(n)), ansi.StringWidth(boxSubtitle(n))+4, textBoxMin)
		return min(w, maxBox), textBoxHeight
	}, TextSpacing)
}

// Text renders a view as box-drawing text that fits opts.Width: full boxes,
// then narrower ones, then a list of elements and their arrows.
func Text(v *View, opts TextOptions) string {
	l := ArrangeText(v, TextBoxMax)
	if opts.Width > 0 && l.Width > opts.Width {
		l = ArrangeText(v, 20)
		if l.Width > opts.Width {
			return List(v, opts)
		}
	}
	return RenderText(l, opts)
}

func boxTitle(n Node) string {
	return ui.LevelGlyph(n.Level) + " " + n.Name
}

// childMark tells how many elements a drill-down into n shows.
func childMark(n Node) string {
	if n.Children == 0 {
		return ""
	}
	return "  " + itoa(n.Children) + " " + ui.LevelGlyph(childLevel(n.Level))
}

func boxSubtitle(n Node) string {
	var parts []string
	if n.Kind != "" {
		parts = append(parts, strings.ReplaceAll(n.Kind, "_", " "))
	} else {
		parts = append(parts, n.Level)
	}
	if n.Parent != "" {
		parts = append(parts, "in "+n.Parent)
	} else if len(n.Tech) > 0 {
		parts = append(parts, strings.Join(n.Tech[:min(len(n.Tech), 2)], ", "))
	}
	return strings.Join(parts, " · ")
}

// childLevel is the level a drill-down into this one shows.
func childLevel(level string) string {
	switch level {
	case "system":
		return "container"
	case "container":
		return "component"
	}
	return "code"
}

// Connection bits of an edge cell.
const (
	bitUp uint8 = 1 << iota
	bitDown
	bitLeft
	bitRight
)

var lineRunes = map[uint8]rune{
	bitUp: '│', bitDown: '│', bitUp | bitDown: '│',
	bitLeft: '─', bitRight: '─', bitLeft | bitRight: '─',
	bitDown | bitRight: '┌', bitDown | bitLeft: '┐', bitUp | bitRight: '└', bitUp | bitLeft: '┘',
	bitUp | bitDown | bitRight: '├', bitUp | bitDown | bitLeft: '┤',
	bitDown | bitLeft | bitRight: '┬', bitUp | bitLeft | bitRight: '┴',
	bitUp | bitDown | bitLeft | bitRight: '┼',
}

type borderSet struct{ h, v, tl, tr, bl, br, teeDown, teeUp rune }

var (
	lightBorder  = borderSet{'─', '│', '┌', '┐', '└', '┘', '┬', '┴'}
	heavyBorder  = borderSet{'━', '┃', '┏', '┓', '┗', '┛', '┯', '┷'}
	dashedBorder = borderSet{'╌', '╎', '┌', '┐', '└', '┘', '┬', '┴'}
)

type cell struct {
	r     rune
	style int
	bits  uint8
	heat  int // edge cells: the hottest edge through, lower is hotter
	taken bool
	// change is the strongest change of the edges through, and solid is
	// set when one of them was not removed.
	change Change
	solid  bool
}

// canvas is a grid of styled cells.
type canvas struct {
	w, h   int
	cells  [][]cell
	styles []lipgloss.Style
	keys   map[styleKey]int
}

type styleKey struct {
	fg   color.Color
	bold bool
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([][]cell, h), styles: []lipgloss.Style{lipgloss.NewStyle()}, keys: map[styleKey]int{}}
	for y := range c.cells {
		c.cells[y] = make([]cell, w)
		for x := range c.cells[y] {
			c.cells[y][x] = cell{r: ' ', heat: 1 << 30}
		}
	}
	return c
}

func (c *canvas) style(fg color.Color, bold bool) int {
	if fg == nil {
		return 0
	}
	k := styleKey{fg, bold}
	if i, ok := c.keys[k]; ok {
		return i
	}
	c.styles = append(c.styles, lipgloss.NewStyle().Foreground(fg).Bold(bold))
	c.keys[k] = len(c.styles) - 1
	return len(c.styles) - 1
}

func (c *canvas) in(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.w && y < c.h
}

func (c *canvas) set(x, y int, r rune, style int) {
	if c.in(x, y) {
		c.cells[y][x].r, c.cells[y][x].style, c.cells[y][x].taken = r, style, true
	}
}

func (c *canvas) text(x, y int, s string, style int) {
	for _, r := range s {
		c.set(x, y, r, style)
		if ansi.StringWidth(string(r)) == 2 {
			x++
			c.set(x, y, 0, style)
		}
		x++
	}
}

// free reports whether a run of cells holds nothing.
func (c *canvas) free(x, y, n int) bool {
	for i := range n {
		if !c.in(x+i, y) || c.cells[y][x+i].taken || c.cells[y][x+i].bits != 0 {
			return false
		}
	}
	return true
}

func (c *canvas) String(color bool) string {
	lines := make([]string, c.h)
	for y, row := range c.cells {
		var b strings.Builder
		run, runStyle := []rune{}, -1
		flush := func() {
			if len(run) == 0 {
				return
			}
			if color && runStyle > 0 {
				b.WriteString(c.styles[runStyle].Render(string(run)))
			} else {
				b.WriteString(string(run))
			}
			run = run[:0]
		}
		for _, cl := range row {
			if cl.r == 0 {
				continue // the right half of a wide rune
			}
			if cl.style != runStyle {
				flush()
				runStyle = cl.style
			}
			run = append(run, cl.r)
		}
		flush()
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// palette resolves the colours of a drawing.
type palette struct {
	t         ui.Theme
	highlight Highlight
	selected  string
	// marked draws changes instead of heat: what was added, removed or
	// changed stands out, the rest is muted.
	marked bool
}

// changeColor is the colour of a change in a marked drawing.
func (p palette) changeColor(c Change) color.Color {
	switch c {
	case Added:
		return p.t.Success
	case Removed:
		return p.t.Subtle
	case Changed:
		return p.t.Warning
	}
	return p.t.Muted
}

// heat ranks a box for its edges: the selection and touched boxes first,
// then dependents by distance, then the rest (50 when nothing is
// highlighted, 100 when they are unrelated to a highlight).
func (p palette) heat(id string) int {
	if p.selected != "" && id == p.selected {
		return 0
	}
	if len(p.highlight) == 0 {
		return 50
	}
	if d, ok := p.highlight[id]; ok {
		return d
	}
	return 100
}

// shade is the colour of a heat: the accent fading toward the muted text.
func (p palette) shade(heat int) color.Color {
	switch {
	case heat == 0:
		return p.t.Accent
	case heat == 1:
		return blend(p.t.Accent, p.t.Muted, 0.45)
	case heat == 2:
		return blend(p.t.Accent, p.t.Muted, 0.7)
	case heat < 50:
		return blend(p.t.Accent, p.t.Muted, 0.85)
	case heat == 50:
		return p.t.Muted
	}
	return p.t.Subtle
}

func (p palette) edgeHeat(e Edge) int {
	if p.selected != "" && (e.From == p.selected || e.To == p.selected) {
		return 0
	}
	return max(p.heat(e.From), p.heat(e.To))
}

// blend mixes a toward b by t.
func blend(a, b color.Color, t float64) color.Color {
	if a == nil || b == nil {
		return a
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8((float64(x>>8)*(1-t) + float64(y>>8)*t) + 0.5)
	}
	return color.RGBA{mix(ar, br), mix(ag, bg), mix(ab, bb), 255}
}

// RenderText draws a text layout.
func RenderText(l *Layout, opts TextOptions) string {
	c := newCanvas(l.Width, l.Height)
	pal := palette{t: opts.Theme, highlight: l.View.Lift(opts.Highlight), selected: opts.Selected, marked: l.View.Marked()}
	if !opts.Color {
		pal.t = ui.Theme{}
	}
	color := func(fg color.Color, bold bool) int {
		if !opts.Color {
			return 0
		}
		return c.style(fg, bold)
	}

	// Edges first, as connection bits, so crossings and bends join up.
	for _, r := range l.Routes {
		heat := pal.edgeHeat(r.Edge)
		mark := func(x, y int, bits uint8) {
			if !c.in(x, y) {
				return
			}
			cl := &c.cells[y][x]
			cl.bits |= bits
			cl.heat = min(cl.heat, heat)
			if markRank(r.Change) > markRank(cl.change) {
				cl.change = r.Change
			}
			cl.solid = cl.solid || r.Change != Removed
		}
		pts := r.Points
		for i := 0; i+1 < len(pts); i++ {
			a, b := pts[i], pts[i+1]
			switch a.X {
			case b.X:
				lo, hi := min(a.Y, b.Y), max(a.Y, b.Y)
				for y := lo; y <= hi; y++ {
					var bits uint8
					if y > lo {
						bits |= bitUp
					}
					if y < hi {
						bits |= bitDown
					}
					mark(a.X, y, bits)
				}
			default:
				lo, hi := min(a.X, b.X), max(a.X, b.X)
				for x := lo; x <= hi; x++ {
					var bits uint8
					if x > lo {
						bits |= bitLeft
					}
					if x < hi {
						bits |= bitRight
					}
					mark(x, a.Y, bits)
				}
			}
		}
		if len(pts) > 0 {
			// The first cell joins the source box.
			first := pts[0]
			if len(pts) == 1 || pts[1].Y > first.Y {
				mark(first.X, first.Y, bitUp)
			} else {
				mark(first.X, first.Y, bitDown)
			}
		}
	}
	for y := range c.cells {
		for x := range c.cells[y] {
			cl := &c.cells[y][x]
			if cl.bits != 0 {
				cl.r = lineRunes[cl.bits]
				cl.style = color(pal.shade(cl.heat), cl.heat == 0)
				if pal.marked {
					cl.style = color(pal.changeColor(cl.change), false)
					if !cl.solid {
						cl.r = dashedLine(cl.r)
					}
				}
			}
		}
	}

	for _, b := range l.Boxes {
		drawBox(c, b, pal, color)
	}

	// Arrowheads, and tees where arrows leave a box.
	for _, r := range l.Routes {
		if len(r.Points) < 2 {
			continue
		}
		heat := pal.edgeHeat(r.Edge)
		style := color(pal.shade(heat), heat == 0)
		if pal.marked {
			style = color(pal.changeColor(r.Change), false)
		}
		head := func(at, from Point) {
			arrow := '▼'
			if from.Y > at.Y {
				arrow = '▲'
			}
			c.set(at.X, at.Y, arrow, style)
		}
		pts := r.Points
		end, start := pts[len(pts)-1], pts[0]
		head(end, pts[len(pts)-2])
		if r.Both {
			head(start, pts[1])
		} else {
			tee(c, start, pts[1].Y > start.Y)
		}
	}

	for _, r := range l.Routes {
		placeLabel(c, r, pal, color)
	}
	return c.String(opts.Color)
}

// dashedLine is the dashed form of a straight line rune, for removed
// arrows; bends and crossings keep theirs.
func dashedLine(r rune) rune {
	switch r {
	case '─':
		return '╌'
	case '│':
		return '╎'
	}
	return r
}

// tee joins an arrow to the border of the box it leaves.
func tee(c *canvas, start Point, down bool) {
	y := start.Y + 1
	if down {
		y = start.Y - 1
	}
	if !c.in(start.X, y) {
		return
	}
	cl := &c.cells[y][start.X]
	for _, set := range []borderSet{lightBorder, heavyBorder, dashedBorder} {
		if cl.r == set.h {
			if down {
				cl.r = set.teeDown
			} else {
				cl.r = set.teeUp
			}
			return
		}
	}
}

func drawBox(c *canvas, b Box, pal palette, color func(color.Color, bool) int) {
	set := lightBorder
	border := pal.t.LevelColor(b.Level)
	nameColor := pal.t.Text
	heat := pal.heat(b.ID)
	switch {
	case heat == 0:
		set, border = heavyBorder, pal.t.Accent
	case heat < 50:
		border = pal.shade(heat)
	case heat == 100:
		border, nameColor = pal.t.Subtle, pal.t.Muted
	}
	if b.External && heat != 0 {
		set = dashedBorder
		if heat >= 50 {
			border = pal.t.Muted
		}
	}
	glyphColor := pal.t.LevelColor(b.Level)
	if heat == 100 {
		glyphColor = pal.t.Subtle
	}
	bold := heat == 0
	if pal.marked {
		set, border, nameColor, glyphColor = pal.markedBox(b.Node)
		bold = b.Change == Added || b.Change == Changed
	}
	bs := color(border, bold)
	right, bottom := b.X+b.W-1, b.Y+b.H-1
	for x := b.X + 1; x < right; x++ {
		c.set(x, b.Y, set.h, bs)
		c.set(x, bottom, set.h, bs)
	}
	for y := b.Y + 1; y < bottom; y++ {
		c.set(b.X, y, set.v, bs)
		c.set(right, y, set.v, bs)
		for x := b.X + 1; x < right; x++ {
			c.set(x, y, ' ', 0)
		}
	}
	c.set(b.X, b.Y, set.tl, bs)
	c.set(right, b.Y, set.tr, bs)
	c.set(b.X, bottom, set.bl, bs)
	c.set(right, bottom, set.br, bs)

	if badge := b.Change.Badge(); pal.marked && badge != "" {
		c.text(b.X+2, b.Y, " "+badge+" ", bs)
	}

	inner := b.W - 4
	mark := childMark(b.Node)
	if inner-2-ansi.StringWidth(mark) < 6 {
		mark = ""
	}
	c.text(b.X+2, b.Y+1, ui.LevelGlyph(b.Level), color(glyphColor, false))
	c.text(b.X+4, b.Y+1, ansi.Truncate(b.Name, inner-2-ansi.StringWidth(mark), "…"), color(nameColor, true))
	if mark != "" {
		c.text(right-1-ansi.StringWidth(mark), b.Y+1, mark, color(pal.t.Muted, false))
	}
	c.text(b.X+2, b.Y+2, ansi.Truncate(boxSubtitle(b.Node), inner, "…"), color(pal.t.Muted, false))
}

// markedBox is how a marked drawing draws a box: added and changed boxes
// heavy in their colour, removed ones dashed and dim, the rest muted.
func (p palette) markedBox(n Node) (set borderSet, border, name, glyph color.Color) {
	set, border, name, glyph = lightBorder, p.t.Muted, p.t.Text, p.t.LevelColor(n.Level)
	if n.External {
		set = dashedBorder
	}
	switch n.Change {
	case Added, Changed:
		set, border = heavyBorder, p.changeColor(n.Change)
	case Removed:
		set, border, name, glyph = dashedBorder, p.t.Subtle, p.t.Muted, p.t.Subtle
	}
	return set, border, name, glyph
}

// placeLabel writes an edge's label in the first free spot along it: beside
// a vertical run, then above or below a horizontal one. A label with no room
// is left out.
func placeLabel(c *canvas, r Route, pal palette, color func(color.Color, bool) int) {
	label := r.Label
	if label == "" {
		return
	}
	if r.Count > 1 && !r.Both {
		label += " ×" + itoa(r.Count)
	}
	label = ansi.Truncate(label, textLabelMax, "…")
	n := ansi.StringWidth(label)
	heat := pal.edgeHeat(r.Edge)
	style := color(pal.shade(min(heat, 50)), false)
	if heat > 50 {
		style = color(pal.t.Subtle, false)
	}
	if pal.marked {
		style = color(pal.changeColor(r.Change), false)
	}

	pts := r.Points
	try := func(x, y int) bool {
		if c.free(x-1, y, n+2) {
			c.text(x, y, label, style)
			return true
		}
		return false
	}
	// Vertical runs, from the target backwards, middle row first.
	for i := len(pts) - 1; i > 0; i-- {
		a, b := pts[i-1], pts[i]
		if a.X != b.X {
			continue
		}
		lo, hi := min(a.Y, b.Y), max(a.Y, b.Y)
		mid := (lo + hi) / 2
		for d := 0; d <= hi-lo; d++ {
			for _, y := range []int{mid + d, mid - d} {
				if y < lo || y > hi {
					continue
				}
				if try(a.X+2, y) || try(a.X-1-n, y) {
					return
				}
			}
		}
	}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if a.Y != b.Y {
			continue
		}
		lo := min(a.X, b.X)
		if try(lo+1, a.Y-1) || try(lo+1, a.Y+1) {
			return
		}
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// List renders a view as its elements, each with the arrows it starts: the
// form for terminals too narrow for the drawing.
func List(v *View, opts TextOptions) string {
	pal := palette{t: opts.Theme, highlight: v.Lift(opts.Highlight), selected: opts.Selected, marked: v.Marked()}
	paint := func(fg color.Color, bold bool, s string) string {
		if !opts.Color || fg == nil {
			return s
		}
		return lipgloss.NewStyle().Foreground(fg).Bold(bold).Render(s)
	}
	var lines []string
	for _, n := range v.Nodes {
		heat := pal.heat(n.ID)
		glyph := paint(opts.Theme.LevelColor(n.Level), false, ui.LevelGlyph(n.Level))
		nameColor := opts.Theme.Text
		if heat < 50 {
			nameColor = pal.shade(heat)
		}
		line := glyph + " " + paint(nameColor, true, n.Name) + "  " + paint(opts.Theme.Muted, false, boxSubtitle(n))
		if badge := n.Change.Badge(); pal.marked && badge != "" {
			line = paint(pal.changeColor(n.Change), true, badge) + " " + line
		}
		if n.External {
			line += paint(opts.Theme.Muted, false, " · external")
		}
		lines = append(lines, line)
		for _, e := range v.Edges {
			if e.From != n.ID {
				continue
			}
			arrow, other := "→", e.To
			if e.Both && e.From == n.ID {
				arrow = "↔"
			}
			target := v.Node(other)
			arrowColor := pal.shade(pal.edgeHeat(e))
			if pal.marked {
				arrowColor = pal.changeColor(e.Change)
				arrow = e.Change.Badge() + arrow
			}
			text := "  " + paint(arrowColor, false, arrow) + " " + target.Name
			label := e.Label
			if e.Count > 1 && !e.Both {
				label += " ×" + itoa(e.Count)
			}
			if label != "" {
				text += "  " + paint(opts.Theme.Muted, false, label)
			}
			lines = append(lines, text)
		}
	}
	return strings.Join(lines, "\n")
}

// Swatch is a small box in the colour of a heat, for legends.
func Swatch(t ui.Theme, heat int) string {
	pal := palette{t: t, highlight: Highlight{"": 0}}
	return lipgloss.NewStyle().Foreground(pal.shade(heat)).Render("┌─┐")
}
