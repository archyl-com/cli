package diagram

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// imageScale renders PNGs at twice their logical size, for sharp text on
// high-density screens.
const imageScale = 2

// PNG renders a view as a PNG image. It returns the image's logical width
// in pixels, for terminals that size inline images.
func PNG(v *View, opts ImageOptions) ([]byte, int, error) {
	s := buildScene(ArrangeImage(v), opts)
	img := rasterize(s, imageScale)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, 0, fmt.Errorf("encode the diagram: %w", err)
	}
	return buf.Bytes(), int(s.w), nil
}

// SVG renders a view as an SVG document.
func SVG(w io.Writer, v *View, opts ImageOptions) error {
	s := buildScene(ArrangeImage(v), opts)
	_, err := io.WriteString(w, svgDocument(s))
	return err
}

func rasterize(s *scene, k float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(s.w*k)), int(math.Ceil(s.h*k))))
	draw.Draw(img, img.Bounds(), image.NewUniform(s.background), image.Point{}, draw.Src)
	for _, sh := range s.shapes {
		switch sh.kind {
		case shapeRect:
			if sh.fill != nil {
				fillRect(img, sh.x*k, sh.y*k, sh.w*k, sh.h*k, sh.fill)
			}
			if sh.stroke != nil {
				strokeRect(img, sh, k)
			}
		case shapeLine:
			for i := 0; i+1 < len(sh.pts); i++ {
				strokeSegment(img, sh.pts[i], sh.pts[i+1], sh, k)
			}
		case shapePolygon:
			fillPolygon(img, sh.pts, k, sh.fill)
		case shapeCircle:
			pts := make([][2]float64, 0, 24)
			for i := range 24 {
				a := float64(i) * 2 * math.Pi / 24
				pts = append(pts, [2]float64{sh.x + sh.w*math.Cos(a), sh.y + sh.w*math.Sin(a)})
			}
			fillPolygon(img, pts, k, sh.fill)
		case shapeText:
			d := font.Drawer{Dst: img, Src: image.NewUniform(sh.fill), Face: face(sh.size*k, sh.bold),
				Dot: fixed.Point26_6{X: fixed.Int26_6(sh.x * k * 64), Y: fixed.Int26_6(sh.y * k * 64)}}
			d.DrawString(sh.text)
		}
	}
	return img
}

// fillRect fills a rectangle, blending the partly covered edge pixels.
func fillRect(img *image.RGBA, x, y, w, h float64, c color.Color) {
	if w <= 0 || h <= 0 {
		return
	}
	fillPolygon(img, [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}, 1, c)
}

// strokeSegment draws one horizontal or vertical run of a line, in dashes
// when the line is dashed.
func strokeSegment(img *image.RGBA, a, b [2]float64, sh shape, k float64) {
	half := sh.width / 2
	x0, y0 := min(a[0], b[0])-half, min(a[1], b[1])-half
	x1, y1 := max(a[0], b[0])+half, max(a[1], b[1])+half
	if !sh.dashed {
		fillRect(img, x0*k, y0*k, (x1-x0)*k, (y1-y0)*k, sh.stroke)
		return
	}
	const dash, gap = 6.0, 4.0
	if x1-x0 >= y1-y0 {
		for x := x0; x < x1; x += dash + gap {
			fillRect(img, x*k, y0*k, min(dash, x1-x)*k, (y1-y0)*k, sh.stroke)
		}
		return
	}
	for y := y0; y < y1; y += dash + gap {
		fillRect(img, x0*k, y*k, (x1-x0)*k, min(dash, y1-y)*k, sh.stroke)
	}
}

func strokeRect(img *image.RGBA, sh shape, k float64) {
	x, y, w, h, t := sh.x*k, sh.y*k, sh.w*k, sh.h*k, sh.width*k
	if !sh.dashed {
		fillRect(img, x, y, w, t, sh.stroke)
		fillRect(img, x, y+h-t, w, t, sh.stroke)
		fillRect(img, x, y, t, h, sh.stroke)
		fillRect(img, x+w-t, y, t, h, sh.stroke)
		return
	}
	dash, gap := 6*k, 4*k
	for d := 0.0; d < w; d += dash + gap {
		l := min(dash, w-d)
		fillRect(img, x+d, y, l, t, sh.stroke)
		fillRect(img, x+d, y+h-t, l, t, sh.stroke)
	}
	for d := 0.0; d < h; d += dash + gap {
		l := min(dash, h-d)
		fillRect(img, x, y+d, t, l, sh.stroke)
		fillRect(img, x+w-t, y+d, t, l, sh.stroke)
	}
}

