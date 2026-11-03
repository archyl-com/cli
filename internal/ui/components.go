package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Brand is the wordmark.
func (p *Printer) Brand() string {
	t := p.Theme()
	return lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Render("◆ archyl")
}

// Heading is a section title: flat, bold, indented like the content under it.
func (p *Printer) Heading(title string) string {
	return "  " + lipgloss.NewStyle().Bold(true).Foreground(p.Theme().Text).Render(strings.TrimSpace(title))
}

// Muted renders secondary text.
func (p *Printer) Muted(s string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().Muted).Render(s)
}

// Bold renders emphasized text.
func (p *Printer) Bold(s string) string {
	return lipgloss.NewStyle().Bold(true).Render(s)
}

// Accent renders text in the brand color.
func (p *Printer) Accent(s string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().Accent).Render(s)
}

// Success, Warning and Danger render status text.
func (p *Printer) Success(s string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().Success).Render(s)
}

func (p *Printer) Warning(s string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().Warning).Render(s)
}

func (p *Printer) Danger(s string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().Danger).Render(s)
}

// Code renders a command or path the user may copy.
func (p *Printer) Code(s string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().Accent).Render(s)
}

// Element renders a C4 element: level glyph in the level's hue, then the name.
func (p *Printer) Element(level, name string) string {
	t := p.Theme()
	glyph := lipgloss.NewStyle().Foreground(t.LevelColor(level)).Render(LevelGlyph(level))
	return glyph + " " + lipgloss.NewStyle().Bold(true).Foreground(t.Text).Render(name)
}

// Level renders a level name in its hue, for legends and labels.
func (p *Printer) Level(level string) string {
	return lipgloss.NewStyle().Foreground(p.Theme().LevelColor(level)).Render(level)
}

// Check, Cross and Dot are status markers.
func (p *Printer) Check() string { return p.Success("✓") }
func (p *Printer) Cross() string { return p.Danger("✗") }
func (p *Printer) Bang() string  { return p.Warning("!") }

// Hint suggests the next command.
func (p *Printer) Hint(text, command string) string {
	return p.Muted("→ "+text+" ") + p.Code(command)
}

// KeyValues renders aligned "key  value" rows.
func (p *Printer) KeyValues(rows [][2]string) string {
	width := 0
	for _, r := range rows {
		width = max(width, lipgloss.Width(r[0]))
	}
	keyStyle := lipgloss.NewStyle().Foreground(p.Theme().Muted).Width(width + 2)
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, keyStyle.Render(r[0])+r[1])
	}
	return strings.Join(lines, "\n")
}

// Gauge renders a 0-100 score as a bar, colored by health.
func (p *Printer) Gauge(score, width int) string {
	score = min(max(score, 0), 100)
	filled := score * width / 100
	c := p.Theme().Success
	switch {
	case score < 60:
		c = p.Theme().Danger
	case score < 80:
		c = p.Theme().Warning
	}
	bar := lipgloss.NewStyle().Foreground(c).Render(strings.Repeat("━", filled)) +
		lipgloss.NewStyle().Foreground(p.Theme().Subtle).Render(strings.Repeat("━", width-filled))
	return bar + " " + lipgloss.NewStyle().Foreground(c).Bold(true).Render(fmt.Sprintf("%d", score))
}

// Panel sets a block of content apart. Flat: no frame, just a margin and
// breathing room above and below.
func (p *Printer) Panel(content string) string {
	return "\n" + lipgloss.NewStyle().PaddingLeft(2).Render(content) + "\n"
}

// Plural returns "1 file" / "3 files".
func Plural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
