package tui

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/history"
	"github.com/archyl-com/cli/internal/ui"
)

// Replay speeds.
const (
	// DefaultFrameInterval is how long a frame shows by default.
	DefaultFrameInterval = 1500 * time.Millisecond
	minFrameInterval     = 200 * time.Millisecond
	maxFrameInterval     = 10 * time.Second
	speedStep            = 1.5
)

// HistoryOptions configure the replay.
type HistoryOptions struct {
	Project string
	Frames  []history.Frame
	Theme   ui.Theme
	// Interval is how long a frame shows; zero is DefaultFrameInterval.
	Interval time.Duration
	// Note is said under the header, such as that the versions were capped.
	Note string
}

// History replays the model's versions full screen, like the web app's
// time-lapse: each frame is the model at a version, with what changed since
// the frame before marked.
type History struct {
	opts     HistoryOptions
	s        styles
	frame    int
	playing  bool
	interval time.Duration
	// seq tells current ticks from those of a pause or a speed change.
	seq    int
	y      int
	lines  []string
	width  int
	height int
}

// historyTick advances the replay.
type historyTick struct{ seq int }

// NewHistory makes the replay, playing from its first frame.
func NewHistory(opts HistoryOptions) *History {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultFrameInterval
	}
	return &History{opts: opts, s: newStyles(opts.Theme), interval: interval, playing: len(opts.Frames) > 1}
}

// Init implements tea.Model.
func (h *History) Init() tea.Cmd {
	return h.tick()
}

func (h *History) tick() tea.Cmd {
	if !h.playing {
		return nil
	}
	h.seq++
	seq := h.seq
	return tea.Tick(h.interval, func(time.Time) tea.Msg { return historyTick{seq: seq} })
}

// Update implements tea.Model.
func (h *History) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.width, h.height = msg.Width, msg.Height
		h.render()
	case historyTick:
		if msg.seq != h.seq || !h.playing {
			return h, nil
		}
		h.goTo(h.frame + 1)
		if h.frame == len(h.opts.Frames)-1 {
			h.playing = false
			return h, nil
		}
		return h, h.tick()
	case tea.KeyPressMsg:
		return h, h.key(msg.String())
	}
	return h, nil
}

func (h *History) key(k string) tea.Cmd {
	last := len(h.opts.Frames) - 1
	switch k {
	case "q", "ctrl+c", "esc":
		return tea.Quit
	case "space", " ", "p":
		h.playing = !h.playing
		if h.playing && h.frame == last {
			h.goTo(0)
		}
		return h.tick()
	case "right", "l", "n":
		h.playing = false
		h.goTo(h.frame + 1)
	case "left", "h", "b":
		h.playing = false
		h.goTo(h.frame - 1)
	case "g", "home":
		h.goTo(0)
	case "G", "end":
		h.playing = false
		h.goTo(last)
	case "+", "=", "f":
		h.interval = max(time.Duration(float64(h.interval)/speedStep), minFrameInterval)
		return h.tick()
	case "-", "_", "s":
		h.interval = min(time.Duration(float64(h.interval)*speedStep), maxFrameInterval)
		return h.tick()
	case "down", "j":
		h.y++
	case "up", "k":
		h.y--
	}
	h.clamp()
	return nil
}

func (h *History) goTo(frame int) {
	frame = min(max(frame, 0), len(h.opts.Frames)-1)
	if frame != h.frame {
		h.frame, h.y = frame, 0
		h.render()
	}
}

func (h *History) bodyHeight() int {
	return max(h.height-6, 3)
}

func (h *History) clamp() {
	h.y = min(max(h.y, 0), max(len(h.lines)-h.bodyHeight(), 0))
}

// render draws the current frame's diagram at the current width.
func (h *History) render() {
	if h.width == 0 || len(h.opts.Frames) == 0 {
		return
	}
	v := h.opts.Frames[h.frame].View
	if len(v.Nodes) == 0 {
		h.lines = []string{"  " + h.s.muted.Render("The model is empty at this version.")}
		return
	}
	text := diagram.Text(v, diagram.TextOptions{Theme: h.opts.Theme, Color: true, Width: max(h.width-4, 20)})
	h.lines = h.lines[:0]
	for _, l := range strings.Split(text, "\n") {
		h.lines = append(h.lines, "  "+ansi.Truncate(l, max(h.width-2, 1), "…"))
	}
	h.clamp()
}

