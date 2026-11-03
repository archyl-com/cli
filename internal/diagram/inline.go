package diagram

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// Protocol is a way to show an image inline in a terminal.
type Protocol int

const (
	// NoImages means the terminal shows text only.
	NoImages Protocol = iota
	// Kitty is the Kitty graphics protocol (Kitty, Ghostty).
	Kitty
	// ITerm is iTerm2's inline images (iTerm2, WezTerm).
	ITerm
)

// String names the protocol.
func (p Protocol) String() string {
	switch p {
	case Kitty:
		return "kitty"
	case ITerm:
		return "iterm2"
	}
	return "none"
}

// DetectProtocol reads the environment for a terminal that shows images.
// ARCHYL_IMAGES=0 turns images off; =kitty or =iterm2 forces a protocol.
// Inside tmux only iTerm2's protocol is used, through passthrough (it needs
// "set -g allow-passthrough on"); Kitty's needs the placeholders tmux lacks.
func DetectProtocol(getenv func(string) string) Protocol {
	switch strings.ToLower(getenv("ARCHYL_IMAGES")) {
	case "0", "off", "false", "no":
		return NoImages
	case "kitty":
		return Kitty
	case "iterm2", "iterm":
		return ITerm
	}
	term, program := getenv("TERM"), getenv("TERM_PROGRAM")
	iterm := program == "iTerm.app" || getenv("LC_TERMINAL") == "iTerm2"
	wezterm := program == "WezTerm" || getenv("WEZTERM_EXECUTABLE") != ""
	kitty := term == "xterm-kitty" || getenv("KITTY_WINDOW_ID") != ""
	ghostty := program == "ghostty" || term == "xterm-ghostty" || getenv("GHOSTTY_RESOURCES_DIR") != ""
	if InTmux(getenv) {
		if iterm || wezterm {
			return ITerm
		}
		return NoImages
	}
	switch {
	case iterm, wezterm:
		return ITerm
	case kitty, ghostty:
		return Kitty
	}
	return NoImages
}

// InTmux reports whether the program runs inside tmux.
func InTmux(getenv func(string) string) bool {
	return getenv("TMUX") != "" || strings.HasPrefix(getenv("TERM"), "tmux")
}

// WriteInline writes a PNG for the terminal to show, cols cells wide, and a
// newline after it.
func WriteInline(w io.Writer, p Protocol, png []byte, cols int, tmux bool) error {
	var seq string
	encoded := base64.StdEncoding.EncodeToString(png)
	switch p {
	case Kitty:
		var b strings.Builder
		const chunk = 4096
		for i := 0; i < len(encoded); i += chunk {
			end := min(i+chunk, len(encoded))
			more := 1
			if end == len(encoded) {
				more = 0
			}
			if i == 0 {
				fmt.Fprintf(&b, "\x1b_Ga=T,f=100,q=2,c=%d,m=%d;%s\x1b\\", cols, more, encoded[i:end])
			} else {
				fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, encoded[i:end])
			}
		}
		seq = b.String()
	case ITerm:
		seq = fmt.Sprintf("\x1b]1337;File=inline=1;size=%d;width=%d;preserveAspectRatio=1:%s\a", len(png), cols, encoded)
		if tmux {
			seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
		}
	default:
		return fmt.Errorf("this terminal does not show images")
	}
	_, err := io.WriteString(w, seq+"\n")
	return err
}

// InlineColumns is how many cells an image of a logical width takes: about
// eight pixels a cell, never wider than the terminal.
func InlineColumns(logicalWidth, termWidth int) int {
	cols := (logicalWidth + 7) / 8
	if termWidth > 0 {
		cols = min(cols, termWidth-1)
	}
	return max(cols, 20)
}
