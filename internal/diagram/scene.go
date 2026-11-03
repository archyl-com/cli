package diagram

import (
	"image/color"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/archyl-com/cli/internal/ui"
)

// ImageOptions styles the PNG and SVG renderers.
type ImageOptions struct {
	Theme ui.Theme
	// Dark picks the dark canvas.
	Dark bool
	// Highlight marks touched elements and their dependents.
	Highlight Highlight
	// Title heads the drawing; empty uses the view's.
	Title string
}

// Sizes of the image renderers, in logical pixels.
const (
	imgBoxMinW   = 150
	imgBoxMaxW   = 290
	imgBoxH      = 60
	imgPadX      = 14
	imgHeader    = 56
	imgNameSize  = 14
	imgSubSize   = 11.5
	imgLabelSize = 11
	imgTitleSize = 15
	imgLine      = 1.5
)

// imageSpacing is the image renderers' spacing, in logical pixels.
var imageSpacing = Spacing{Margin: 36, NodeGap: 40, LaneWidth: 2, PadTop: 20, TrackGap: 12, PadBottom: 36, MinChannel: 72, SegmentGap: 16}

// shape is one primitive of a scene.
type shape struct {
	kind   shapeKind
	x, y   float64 // rect origin, text baseline, circle centre
	w, h   float64 // rect size; circle radius in w
	pts    [][2]float64
	fill   color.Color
	stroke color.Color
	width  float64
	dashed bool
	text   string
	size   float64
	bold   bool
}

type shapeKind int

const (
	shapeRect shapeKind = iota
	shapeLine
	shapePolygon
	shapeCircle
	shapeText
)

// scene is a drawing in logical pixels, shared by the PNG and SVG writers.
type scene struct {
	w, h       float64
	background color.Color
	shapes     []shape
}

var (
	fontsOnce sync.Once
	regular   *opentype.Font
	bold      *opentype.Font
)

func fonts() (*opentype.Font, *opentype.Font) {
	fontsOnce.Do(func() {
		regular, _ = opentype.Parse(goregular.TTF)
		bold, _ = opentype.Parse(gobold.TTF)
	})
	return regular, bold
}

var (
	facesMu sync.Mutex
	faces   = map[[2]float64]font.Face{}
)

// face returns a cached font face; size is in device pixels.
func face(size float64, isBold bool) font.Face {
	facesMu.Lock()
	defer facesMu.Unlock()
	key := [2]float64{size, 0}
	if isBold {
		key[1] = 1
	}
	if f, ok := faces[key]; ok {
		return f
	}
	reg, b := fonts()
	src := reg
	if isBold {
		src = b
	}
	f, _ := opentype.NewFace(src, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	faces[key] = f
	return f
}

// measure is the advance of s in logical pixels.
func measure(s string, size float64, isBold bool) float64 {
	return float64(font.MeasureString(face(size, isBold), s)) / 64
}

// truncateTo shortens s with an ellipsis to fit width.
func truncateTo(s string, width, size float64, isBold bool) string {
	if measure(s, size, isBold) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && measure(string(r)+"…", size, isBold) > width {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r)) + "…"
}

// imageCanvas holds the colours of the image renderers.
type imageCanvas struct {
	bg, card, cardHot, grid color.Color
}

func canvasColors(dark bool) imageCanvas {
	if dark {
		return imageCanvas{
			bg: color.RGBA{0x09, 0x09, 0x0b, 0xff}, card: color.RGBA{0x13, 0x13, 0x16, 0xff},
			cardHot: color.RGBA{0x1b, 0x1a, 0x2e, 0xff}, grid: color.RGBA{0x22, 0x22, 0x27, 0xff},
		}
	}
	return imageCanvas{
		bg: color.RGBA{0xff, 0xff, 0xff, 0xff}, card: color.RGBA{0xfa, 0xfa, 0xfa, 0xff},
		cardHot: color.RGBA{0xee, 0xf0, 0xff, 0xff}, grid: color.RGBA{0xe4, 0xe4, 0xe7, 0xff},
	}
}

