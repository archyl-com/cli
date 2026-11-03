package ui

import (
	"fmt"
	"os"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spin runs fn while animating a spinner with label on stderr. Without a
// terminal on stderr it just runs fn.
func (p *Printer) Spin(label string, fn func() error) error {
	if p.JSON || !term.IsTerminal(os.Stderr.Fd()) {
		return fn()
	}

	done := make(chan error, 1)
	go func() { done <- fn() }()

	style := lipgloss.NewStyle().Foreground(p.Theme().Accent)
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for frame := 0; ; frame++ {
		select {
		case err := <-done:
			_, _ = fmt.Fprint(p.Err, "\r\033[2K")
			return err
		case <-ticker.C:
			_, _ = lipgloss.Fprint(p.Err, "\r\033[2K"+style.Render(spinnerFrames[frame%len(spinnerFrames)])+" "+p.Muted(label))
		}
	}
}
