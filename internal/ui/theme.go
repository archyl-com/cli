// Package ui renders the CLI's output: one palette, a handful of components,
// and the rules for when to draw them (terminal) or not (pipes, --json).
package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme is the CLI palette, resolved for the terminal's background.
type Theme struct {
	Accent    color.Color
	Text      color.Color
	Muted     color.Color
	Subtle    color.Color
	Success   color.Color
	Warning   color.Color
	Danger    color.Color
	System    color.Color
	Container color.Color
	Component color.Color
	Code      color.Color
}

// NewTheme returns the palette for a dark or light background. The accent is
// Archyl's indigo; the four C4 levels each get their own hue so a tree reads
// at a glance.
func NewTheme(dark bool) Theme {
	return NewThemeFunc(lipgloss.LightDark(dark))
}

// NewThemeFunc resolves the palette with a light/dark chooser.
func NewThemeFunc(pick lipgloss.LightDarkFunc) Theme {
	hex := lipgloss.Color
	return Theme{
		Accent:    pick(hex("#4f46e5"), hex("#818cf8")),
		Text:      pick(hex("#18181b"), hex("#fafafa")),
		Muted:     pick(hex("#71717a"), hex("#a1a1aa")),
		Subtle:    pick(hex("#d4d4d8"), hex("#3f3f46")),
		Success:   pick(hex("#16a34a"), hex("#4ade80")),
		Warning:   pick(hex("#d97706"), hex("#fbbf24")),
		Danger:    pick(hex("#dc2626"), hex("#f87171")),
		System:    pick(hex("#7c3aed"), hex("#a78bfa")),
		Container: pick(hex("#0284c7"), hex("#38bdf8")),
		Component: pick(hex("#0d9488"), hex("#2dd4bf")),
		Code:      pick(hex("#c2410c"), hex("#fb923c")),
	}
}

// LevelGlyph is the marker drawn before an element of a C4 level.
func LevelGlyph(level string) string {
	switch level {
	case "system":
		return "◆"
	case "container":
		return "▣"
	case "component":
		return "◉"
	case "code":
		return "ƒ"
	default:
		return "•"
	}
}

// LevelColor is the hue of a C4 level.
func (t Theme) LevelColor(level string) color.Color {
	switch level {
	case "system":
		return t.System
	case "container":
		return t.Container
	case "component":
		return t.Component
	case "code":
		return t.Code
	default:
		return t.Muted
	}
}
