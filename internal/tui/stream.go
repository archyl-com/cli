package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/archyl-com/cli/internal/ui"
)

// Markdown renders markdown for the terminal in the CLI's light or dark style.
func Markdown(md string, width int, dark bool) string {
	style := "light"
	if dark {
		style = "dark"
	}
	md = ui.Sanitize(md)
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(min(max(width-2, 40), 100)))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return ui.Sanitize(strings.TrimRight(out, "\n"))
}

// StreamChunk carries one piece of a streamed answer; Done ends the stream.
type StreamChunk struct {
	Text string
	Err  error
	Done bool
}

// Stream renders a markdown answer while it is being written. It runs inline,
// so the final answer stays in the scrollback.
type Stream struct {
	s       styles
	dark    bool
	title   string
	chunks  <-chan StreamChunk
	text    strings.Builder
	frame   int
	width   int
	done    bool
	started time.Time
	// Err is the stream's error, if any.
	Err error
}

type streamFrame struct{}

// NewStream reads chunks until Done.
func NewStream(t ui.Theme, dark bool, title string, chunks <-chan StreamChunk) *Stream {
	return &Stream{s: newStyles(t), dark: dark, title: title, chunks: chunks, width: 100, started: time.Now()}
}

func (m *Stream) next() tea.Cmd {
	return func() tea.Msg { return <-m.chunks }
}

func tickFrame() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return streamFrame{} })
}

// Init implements tea.Model.
func (m *Stream) Init() tea.Cmd {
	return tea.Batch(m.next(), tickFrame())
}

// Update implements tea.Model.
func (m *Stream) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" || msg.String() == "esc" {
			m.done = true
			return m, tea.Quit
		}
	case streamFrame:
		if m.done {
			return m, nil
		}
		m.frame++
		return m, tickFrame()
	case StreamChunk:
		if msg.Err != nil {
			m.Err, m.done = msg.Err, true
			return m, tea.Quit
		}
		if msg.Done {
			m.done = true
			return m, tea.Quit
		}
		m.text.WriteString(msg.Text)
		return m, m.next()
	}
	return m, nil
}

// View implements tea.Model.
func (m *Stream) View() tea.View {
	head := m.s.brand.Render("◆ archyl") + m.s.muted.Render("  "+m.title)
	if !m.done {
		dots := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		state := "thinking"
		if m.text.Len() > 0 {
			state = "writing"
		}
		head += "  " + m.s.accent.Render(dots[m.frame%len(dots)]) + m.s.muted.Render(" "+state+"…")
	} else {
		head += m.s.muted.Render("  · " + time.Since(m.started).Round(100*time.Millisecond).String())
	}
	body := ""
	if m.text.Len() > 0 {
		body = Markdown(m.text.String(), m.width, m.dark)
	}
	return tea.NewView(lipgloss.JoinVertical(lipgloss.Left, "", head, body, ""))
}

// Text is the full answer received so far.
func (m *Stream) Text() string {
	return m.text.String()
}