// ArrangeImage lays a view out in logical pixels.
func ArrangeImage(v *View) *Layout {
	return Arrange(v, func(n Node) (int, int) {
		name := measure(n.Name, imgNameSize, true) + 20
		if n.Children > 0 {
			name += measure(itoa(n.Children), imgSubSize, false) + 26
		}
		sub := measure(boxSubtitle(n), imgSubSize, false)
		w := max(name, sub) + 2*imgPadX
		return int(min(max(w, imgBoxMinW), imgBoxMaxW)), imgBoxH
	}, imageSpacing)
}

// buildScene turns an image layout into shapes.
func buildScene(l *Layout, opts ImageOptions) *scene {
	t := opts.Theme
	cc := canvasColors(opts.Dark)
	pal := palette{t: t, highlight: l.View.Lift(opts.Highlight), marked: l.View.Marked()}
	s := &scene{w: float64(l.Width), h: float64(l.Height + imgHeader), background: cc.bg}
	s.w = max(s.w, 420)
	add := func(sh shape) { s.shapes = append(s.shapes, sh) }

	// A faint dot grid, the canvas of the web app.
	for y := 12.0; y < s.h; y += 20 {
		for x := 12.0; x < s.w; x += 20 {
			add(shape{kind: shapeCircle, x: x, y: y, w: 0.9, fill: cc.grid})
		}
	}

	title := opts.Title
	if title == "" {
		title = l.View.Title
	}
	add(shape{kind: shapePolygon, pts: diamond(float64(imageSpacing.Margin)+5, 30, 6), fill: t.Accent})
	add(shape{kind: shapeText, x: float64(imageSpacing.Margin) + 17, y: 35, text: "archyl", size: imgTitleSize, bold: true, fill: t.Accent})
	add(shape{kind: shapeText, x: float64(imageSpacing.Margin) + 17 + measure("archyl", imgTitleSize, true) + 12, y: 35,
		text: title, size: imgTitleSize, fill: t.Text})
	if pal.marked {
		x := s.w - float64(imageSpacing.Margin)
		for _, item := range []Change{Removed, Changed, Added} {
			x -= measure(string(item), imgLabelSize, false)
			add(shape{kind: shapeText, x: x, y: 34, text: string(item), size: imgLabelSize, fill: t.Muted})
			x -= 16
			add(shape{kind: shapeRect, x: x, y: 25, w: 10, h: 10, fill: cc.card, stroke: pal.changeColor(item), width: 2, dashed: item == Removed})
			x -= 18
		}
	} else if len(pal.highlight) > 0 {
		x := s.w - float64(imageSpacing.Margin)
		for _, item := range []struct {
			label string
			heat  int
		}{{"unrelated", 100}, {"depends on it", 1}, {"touched", 0}} {
			x -= measure(item.label, imgLabelSize, false)
			add(shape{kind: shapeText, x: x, y: 34, text: item.label, size: imgLabelSize, fill: t.Muted})
			x -= 16
			add(shape{kind: shapeRect, x: x, y: 25, w: 10, h: 10, fill: cc.card, stroke: pal.shade(item.heat), width: 2})
			x -= 18
		}
	}

	dy := float64(imgHeader)
	var occupied [][4]float64
	for _, b := range l.Boxes {
		occupied = append(occupied, [4]float64{float64(b.X), float64(b.Y) + dy, float64(b.W), float64(b.H)})
	}

	for _, r := range l.Routes {
		heat := pal.edgeHeat(r.Edge)
		c := pal.shade(heat)
		width := imgLine
		if heat < 50 {
			width = 2
		}
		if pal.marked {
			c, width = pal.changeColor(r.Change), imgLine
			if r.Change == Added || r.Change == Changed {
				width = 2
			}
		}
		pts := make([][2]float64, len(r.Points))
		for i, p := range r.Points {
			pts[i] = [2]float64{float64(p.X), float64(p.Y) + dy}
		}
		// Join the box the arrow leaves, and leave room for the head.
		if len(pts) >= 2 {
			pts[0][1] += sign(pts[0][1]-pts[1][1]) * 1
			end := len(pts) - 1
			pts[end][1] += sign(pts[end][1]-pts[end-1][1]) * 1
		}
		add(shape{kind: shapeLine, pts: pts, stroke: c, width: width, dashed: pal.marked && r.Change == Removed})
		if len(pts) >= 2 {
			add(shape{kind: shapePolygon, pts: arrowHead(pts[len(pts)-1], pts[len(pts)-2]), fill: c})
			if r.Both {
				add(shape{kind: shapePolygon, pts: arrowHead(pts[0], pts[1]), fill: c})
			}
		}
	}

	for _, b := range l.Boxes {
		heat := pal.heat(b.ID)
		x, y, w, h := float64(b.X), float64(b.Y)+dy, float64(b.W), float64(b.H)
		border, fill, nameColor := t.LevelColor(b.Level), cc.card, t.Text
		width := imgLine
		switch {
		case heat == 0:
			border, fill, width = t.Accent, cc.cardHot, 2.5
		case heat < 50:
			border, width = pal.shade(heat), 2
		case heat == 100:
			border, nameColor = t.Subtle, t.Muted
		}
		dashed := b.External && heat != 0
		if dashed && heat >= 50 {
			border = t.Muted
		}
		glyphColor := t.LevelColor(b.Level)
		if heat == 100 {
			glyphColor = t.Subtle
		}
		if pal.marked {
			var set borderSet
			set, border, nameColor, glyphColor = pal.markedBox(b.Node)
			fill, width, dashed = cc.card, imgLine, set == dashedBorder
			if b.Change == Added || b.Change == Changed {
				width = 2.5
			}
		}
		add(shape{kind: shapeRect, x: x, y: y, w: w, h: h, fill: fill, stroke: border, width: width, dashed: dashed})
		if badge := b.Change.Badge(); pal.marked && badge != "" {
			s.shapes = append(s.shapes, changeBadge(badge, x+w, y, pal.changeColor(b.Change), cc.bg)...)
		}
		add(levelGlyph(b.Level, x+imgPadX+5, y+22, glyphColor))
		nameRoom := w - 2*imgPadX - 16
		if b.Children > 0 {
			count := itoa(b.Children)
			cw := measure(count, imgSubSize, false)
			add(shape{kind: shapeText, x: x + w - imgPadX - 14 - cw, y: y + 26, text: count, size: imgSubSize, fill: t.Muted})
			add(levelGlyph(childLevel(b.Level), x+w-imgPadX-4, y+22, t.Muted))
			nameRoom -= cw + 24
		}
		add(shape{kind: shapeText, x: x + imgPadX + 16, y: y + 27, text: truncateTo(b.Name, nameRoom, imgNameSize, true),
			size: imgNameSize, bold: true, fill: nameColor})
		add(shape{kind: shapeText, x: x + imgPadX, y: y + 46, text: truncateTo(boxSubtitle(b.Node), w-2*imgPadX, imgSubSize, false),
			size: imgSubSize, fill: t.Muted})
	}

	for _, r := range l.Routes {
		if sh, ok := imageLabel(r, dy, &occupied, pal, cc); ok {
			s.shapes = append(s.shapes, sh...)
		}
	}
	return s
}

