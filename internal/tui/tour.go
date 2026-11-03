package tui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/tour"
	"github.com/archyl-com/cli/internal/ui"
)

// TourOptions configure the tour view.
type TourOptions struct {
	Tour  *tour.Tour
	Graph *graph.Graph
	Theme ui.Theme
	// Link returns the web app URL of an element; Open opens a URL.
	Link func(id string) string
	Open func(url string) error
}

// Tour is the full-screen guided tour: one stop per page.
type Tour struct {
	opts   TourOptions
	s      styles
	stop   int
	y      int
	lines  []string
	width  int
	height int
	status string
}

// NewTour makes the tour view.
func NewTour(opts TourOptions) *Tour {
	return &Tour{opts: opts, s: newStyles(opts.Theme)}
}

// Init implements tea.Model.
func (t *Tour) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (t *Tour) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width, t.height = msg.Width, msg.Height
		t.render()
	case tea.KeyPressMsg:
		t.status = ""
		last := len(t.opts.Tour.Stops) - 1
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return t, tea.Quit
		case "n", "right", "l", "space", "enter":
			if t.stop == last {
				return t, tea.Quit
			}
			t.goTo(t.stop + 1)
		case "p", "left", "h", "backspace":
			t.goTo(t.stop - 1)
		case "g", "home":
			t.goTo(0)
		case "G", "end":
			t.goTo(last)
		case "down", "j":
			t.y++
		case "up", "k":
			t.y--
		case "pgdown", "ctrl+d":
			t.y += t.bodyHeight() / 2
		case "pgup", "ctrl+u":
			t.y -= t.bodyHeight() / 2
		case "o":
			t.open()
		default:
			if d, err := strconv.Atoi(msg.String()); err == nil && d >= 1 && d <= last {
				t.goTo(d)
			}
		}
		t.clamp()
	}
	return t, nil
}

func (t *Tour) goTo(stop int) {
	stop = min(max(stop, 0), len(t.opts.Tour.Stops)-1)
	if stop != t.stop {
		t.stop, t.y = stop, 0
		t.render()
	}
}

func (t *Tour) open() {
	id := t.opts.Tour.Stops[t.stop].ElementID
	if id == "" || t.opts.Link == nil || t.opts.Open == nil {
		t.status = "nothing to open on this stop"
		return
	}
	url := t.opts.Link(id)
	if url == "" {
		return
	}
	if err := t.opts.Open(url); err != nil {
		t.status = "could not open the browser"
		return
	}
	t.status = "opened in the browser"
}

func (t *Tour) bodyHeight() int {
	return max(t.height-4, 3)
}

func (t *Tour) clamp() {
	t.y = min(max(t.y, 0), max(len(t.lines)-t.bodyHeight(), 0))
}

// View implements tea.Model.
func (t *Tour) View() tea.View {
	if t.width == 0 {
		return tea.NewView("")
	}
	out := []string{t.header(), t.progress()}
	h := t.bodyHeight()
	for i := t.y; i < len(t.lines) && i < t.y+h; i++ {
		out = append(out, t.lines[i])
	}
	for len(out) < h+2 {
		out = append(out, "")
	}
	out = append(out, "", t.footer())
	v := tea.NewView(strings.Join(out, "\n"))
	v.AltScreen = true
	v.WindowTitle = "archyl tour · " + t.opts.Tour.Project
	return v
}

func (t *Tour) header() string {
	left := t.s.brand.Render("◆ archyl") + t.s.muted.Render("  tour  ") + t.s.title.Render(t.title())
	right := t.s.muted.Render(strconv.Itoa(t.stop+1) + "/" + strconv.Itoa(len(t.opts.Tour.Stops)))
	gap := max(t.width-lipgloss.Width(left)-lipgloss.Width(right)-2, 1)
	return " " + left + strings.Repeat(" ", gap) + right + " "
}

func (t *Tour) title() string {
	if t.opts.Tour.FocusID != "" && len(t.opts.Tour.Stops) > 0 {
		return t.opts.Tour.Stops[0].Name
	}
	return t.opts.Tour.Project
}

// progress is one mark per stop, the current one accented.
func (t *Tour) progress() string {
	var marks []string
	for i := range t.opts.Tour.Stops {
		switch {
		case i == t.stop:
			marks = append(marks, t.s.accent.Render("●"))
		case i < t.stop:
			marks = append(marks, t.s.muted.Render("●"))
		default:
			marks = append(marks, t.s.subtle.Render("○"))
		}
	}
	return " " + strings.Join(marks, " ")
}

func (t *Tour) footer() string {
	if t.status != "" {
		return " " + t.s.muted.Render(t.status)
	}
	next := "next"
	if t.stop == len(t.opts.Tour.Stops)-1 {
		next = "done"
	}
	pairs := []string{"n →", next, "p ←", "back", "j k", "scroll"}
	if t.opts.Tour.Stops[t.stop].ElementID != "" {
		pairs = append(pairs, "o", "open in web")
	}
	pairs = append(pairs, "q", "quit")
	return " " + t.s.hints(pairs...)
}

