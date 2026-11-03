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

// DiscoveryOptions configures the live view of a running discovery.
type DiscoveryOptions struct {
	Context    context.Context
	Theme      ui.Theme
	Repository string
	Job        *api.DiscoveryJob
	// Status and Pending are polled every Poll while the job runs.
	Status  func(ctx context.Context, jobID string) (*api.DiscoveryJob, error)
	Pending func(ctx context.Context) ([]api.PendingDiscovery, error)
	Poll    time.Duration
	// Frame is the animation step; zero means 70ms.
	Frame time.Duration
}

// Discovery follows a discovery inline: its stages as they pass, then the
// model it found growing as a tree, systems first, one line at a time.
type Discovery struct {
	opts     DiscoveryOptions
	s        styles
	job      *api.DiscoveryJob
	stage    int
	items    []TriageItem
	lines    []TreeLine
	shown    int
	frame    int
	started  time.Time
	failures int
	width    int
	done     bool

	// Err is set when the job could not be followed.
	Err error
	// Detached is set when the user stopped following a running job.
	Detached bool
}

// discoveryStages are the steps a discovery reports, by the progress at which
// each one ends.
var discoveryStages = []struct {
	until int
	label string
}{
	{20, "Reading the repository"},
	{55, "Picking and fetching the source files"},
	{78, "Mapping the architecture with AI"},
	{90, "Finding dependencies, decisions and contracts"},
	{101, "Preparing the review"},
}

type discoveryTick struct{}

type discoveryPolled struct {
	job     *api.DiscoveryJob
	pending []api.PendingDiscovery
	err     error
}

// NewDiscovery follows opts.Job.
func NewDiscovery(opts DiscoveryOptions) *Discovery {
	if opts.Poll <= 0 {
		opts.Poll = 2 * time.Second
	}
	if opts.Context == nil {
		opts.Context = context.Background()
	}
	if opts.Frame <= 0 {
		opts.Frame = 70 * time.Millisecond
	}
	job := *opts.Job
	return &Discovery{opts: opts, s: newStyles(opts.Theme), job: &job, started: time.Now(), width: 100}
}

// Job is the job as last seen.
func (m *Discovery) Job() *api.DiscoveryJob {
	return m.job
}

// Pending is what the job proposes, as last seen.
func (m *Discovery) Pending() []api.PendingDiscovery {
	out := make([]api.PendingDiscovery, len(m.items))
	for i, it := range m.items {
		out[i] = it.PendingDiscovery
	}
	return out
}

func (m *Discovery) Init() tea.Cmd {
	return tea.Batch(m.tick(), m.poll(0))
}

func (m *Discovery) tick() tea.Cmd {
	return tea.Tick(m.opts.Frame, func(time.Time) tea.Msg { return discoveryTick{} })
}

func (m *Discovery) poll(after time.Duration) tea.Cmd {
	jobID := m.job.ID
	fetch := func() tea.Msg {
		ctx := m.opts.Context
		job, err := m.opts.Status(ctx, jobID)
		if err != nil {
			return discoveryPolled{err: err}
		}
		pending, err := m.opts.Pending(ctx)
		return discoveryPolled{job: job, pending: pending, err: err}
	}
	if after == 0 {
		return fetch
	}
	return tea.Tick(after, func(time.Time) tea.Msg { return fetch() })
}

func (m *Discovery) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if !m.job.Done() {
				m.Detached = true
			}
			m.done = true
			return m, tea.Quit
		}
	case discoveryTick:
		m.frame++
		if m.shown < len(m.lines) {
			// Long trees cascade in about three seconds, short ones a line a frame.
			m.shown = min(len(m.lines), m.shown+max(1, len(m.lines)/40))
		}
		if m.job.Done() && m.shown >= len(m.lines) {
			m.done = true
			return m, tea.Quit
		}
		return m, m.tick()
	case discoveryPolled:
		return m, m.polled(msg)
	}
	return m, nil
}