func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// imageLabel places an edge label on the longest free stretch of its route,
// on a background chip, or leaves it out when every spot is taken.
func imageLabel(r Route, dy float64, occupied *[][4]float64, pal palette, cc imageCanvas) ([]shape, bool) {
	label := r.Label
	if label == "" {
		return nil, false
	}
	if r.Count > 1 && !r.Both {
		label += " ×" + itoa(r.Count)
	}
	label = truncateTo(label, 170, imgLabelSize, false)
	w, h := measure(label, imgLabelSize, false)+10, 16.0
	free := func(x, y float64) bool {
		for _, o := range *occupied {
			if x < o[0]+o[2]+2 && x+w+2 > o[0] && y < o[1]+o[3]+2 && y+h+2 > o[1] {
				return false
			}
		}
		return true
	}
	var spots [][2]float64
	pts := r.Points
	for i := len(pts) - 1; i > 0; i-- {
		a, b := pts[i-1], pts[i]
		ax, ay, bx, by := float64(a.X), float64(a.Y)+dy, float64(b.X), float64(b.Y)+dy
		if ax == bx {
			if max(ay, by)-min(ay, by) < h+8 {
				continue
			}
			mid := (ay+by)/2 - h/2
			spots = append(spots, [2]float64{ax - w/2, mid}, [2]float64{ax + 6, mid}, [2]float64{ax - w - 6, mid})
		} else {
			if max(ax, bx)-min(ax, bx) < w+8 {
				continue
			}
			spots = append(spots, [2]float64{(ax+bx)/2 - w/2, ay - h/2})
		}
	}
	heat := pal.edgeHeat(r.Edge)
	textColor := pal.shade(min(heat, 50))
	if heat > 50 {
		textColor = pal.t.Muted
	}
	if pal.marked {
		textColor = pal.changeColor(r.Change)
		if r.Change == Removed {
			textColor = pal.t.Muted
		}
	}
	for _, sp := range spots {
		if !free(sp[0], sp[1]) {
			continue
		}
		*occupied = append(*occupied, [4]float64{sp[0], sp[1], w, h})
		return []shape{
			{kind: shapeRect, x: sp[0], y: sp[1], w: w, h: h, fill: cc.bg},
			{kind: shapeText, x: sp[0] + 5, y: sp[1] + 12, text: label, size: imgLabelSize, fill: textColor},
		}, true
	}
	return nil, false
}

