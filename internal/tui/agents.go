package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// AgentsOptions configures the live agents view.
type AgentsOptions struct {
	Theme   ui.Theme
	Project string
	// Sessions loads sessions: active ones, or recent ones when all is set.
	Sessions func(ctx context.Context, all bool) ([]api.Session, error)
	// Files loads the files a session changed.
	Files func(ctx context.Context, sessionID string) ([]api.SessionFile, error)
	Every time.Duration
}

// Agents is the live view behind `archyl agents`.
type Agents struct {
	opts     AgentsOptions
	s        styles
	sessions []api.Session
	files    map[string][]api.SessionFile
	cursor   int
	all      bool
	err      error
	updated  time.Time
	width    int
	height   int
	loading  bool
	pollSeq  int
	frame    int
}

type agentsLoaded struct {
	seq      int
	sessions []api.Session
	err      error
}

type filesLoaded struct {
	id    string
	files []api.SessionFile
}

type agentsTick struct{ seq int }

// NewAgents starts polling at once.
func NewAgents(opts AgentsOptions) *Agents {
	if opts.Every == 0 {
		opts.Every = 3 * time.Second
	}
	return &Agents{opts: opts, s: newStyles(opts.Theme), files: map[string][]api.SessionFile{}, loading: true}
}

func (m *Agents) load() tea.Cmd {
	seq, all := m.pollSeq, m.all
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sessions, err := m.opts.Sessions(ctx, all)
		return agentsLoaded{seq: seq, sessions: sessions, err: err}
	}
}

func (m *Agents) loadFiles(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		files, _ := m.opts.Files(ctx, id)
		return filesLoaded{id: id, files: files}
	}
}

func (m *Agents) tick() tea.Cmd {
	seq := m.pollSeq
	return tea.Tick(m.opts.Every, func(time.Time) tea.Msg { return agentsTick{seq: seq} })
}

// Init implements tea.Model.
func (m *Agents) Init() tea.Cmd {
	return m.load()
}

func (m *Agents) current() *api.Session {
	if m.cursor < 0 || m.cursor >= len(m.sessions) {
		return nil
	}
	return &m.sessions[m.cursor]
}

// Update implements tea.Model.
func (m *Agents) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case agentsLoaded:
		if msg.seq != m.pollSeq {
			return m, nil
		}
		m.loading, m.err, m.updated = false, msg.err, time.Now()
		if msg.err == nil {
			selected := ""
			if cur := m.current(); cur != nil {
				selected = cur.ID
			}
			m.sessions = msg.sessions
			sort.SliceStable(m.sessions, func(i, j int) bool {
				ai, aj := m.sessions[i].Status == "active", m.sessions[j].Status == "active"
				if ai != aj {
					return ai
				}
				return m.sessions[i].StartedAt.After(m.sessions[j].StartedAt)
			})
			m.cursor = 0
			for i, s := range m.sessions {
				if s.ID == selected {
					m.cursor = i
				}
			}
		}
		cmds := []tea.Cmd{m.tick()}
		if cur := m.current(); cur != nil {
			cmds = append(cmds, m.loadFiles(cur.ID))
		}
		return m, tea.Batch(cmds...)
	case filesLoaded:
		m.files[msg.id] = msg.files
	case agentsTick:
		if msg.seq != m.pollSeq {
			return m, nil
		}
		m.frame++
		return m, m.load()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.cursor = max(m.cursor-1, 0)
		case "down", "j":
			m.cursor = min(m.cursor+1, max(len(m.sessions)-1, 0))
		case "a":
			m.all = !m.all
			m.pollSeq++
			m.loading = true
			return m, m.load()
		case "r":
			m.pollSeq++
			return m, m.load()
		}
		if cur := m.current(); cur != nil {
			if _, ok := m.files[cur.ID]; !ok {
				return m, m.loadFiles(cur.ID)
			}
		}
	}
	return m, nil
}