// fillPolygon fills a polygon with anti-aliased edges.
func fillPolygon(img *image.RGBA, pts [][2]float64, k float64, c color.Color) {
	if len(pts) < 3 || c == nil {
		return
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		minX, minY = min(minX, p[0]*k), min(minY, p[1]*k)
		maxX, maxY = max(maxX, p[0]*k), max(maxY, p[1]*k)
	}
	bounds := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))).Intersect(img.Bounds())
	if bounds.Empty() {
		return
	}
	r := vector.NewRasterizer(bounds.Dx(), bounds.Dy())
	ox, oy := float32(bounds.Min.X), float32(bounds.Min.Y)
	r.MoveTo(float32(pts[0][0]*k)-ox, float32(pts[0][1]*k)-oy)
	for _, p := range pts[1:] {
		r.LineTo(float32(p[0]*k)-ox, float32(p[1]*k)-oy)
	}
	r.ClosePath()
	r.DrawOp = draw.Over
	r.Draw(img, bounds, image.NewUniform(c), image.Point{})
}

// svgDocument writes a scene as SVG, in logical pixels.
func svgDocument(s *scene) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s">`+"\n",
		num(s.w), num(s.h), num(s.w), num(s.h))
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" fill="%s"/>`+"\n", hexColor(s.background))
	b.WriteString(`<g font-family="Inter, 'Segoe UI', 'Helvetica Neue', Arial, sans-serif">` + "\n")
	for _, sh := range s.shapes {
		switch sh.kind {
		case shapeRect:
			attrs := ""
			if sh.fill != nil {
				attrs += ` fill="` + hexColor(sh.fill) + `"`
			} else {
				attrs += ` fill="none"`
			}
			x, y, w, h := sh.x, sh.y, sh.w, sh.h
			if sh.stroke != nil {
				// SVG centres strokes on the outline; the PNG draws them inside.
				x, y, w, h = x+sh.width/2, y+sh.width/2, w-sh.width, h-sh.width
				attrs += fmt.Sprintf(` stroke="%s" stroke-width="%s"`, hexColor(sh.stroke), num(sh.width))
				if sh.dashed {
					attrs += ` stroke-dasharray="6 4"`
				}
			}
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s"%s/>`+"\n", num(x), num(y), num(w), num(h), attrs)
		case shapeLine:
			dash := ""
			if sh.dashed {
				dash = ` stroke-dasharray="6 4"`
			}
			fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%s" stroke-linejoin="miter"%s/>`+"\n",
				points(sh.pts), hexColor(sh.stroke), num(sh.width), dash)
		case shapePolygon:
			fmt.Fprintf(&b, `<polygon points="%s" fill="%s"/>`+"\n", points(sh.pts), hexColor(sh.fill))
		case shapeCircle:
			fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`+"\n", num(sh.x), num(sh.y), num(sh.w), hexColor(sh.fill))
		case shapeText:
			weight := ""
			if sh.bold {
				weight = ` font-weight="600"`
			}
			fmt.Fprintf(&b, `<text x="%s" y="%s" font-size="%s"%s fill="%s">%s</text>`+"\n",
				num(sh.x), num(sh.y), num(sh.size), weight, hexColor(sh.fill), xmlEscape(sh.text))
		}
	}
	b.WriteString("</g>\n</svg>\n")
	return b.String()
}

func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}

func points(pts [][2]float64) string {
	parts := make([]string, len(pts))
	for i, p := range pts {
		parts[i] = num(p[0]) + "," + num(p[1])
	}
	return strings.Join(parts, " ")
}

func hexColor(c color.Color) string {
	if c == nil {
		return "none"
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