// changeBadge is a small disc on a box's top-right corner holding the sign
// of its change.
func changeBadge(sign string, cx, cy float64, fill, ink color.Color) []shape {
	const r, size = 8.0, 12.0
	return []shape{
		{kind: shapeCircle, x: cx, y: cy, w: r + 1.5, fill: ink},
		{kind: shapeCircle, x: cx, y: cy, w: r, fill: fill},
		{kind: shapeText, x: cx - measure(sign, size, true)/2, y: cy + 4.5, text: sign, size: size, bold: true, fill: ink},
	}
}

// arrowHead is a triangle with its tip at tip, pointing away from from.
func arrowHead(tip, from [2]float64) [][2]float64 {
	const length, half = 9, 4.5
	dx, dy := sign(tip[0]-from[0]), sign(tip[1]-from[1])
	bx, by := tip[0]-dx*length, tip[1]-dy*length
	return [][2]float64{tip, {bx - dy*half, by + dx*half}, {bx + dy*half, by - dx*half}}
}

func diamond(cx, cy, r float64) [][2]float64 {
	return [][2]float64{{cx, cy - r}, {cx + r, cy}, {cx, cy + r}, {cx - r, cy}}
}

// levelGlyph draws the marker of a level: a diamond for systems, a square
// for containers, a disc for components, a small bar for code.
func levelGlyph(level string, cx, cy float64, c color.Color) shape {
	switch level {
	case "system":
		return shape{kind: shapePolygon, pts: diamond(cx, cy, 5.5), fill: c}
	case "container":
		return shape{kind: shapeRect, x: cx - 4.5, y: cy - 4.5, w: 9, h: 9, fill: c}
	case "component":
		return shape{kind: shapeCircle, x: cx, y: cy, w: 4.5, fill: c}
	}
	return shape{kind: shapeRect, x: cx - 4.5, y: cy - 1.5, w: 9, h: 3, fill: c}
}