func (m *Discovery) polled(msg discoveryPolled) tea.Cmd {
	if msg.err != nil {
		// A dropped poll is retried; three in a row end the view.
		m.failures++
		if m.failures >= 3 {
			m.Err = msg.err
			m.done = true
			return tea.Quit
		}
		return m.poll(m.opts.Poll)
	}
	m.failures = 0
	repository, branch := m.job.Repository, m.job.Branch
	m.job = msg.job
	m.job.Repository, m.job.Branch = firstOf(m.job.Repository, repository), firstOf(m.job.Branch, branch)
	m.stage = max(m.stage, stageOf(m.job.Progress))
	m.setPending(msg.pending)
	if m.job.Done() {
		if m.job.Status == "completed" {
			m.stage = len(discoveryStages)
		}
		return nil
	}
	return m.poll(m.opts.Poll)
}

// setPending keeps what this job proposes and regrows the tree. Lines already
// on screen stay; new ones join the cascade.
func (m *Discovery) setPending(pending []api.PendingDiscovery) {
	var mine []api.PendingDiscovery
	for _, p := range pending {
		if p.DiscoveryJobID == "" || p.DiscoveryJobID == m.job.ID {
			mine = append(mine, p)
		}
	}
	if len(mine) == len(m.items) {
		return
	}
	m.items = TriageItems(mine)
	m.lines = DiscoveryTree(m.items, 4)
	m.shown = min(m.shown, len(m.lines))
}

func stageOf(progress int) int {
	for i, st := range discoveryStages {
		if progress < st.until {
			return i
		}
	}
	return len(discoveryStages) - 1
}

func (m *Discovery) View() tea.View {
	var b strings.Builder
	name := firstOf(m.opts.Repository, m.job.Repository, "the repository")
	elapsed := time.Since(m.started).Round(time.Second)
	b.WriteString("\n  " + m.s.brand.Render("◆") + " " + m.s.title.Render("Discovering "+name) +
		m.s.muted.Render(fmt.Sprintf("  ·  %d:%02d", int(elapsed.Minutes()), int(elapsed.Seconds())%60)) + "\n\n")

	failed := m.job.Status == "failed"
	for i, st := range discoveryStages {
		var marker, label string
		switch {
		case i < m.stage:
			marker, label = lipgloss.NewStyle().Foreground(m.s.t.Success).Render("✓"), m.s.muted.Render(st.label)
		case i == m.stage && failed:
			marker, label = lipgloss.NewStyle().Foreground(m.s.t.Danger).Render("✗"), m.s.title.Render(st.label)
		case i == m.stage && !m.job.Done():
			marker, label = m.s.accent.Render(spinnerFrames[m.frame%len(spinnerFrames)]), m.s.title.Render(st.label)
		default:
			marker, label = m.s.subtle.Render("·"), m.s.subtle.Render(st.label)
		}
		b.WriteString("  " + marker + " " + label + "\n")
		if i == m.stage && !m.job.Done() {
			if detail := strings.TrimRight(m.job.CurrentStep, ". "); detail != "" {
				b.WriteString("      " + m.s.muted.Render(cut(detail, m.width-8)) + "\n")
			}
		}
	}
	if failed {
		b.WriteString("      " + lipgloss.NewStyle().Foreground(m.s.t.Danger).Render(cut(firstOf(m.job.Error, "the discovery failed"), m.width-8)) + "\n")
	}

	if !m.job.Done() {
		width := min(40, max(10, m.width-14))
		filled := m.job.Progress * width / 100
		b.WriteString("\n  " + m.s.accent.Render(strings.Repeat("━", filled)) + m.s.subtle.Render(strings.Repeat("━", width-filled)) +
			" " + m.s.title.Render(fmt.Sprintf("%3d%%", m.job.Progress)) + "\n")
	}

	if m.shown > 0 {
		b.WriteString("\n")
		for i, line := range m.lines[:m.shown] {
			b.WriteString("  " + m.renderLine(line, i == m.shown-1 && m.shown < len(m.lines)) + "\n")
		}
		b.WriteString("\n  " + m.s.muted.Render(m.counts()) + "\n")
	} else if m.job.Status == "completed" {
		b.WriteString("\n  " + m.s.muted.Render("Nothing new: the model already holds what it found.") + "\n")
	}

	if !m.done {
		b.WriteString("\n  " + m.s.hints("q", "stop following (the discovery goes on)") + "\n")
	}
	return tea.NewView(b.String())
}