// conflicts maps an element name to the active sessions leasing it, when
// more than one does.
func (m *Agents) conflicts() map[string][]string {
	holders := map[string][]string{}
	for _, s := range m.sessions {
		if s.Status != "active" {
			continue
		}
		for _, l := range s.Leases {
			holders[l.ElementName] = append(holders[l.ElementName], s.AgentName)
		}
	}
	for name, list := range holders {
		if len(list) < 2 {
			delete(holders, name)
		}
	}
	return holders
}

// View implements tea.Model.
func (m *Agents) View() tea.View {
	if m.width == 0 {
		return tea.NewView("")
	}
	active := 0
	for _, s := range m.sessions {
		if s.Status == "active" {
			active++
		}
	}
	left := m.s.brand.Render("◆ archyl") + m.s.muted.Render("  agents  ") + m.s.title.Render(m.opts.Project)
	pulse := m.s.accent.Render([]string{"●", "◉"}[m.frame%2])
	right := pulse + m.s.muted.Render(fmt.Sprintf(" %d at work · updated %s", active, since(m.updated)))
	header := " " + left + strings.Repeat(" ", max(m.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)) + right

	conflicts := m.conflicts()
	if len(conflicts) > 0 {
		var names []string
		for name := range conflicts {
			names = append(names, name)
		}
		sort.Strings(names)
		warn := lipgloss.NewStyle().Foreground(m.s.t.Warning)
		header += "\n " + warn.Render("⚠ "+fmt.Sprintf("%d element(s) leased by several agents: ", len(names))) +
			m.s.title.Render(strings.Join(names, ", "))
	}

	footer := " " + m.s.hints("↑↓", "choose", "a", map[bool]string{false: "show recent", true: "only active"}[m.all], "r", "refresh", "q", "quit")
	bodyHeight := max(m.height-lipgloss.Height(header)-lipgloss.Height(footer), 6)
	listWidth := min(max(m.width*11/20, 40), m.width-30)

	list := m.s.focused.Width(listWidth).Height(bodyHeight).Render(m.listView(listWidth-4, bodyHeight-2, conflicts))
	detail := m.s.panel.Width(m.width - listWidth).Height(bodyHeight).Render(m.detailView(m.width-listWidth-4, bodyHeight-2, conflicts))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, lipgloss.JoinHorizontal(lipgloss.Top, list, detail), footer))
	v.AltScreen = true
	v.WindowTitle = fmt.Sprintf("archyl agents · %d at work", active)
	return v
}