// render lays out the current stop at the current width.
func (t *Tour) render() {
	if t.width == 0 {
		return
	}
	width := max(min(t.width-4, 100), 20)
	st := t.opts.Tour.Stops[t.stop]
	var lines []string
	add := func(s ...string) {
		for _, l := range s {
			lines = append(lines, "  "+l)
		}
	}
	para := func(text string, style lipgloss.Style) {
		for _, l := range wrap(text, width) {
			add(style.Render(l))
		}
	}
	gap := func() { lines = append(lines, "") }
	body := t.s.title.UnsetBold()

	gap()
	switch st.Kind {
	case tour.KindOverview:
		add(t.s.title.Render("The big picture"))
	case tour.KindElement:
		add(t.s.element(st.Level, st.Name))
	default:
		add(t.s.title.Render("Recap"))
	}
	if d := tour.Describe(st); d != "" {
		add(t.s.muted.Render(cut(d, width)))
	}
	gap()

	switch {
	case st.Story != "":
		para(st.Story, body)
		gap()
		if st.Kind == tour.KindElement && st.Description != "" {
			para(st.Description, t.s.muted)
			gap()
		}
	default:
		if st.Why != "" {
			para(st.Why, t.s.accent)
			gap()
		}
		if st.Description != "" {
			para(st.Description, body)
			gap()
		}
	}

	if st.Kind == tour.KindRecap {
		t.recap(add, para, gap)
		t.lines = lines
		t.clamp()
		return
	}
	if len(st.Flows) > 0 {
		add(t.s.section.Render("Main flows"))
		for _, f := range st.Flows {
			for i, l := range wrap(f, width-4) {
				if i == 0 {
					add("  " + t.s.accent.Render("›") + " " + l)
				} else {
					add("    " + l)
				}
			}
		}
		gap()
	}
	t.facts(st, width, add, gap)
	if v := tour.StopView(t.opts.Graph, st.ElementID); v != nil && t.opts.Graph != nil {
		d := diagram.Text(v, diagram.TextOptions{Theme: t.opts.Theme, Color: true, Highlight: v.Hops(), Width: width})
		for _, l := range strings.Split(d, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	t.lines = lines
	t.clamp()
}

func (t *Tour) facts(st tour.Stop, width int, add func(...string), gap func()) {
	const labelWidth = 12
	row := func(label, value string) {
		if value == "" {
			return
		}
		prefix := t.s.section.Render(fit(label, labelWidth))
		for i, l := range wrap(value, width-labelWidth) {
			if i == 0 {
				add(prefix + l)
			} else {
				add(strings.Repeat(" ", labelWidth) + l)
			}
		}
	}
	if st.Kind == tour.KindOverview {
		row("Systems", strings.Join(st.Systems, ", "))
		row("People", strings.Join(st.People, ", "))
		row("Relies on", capList(st.Externals, 8))
		row("Shared", capList(st.Infrastructure, 8))
	}
	if len(st.KeyParts) > 0 {
		names := make([]string, len(st.KeyParts))
		for i, p := range st.KeyParts {
			names[i] = p.Name
		}
		value := strings.Join(names, ", ")
		if st.More > 0 {
			value += " and " + strconv.Itoa(st.More) + " more"
		}
		row("Key parts", value)
	}
	row("Used by", tour.Links(st.UsedBy, st.UsedByMore))
	row("Uses", tour.Links(st.Uses, st.UsesMore))
	if st.Kind == tour.KindElement {
		row("Infra", capList(st.Infrastructure, 8))
	}
	row("Owned by", strings.Join(st.Owners, ", "))
	gap()

	section := func(title string) {
		add(t.s.section.Render(title))
	}
	if len(st.Files) > 0 {
		section("Start here")
		pathWidth := 0
		for _, f := range st.Files {
			pathWidth = max(pathWidth, len([]rune(f.Path)))
		}
		pathWidth = min(pathWidth, width/2)
		for _, f := range st.Files {
			add("  " + t.s.accent.Render(fit(f.Path, pathWidth)) + t.s.muted.Render("  "+f.Reason))
		}
		gap()
	}
	if len(st.Decisions) > 0 {
		section("Decisions")
		for _, d := range st.Decisions {
			add("  " + t.s.title.Render("ADR-"+strconv.Itoa(d.Number)) + " " + d.Title + t.s.muted.Render("  "+d.Status))
		}
		gap()
	}
	if len(st.Notes) > 0 {
		section("Good to know")
		for _, n := range st.Notes {
			text := strings.Join(strings.Fields(n.Text), " ")
			for i, l := range wrap(text, width-14) {
				if i == 0 {
					add("  " + t.s.accent.Render(fit(n.Kind, 11)) + l)
				} else {
					add(strings.Repeat(" ", 13) + l)
				}
			}
		}
		gap()
	}
	if len(st.Rules) > 0 {
		section("Rules that guard it")
		for _, r := range st.Rules {
			add("  " + r.Name + t.s.muted.Render("  "+r.Severity))
		}
		gap()
	}
}

func (t *Tour) recap(add func(...string), para func(string, lipgloss.Style), gap func()) {
	n := 0
	for _, st := range t.opts.Tour.Stops {
		if st.Kind != tour.KindElement {
			continue
		}
		n++
		add(t.s.muted.Render(strconv.Itoa(n)+". ") + t.s.element(st.Level, st.Name))
	}
	gap()
	switch tr := t.opts.Tour; {
	case tr.TraceTask != "":
		add(t.s.section.Render("Try it"))
		para(tr.TraceTask, t.s.title.UnsetBold())
		gap()
	case len(tr.Trace) > 0:
		add(t.s.section.Render("Try it"))
		para("Follow one piece of work through the code: "+strings.Join(tr.Trace, " → ")+".", t.s.title.UnsetBold())
		gap()
	}
	add(t.s.section.Render("Next"))
	add("  " + t.s.accent.Render("archyl brief <files>") + t.s.muted.Render("  before you change code"))
	add("  " + t.s.accent.Render("archyl why <file>") + t.s.muted.Render("     when something surprises you"))
	add("  " + t.s.accent.Render("archyl map") + t.s.muted.Render("            to explore on your own"))
	if tr := t.opts.Tour; tr.Narrated {
		gap()
		add(t.s.subtle.Render("The story was written by AI from the model; the facts under each stop come from Archyl and your repository."))
	}
}

func capList(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + " and " + strconv.Itoa(len(items)-n) + " more"
}
