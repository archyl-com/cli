package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// Printer writes command output. Styled text goes through lipgloss's
// color-profile writer, which strips styling when the output is not a
// terminal or NO_COLOR is set; --json bypasses rendering entirely.
type Printer struct {
	Out  io.Writer
	Err  io.Writer
	JSON bool
	// Embedded is set when the archyl shell runs the command and shows its
	// output: render richly, but never prompt, spin or take the screen.
	Embedded bool

	themeOnce sync.Once
	theme     Theme
	dark      bool
}

// NewPrinter writes to stdout and stderr.
func NewPrinter(jsonOutput bool) *Printer {
	return &Printer{Out: os.Stdout, Err: os.Stderr, JSON: jsonOutput, Embedded: os.Getenv("ARCHYL_EMBEDDED") != ""}
}

// Width is the terminal width, or COLUMNS when the output is captured.
func (p *Printer) Width() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 {
		return w
	}
	return 100
}

// Theme returns the palette, querying the terminal background once.
func (p *Printer) Theme() Theme {
	p.themeOnce.Do(func() {
		p.dark = detectDark(p.Interactive())
		p.theme = NewTheme(p.dark)
	})
	return p.theme
}

// Interactive reports whether a person is at the terminal: prompts, browser
// opening and spinners are only offered then.
func (p *Printer) Interactive() bool {
	return !p.JSON && !p.Embedded && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// Println writes a styled line to stdout.
func (p *Printer) Println(a ...any) {
	_, _ = lipgloss.Fprint(p.Out, Sanitize(fmt.Sprintln(a...)))
}

// Printf writes styled formatted text to stdout.
func (p *Printer) Printf(format string, a ...any) {
	_, _ = lipgloss.Fprint(p.Out, Sanitize(fmt.Sprintf(format, a...)))
}

// Eprintln writes a styled line to stderr, where status chatter belongs so
// stdout stays clean for pipes.
func (p *Printer) Eprintln(a ...any) {
	_, _ = lipgloss.Fprint(p.Err, Sanitize(fmt.Sprintln(a...)))
}

// WriteJSON prints v as indented JSON.
func (p *Printer) WriteJSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Dark reports whether the terminal background is dark.
func (p *Printer) Dark() bool {
	p.Theme()
	return p.dark
}

// detectDark decides light or dark without a round trip when it can:
// ARCHYL_THEME, then COLORFGBG (set by many terminals), then asking the
// terminal, which costs a moment on those that never answer.
func detectDark(interactive bool) bool {
	switch strings.ToLower(os.Getenv("ARCHYL_THEME")) {
	case "dark":
		return true
	case "light":
		return false
	}
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			return bg < 7 || bg == 8
		}
	}
	if !interactive {
		return true
	}
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}