func (m *Agents) listView(width, height int, conflicts map[string][]string) string {
	switch {
	case m.loading && len(m.sessions) == 0:
		return m.s.muted.Render("Loading…")
	case m.err != nil && len(m.sessions) == 0:
		return lipgloss.NewStyle().Foreground(m.s.t.Danger).Render("✗ " + m.err.Error())
	case len(m.sessions) == 0:
		return m.s.title.Render("No agent at work.") + "\n\n" + m.s.muted.Render("Put one to work:") + "\n" + m.s.accent.Render("archyl wrap -- claude")
	}
	var lines []string
	for i, s := range m.sessions {
		if len(lines) >= height-1 {
			lines = append(lines, m.s.muted.Render(fmt.Sprintf("+%d more", len(m.sessions)-i)))
			break
		}
		dot := m.gateDot(s)
		title := dot + " " + m.s.title.Render(s.AgentName) + "  " + m.s.muted.Render(cut(s.TaskText, width-len(s.AgentName)-14))
		meta := "   " + m.s.muted.Render(sessionAge(s))
		var chips []string
		for j, l := range s.Leases {
			if j == 3 {
				chips = append(chips, m.s.muted.Render(fmt.Sprintf("+%d", len(s.Leases)-3)))
				break
			}
			chip := m.s.glyph(l.ElementType) + " " + l.ElementName
			if _, shared := conflicts[l.ElementName]; shared && s.Status == "active" {
				chip = lipgloss.NewStyle().Foreground(m.s.t.Warning).Render("⚠ ") + chip
			}
			chips = append(chips, chip)
		}
		if len(chips) > 0 {
			meta += m.s.muted.Render("  ·  ") + strings.Join(chips, m.s.muted.Render(", "))
		}
		if i == m.cursor {
			lines = append(lines, m.s.selected.Render(fit("› "+ansiStrip(title), width)), m.s.selected.Render(fit("› "+ansiStrip(meta), width)))
		} else {
			lines = append(lines, fit("  "+title, width), fit("  "+meta, width))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m *Agents) gateDot(s api.Session) string {
	switch {
	case s.Status != "active":
		return m.s.muted.Render("○")
	case s.Gate == "deny":
		return lipgloss.NewStyle().Foreground(m.s.t.Danger).Render("●")
	case s.Gate == "warn":
		return lipgloss.NewStyle().Foreground(m.s.t.Warning).Render("●")
	}
	return lipgloss.NewStyle().Foreground(m.s.t.Success).Render("●")
}

func (m *Agents) detailView(width, height int, conflicts map[string][]string) string {
	s := m.current()
	if s == nil {
		return m.s.muted.Render("Sessions open with `archyl wrap` or an agent's start_work_session call.")
	}
	var lines []string
	add := func(l ...string) { lines = append(lines, l...) }
	add(m.gateDot(*s)+" "+m.s.title.Render(s.AgentName)+m.s.muted.Render("  "+s.Status), "")
	add(wrap(s.TaskText, width)...)
	add("")
	add(m.s.muted.Render(fmt.Sprintf("%-10s", "started")) + since(s.StartedAt) + " ago")
	if s.Status == "active" {
		add(m.s.muted.Render(fmt.Sprintf("%-10s", "heartbeat")) + since(s.LastHeartbeatAt) + " ago")
	}
	for _, r := range s.GateReasons {
		for i, line := range wrap(r, width-2) {
			prefix := "  "
			if i == 0 {
				prefix = lipgloss.NewStyle().Foreground(m.s.t.Warning).Render("! ")
			}
			add(prefix + m.s.muted.Render(line))
		}
	}

	if len(s.Leases) > 0 {
		add("", m.s.section.Render(fmt.Sprintf("WORKING ON  %d", len(s.Leases))))
		for _, l := range s.Leases {
			line := "  " + m.s.element(l.ElementType, l.ElementName)
			if l.Source != "" {
				line += m.s.muted.Render("  " + l.Source)
			}
			if others, shared := conflicts[l.ElementName]; shared && s.Status == "active" {
				var rivals []string
				for _, o := range others {
					if o != s.AgentName {
						rivals = append(rivals, o)
					}
				}
				line += lipgloss.NewStyle().Foreground(m.s.t.Warning).Render("  ⚠ also " + strings.Join(rivals, ", "))
			}
			add(fit(line, width))
		}
	}

	files := m.files[s.ID]
	if len(files) > 0 {
		add("", m.s.section.Render(fmt.Sprintf("FILES CHANGED  %d", len(files))))
		for i, f := range files {
			if i == 10 {
				add(m.s.muted.Render(fmt.Sprintf("  +%d more", len(files)-10)))
				break
			}
			sign := map[string]string{"added": "+", "deleted": "−"}[f.Status]
			if sign == "" {
				sign = "~"
			}
			add(fit("  "+m.s.accent.Render(sign)+" "+f.Path, width))
		}
	}
	if len(lines) > height {
		lines = append(lines[:height-1], m.s.muted.Render("…"))
	}
	return strings.Join(lines, "\n")
}

func sessionAge(s api.Session) string {
	if s.Status == "active" {
		return "working for " + since(s.StartedAt)
	}
	if s.EndedAt != nil {
		return s.Status + " " + since(*s.EndedAt) + " ago"
	}
	return s.Status
}

// since renders an elapsed time compactly: 42s, 7m, 3h, 2d.
func since(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