// renderLine draws a tree line; the newest one is lit as it lands.
func (m *Discovery) renderLine(line TreeLine, landing bool) string {
	name := m.s.title.Render(line.Name)
	if landing {
		name = m.s.selected.Render(line.Name)
	}
	if line.More > 0 {
		name = m.s.muted.Render(fmt.Sprintf("+%d more", line.More))
	}
	out := m.s.subtle.Render(line.Prefix) + m.s.glyph(line.Level) + " " + name
	if line.Detail != "" {
		out += "  " + m.s.muted.Render(cut(line.Detail, 32))
	}
	return out
}

// counts tallies what is on screen by level, then what the tree leaves out.
func (m *Discovery) counts() string {
	byLevel := map[string]int{}
	for _, line := range m.lines[:m.shown] {
		byLevel[line.Level] += max(1, line.More)
	}
	if m.shown >= len(m.lines) {
		byLevel = map[string]int{}
		for _, it := range m.items {
			byLevel[it.ElementType]++
		}
	}
	return DiscoveryCounts(byLevel)
}

// DiscoveryCounts reads "1 system · 4 containers · …" in review order.
func DiscoveryCounts(byLevel map[string]int) string {
	var parts []string
	for _, g := range triageGroups {
		if n := byLevel[g]; n > 0 {
			parts = append(parts, ui.Plural(n, levelNoun(g), levelNoun(g)+"s"))
		}
	}
	return strings.Join(parts, " · ")
}

func levelNoun(elementType string) string {
	switch elementType {
	case "code":
		return "code element"
	case "overlay":
		return "group"
	case "api_contract":
		return "API contract"
	case "adr":
		return "decision"
	}
	return elementType
}

// TreeLine is one line of the discovered tree.
type TreeLine struct {
	Prefix string
	Level  string
	Name   string
	Detail string
	// More is set on the line standing for elements left out.
	More int
}

// DiscoveryTree lays pending systems, containers and components out as a
// tree, at most perParent components under each container. Elements whose
// parent was not proposed hang at the root.
func DiscoveryTree(items []TriageItem, perParent int) []TreeLine {
	children := map[string][]TriageItem{}
	var roots []TriageItem
	names := map[string]bool{}
	for _, it := range items {
		if it.ElementType == "system" || it.ElementType == "container" {
			names[it.ElementType+":"+strings.ToLower(it.Name())] = true
		}
	}
	for _, it := range items {
		switch it.ElementType {
		case "system":
			roots = append(roots, it)
		case "container":
			if parent := strings.ToLower(it.Parent()); names["system:"+parent] {
				children["system:"+parent] = append(children["system:"+parent], it)
			} else {
				roots = append(roots, it)
			}
		case "component":
			if parent := strings.ToLower(it.Parent()); names["container:"+parent] {
				children["container:"+parent] = append(children["container:"+parent], it)
			} else {
				roots = append(roots, it)
			}
		}
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return triageGroupRank(roots[i].ElementType) < triageGroupRank(roots[j].ElementType)
	})

	var lines []TreeLine
	var walk func(it TriageItem, indent string, last, root bool)
	walk = func(it TriageItem, indent string, last, root bool) {
		branch, next := "├─ ", "│  "
		if last {
			branch, next = "└─ ", "   "
		}
		if root {
			branch, next = "", ""
		}
		lines = append(lines, TreeLine{Prefix: indent + branch, Level: it.ElementType, Name: it.Name(), Detail: it.Element.Technology})
		kids := children[it.ElementType+":"+strings.ToLower(it.Name())]
		limit := len(kids)
		if it.ElementType == "container" && perParent > 0 && len(kids) > perParent {
			limit = perParent - 1
		}
		for i, kid := range kids[:limit] {
			walk(kid, indent+next, i == len(kids)-1, false)
		}
		if limit < len(kids) {
			lines = append(lines, TreeLine{Prefix: indent + next + "└─ ", Level: "component", More: len(kids) - limit})
		}
	}
	for i, it := range roots {
		walk(it, "", i == len(roots)-1, true)
	}
	return lines
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
