// Package tui holds the CLI's full-screen views: the model explorer, the
// finder and the home screen. They share one palette with the rest of the CLI.
package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/archyl-com/cli/internal/ui"
)

// styles are the lipgloss styles every view draws with.
type styles struct {
	t         ui.Theme
	brand     lipgloss.Style
	title     lipgloss.Style
	muted     lipgloss.Style
	subtle    lipgloss.Style
	accent    lipgloss.Style
	selected  lipgloss.Style
	key       lipgloss.Style
	panel     lipgloss.Style
	focused   lipgloss.Style
	rule      lipgloss.Style
	section   lipgloss.Style
	badge     lipgloss.Style
	highlight lipgloss.Style
}

func newStyles(t ui.Theme) styles {
	return styles{
		t:        t,
		brand:    lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		title:    lipgloss.NewStyle().Foreground(t.Text).Bold(true),
		muted:    lipgloss.NewStyle().Foreground(t.Muted),
		subtle:   lipgloss.NewStyle().Foreground(t.Subtle),
		accent:   lipgloss.NewStyle().Foreground(t.Accent),
		selected: lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		key:      lipgloss.NewStyle().Foreground(t.Accent).Bold(true),
		// Flat: panes are separated by a hairline, never boxed.
		panel:   lipgloss.NewStyle().Padding(1, 2),
		focused: lipgloss.NewStyle().Padding(1, 2),
		rule: lipgloss.NewStyle().Padding(1, 2).
			Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(t.Subtle),
		section:   lipgloss.NewStyle().Foreground(t.Muted).Bold(true),
		badge:     lipgloss.NewStyle().Foreground(t.Muted).Border(lipgloss.NormalBorder(), false, false, false, false),
		highlight: lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Underline(true),
	}
}

// glyph is the level marker in the level's hue.
func (s styles) glyph(level string) string {
	return lipgloss.NewStyle().Foreground(s.t.LevelColor(level)).Render(ui.LevelGlyph(level))
}

// element is "glyph Name".
func (s styles) element(level, name string) string {
	return s.glyph(level) + " " + s.title.Render(name)
}

// hints renders the footer: "key action · key action".
func (s styles) hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, s.key.Render(pairs[i])+" "+s.muted.Render(pairs[i+1]))
	}
	return strings.Join(parts, s.subtle.Render("  ·  "))
}

// input is a text input in the CLI palette.
func (s styles) input(placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = "/ "
	in.Placeholder = placeholder
	st := in.Styles()
	st.Focused.Prompt = s.key
	st.Blurred.Prompt = s.muted
	st.Focused.Placeholder = s.muted
	st.Focused.Text = s.title
	in.SetStyles(st)
	return in
}

// fit truncates or pads s to exactly width cells.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > width {
		return ansi.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}

// wrap word-wraps plain text to width.
func wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return nil
	}
	// Wrap, not Wordwrap: a word longer than the width (a path, a URL, a
	// hyphenated pair) is broken rather than left to overflow the panel.
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}

// highlightRunes marks the matched rune positions of s.
func highlightRunes(s string, matched []int, base, hit lipgloss.Style) string {
	if len(matched) == 0 {
		return base.Render(s)
	}
	set := make(map[int]bool, len(matched))
	for _, i := range matched {
		set[i] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if set[i] {
			b.WriteString(hit.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

func ansiStrip(s string) string {
	return ansi.Strip(s)
}

// cut shortens text to n runes with an ellipsis.
func cut(s string, n int) string {
	if n <= 1 {
		return ""
	}
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
