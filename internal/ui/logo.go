package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// logoRows is Archyl's mark in half blocks, drawn from the logo: the dome, a
// band notched by the top of the counter, a band pierced by its wider bottom,
// and the two feet. Each layer is two half-rows tall with a one half-row gap,
// so the layers read as thick bands with thin gaps, as in the logo.
var logoRows = []string{
	"   ▄██▄",
	"  ▄▄▄▄▄▄",
	"  ▀▀  ▀▀",
	" ██▄▄▄▄██",
	"▄▄▄▄  ▄▄▄▄",
	"▀▀▀▀  ▀▀▀▀",
}

// logoLayer is the layer each row draws, for its shade.
var logoLayer = []int{0, 1, 1, 2, 3, 3}

// logoWidth is the widest row, so text set beside the mark lines up.
const logoWidth = 10

// logoTextRow is the first row text sits beside, centring four lines on the
// six-row mark.
const logoTextRow = 1

// layerColors are the logo's own layer shades, top layer first: white to
// silver on a dark background (logo.svg), black to graphite on a light one.
func (t Theme) layerColors(dark bool) []color.Color {
	pick := lipgloss.LightDark(dark)
	hex := lipgloss.Color
	return []color.Color{
		pick(hex("#09090b"), hex("#ffffff")),
		pick(hex("#27272a"), hex("#e8e8e8")),
		pick(hex("#3f3f46"), hex("#d0d0d0")),
		pick(hex("#52525b"), hex("#b8b8b8")),
	}
}

// Logo renders the mark with up to four lines of text beside it.
func (p *Printer) Logo(beside ...string) string {
	return LogoWith(p.Theme(), p.Dark(), beside...)
}

// LogoWith renders the mark for a theme, with text beside it.
func LogoWith(t Theme, dark bool, beside ...string) string {
	colors := t.layerColors(dark)
	lines := make([]string, len(logoRows))
	for i, row := range logoRows {
		mark := lipgloss.NewStyle().Foreground(colors[logoLayer[i]]).Render(row)
		line := mark + strings.Repeat(" ", logoWidth-lipgloss.Width(row))
		if j := i - logoTextRow; j >= 0 && j < len(beside) && beside[j] != "" {
			line += "   " + beside[j]
		}
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}