// View implements tea.Model.
func (h *History) View() tea.View {
	if h.width == 0 {
		return tea.NewView("")
	}
	out := []string{h.header(), " " + h.caption(), h.progress(), ""}
	body := h.bodyHeight()
	for i := h.y; i < len(h.lines) && i < h.y+body; i++ {
		out = append(out, h.lines[i])
	}
	for len(out) < body+4 {
		out = append(out, "")
	}
	out = append(out, " "+h.legend(), " "+h.footer())
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	v.WindowTitle = "archyl history · " + h.opts.Project
	return v
}

// header names the frame's version: REV 12 · 2026-09-15 14:02 · Ada.
func (h *History) header() string {
	if len(h.opts.Frames) == 0 {
		return " " + h.s.brand.Render("◆ archyl") + h.s.muted.Render("  history")
	}
	f := h.opts.Frames[h.frame]
	label := "now"
	var details []string
	if v := f.Version; v != nil {
		label = "REV " + strconv.Itoa(v.Number)
		details = append(details, v.CreatedAt.Local().Format("2006-01-02 15:04"))
		if author := strings.TrimSpace(ui.StripControl(v.Author)); author != "" {
			details = append(details, author)
		}
	}
	left := h.s.brand.Render("◆ archyl") + h.s.muted.Render("  history  ") + h.s.title.Render(label)
	if len(details) > 0 {
		left += h.s.muted.Render("  " + strings.Join(details, " · "))
	}
	right := h.s.muted.Render(strconv.Itoa(h.frame+1) + "/" + strconv.Itoa(len(h.opts.Frames)))
	gap := max(h.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

// caption says what the version did: its description, else what changed.
func (h *History) caption() string {
	if len(h.opts.Frames) == 0 {
		return h.s.muted.Render("No versions to replay.")
	}
	f := h.opts.Frames[h.frame]
	text := ""
	if f.Version != nil {
		text = strings.Join(strings.Fields(ui.StripControl(f.Version.Description)), " ")
	}
	switch {
	case h.frame == 0:
		text = strings.TrimSpace("Where the replay starts. " + text)
	case text == "":
		text = history.Tally(f.Summary)
	default:
		text += h.s.muted.Render(" · " + history.Tally(f.Summary))
	}
	if h.opts.Note != "" && h.frame == 0 {
		text += h.s.muted.Render(" · " + h.opts.Note)
	}
	return ansi.Truncate(text, max(h.width-2, 1), "…")
}

// progress is a bar across the frames with the play state.
func (h *History) progress() string {
	state := "▶ " + seconds(h.interval)
	if !h.playing {
		state = "❚❚ paused"
	}
	width := max(h.width-lipgloss.Width(state)-5, 4)
	done := width
	if n := len(h.opts.Frames); n > 1 {
		done = width * h.frame / (n - 1)
	}
	return " " + h.s.accent.Render(strings.Repeat("━", done)) + h.s.subtle.Render(strings.Repeat("─", width-done)) +
		"  " + h.s.muted.Render(state)
}

func (h *History) legend() string {
	sign := func(c diagram.Change, color lipgloss.Style) string {
		return color.Render(c.Badge()) + " " + h.s.muted.Render(string(c))
	}
	return sign(diagram.Added, lipgloss.NewStyle().Foreground(h.opts.Theme.Success)) + "   " +
		sign(diagram.Changed, lipgloss.NewStyle().Foreground(h.opts.Theme.Warning)) + "   " +
		sign(diagram.Removed, h.s.muted)
}

func (h *History) footer() string {
	play := "pause"
	if !h.playing {
		play = "play"
	}
	return h.s.hints("space", play, "← →", "step", "+ -", "speed", "j k", "scroll", "q", "quit")
}

// seconds is a frame interval as people say it: 1.5s, 0.4s.
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Round(100*time.Millisecond).Seconds(), 'f', -1, 64) + "s"
}
