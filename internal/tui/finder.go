package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// FinderOptions configures the search view.
type FinderOptions struct {
	Theme  ui.Theme
	Query  string
	Search func(ctx context.Context, query string) ([]api.SearchResult, error)
	// Open opens a result in the browser; nil disables it.
	Open func(api.SearchResult) error
}

// Finder is the interactive search behind `archyl search`.
type Finder struct {
	opts    FinderOptions
	s       styles
	input   textinput.Model
	results []api.SearchResult
	cursor  int
	seq     int
	loading bool
	err     error
	width   int
	height  int
	status  string

	// Chosen is the result picked with enter, nil when the user quit.
	Chosen *api.SearchResult
}

type searchTick struct{ seq int }

type searchDone struct {
	seq     int
	results []api.SearchResult
	err     error
}

// NewFinder starts with the query typed so far.
func NewFinder(opts FinderOptions) *Finder {
	f := &Finder{opts: opts, s: newStyles(opts.Theme)}
	f.input = f.s.input("search elements, decisions, docs, contracts…")
	f.input.Prompt = "❯ "
	f.input.SetValue(opts.Query)
	return f
}

// Init implements tea.Model.
func (f *Finder) Init() tea.Cmd {
	return tea.Batch(f.input.Focus(), f.schedule())
}

// schedule debounces: the search runs only if nothing was typed for 180ms.
func (f *Finder) schedule() tea.Cmd {
	f.seq++
	seq := f.seq
	return tea.Tick(180*time.Millisecond, func(time.Time) tea.Msg { return searchTick{seq: seq} })
}

func (f *Finder) run(seq int, query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		results, err := f.opts.Search(ctx, query)
		return searchDone{seq: seq, results: results, err: err}
	}
}

// Update implements tea.Model.
func (f *Finder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		f.width, f.height = msg.Width, msg.Height
	case searchTick:
		if msg.seq != f.seq {
			return f, nil
		}
		query := strings.TrimSpace(f.input.Value())
		if len([]rune(query)) < 2 {
			f.results, f.loading, f.err = nil, false, nil
			return f, nil
		}
		f.loading = true
		return f, f.run(msg.seq, query)
	case searchDone:
		if msg.seq != f.seq {
			return f, nil
		}
		f.loading, f.err, f.results, f.cursor = false, msg.err, msg.results, 0
	case tea.KeyPressMsg:
		f.status = ""
		switch msg.String() {
		case "ctrl+c", "esc":
			return f, tea.Quit
		case "enter":
			if f.cursor < len(f.results) {
				chosen := f.results[f.cursor]
				f.Chosen = &chosen
			}
			return f, tea.Quit
		case "up", "ctrl+p", "ctrl+k":
			f.cursor = max(f.cursor-1, 0)
			return f, nil
		case "down", "ctrl+n", "ctrl+j", "tab":
			f.cursor = min(f.cursor+1, max(len(f.results)-1, 0))
			return f, nil
		case "ctrl+o":
			if f.opts.Open != nil && f.cursor < len(f.results) {
				if err := f.opts.Open(f.results[f.cursor]); err == nil {
					f.status = "opened in the browser"
				}
			}
			return f, nil
		}
		before := f.input.Value()
		var cmd tea.Cmd
		f.input, cmd = f.input.Update(msg)
		if f.input.Value() != before {
			return f, tea.Batch(cmd, f.schedule())
		}
		return f, cmd
	}
	return f, nil
}

// resultGlyph marks a search hit by kind.
func (f *Finder) resultGlyph(kind string) string {
	switch kind {
	case "system", "container", "component", "code_element":
		level := kind
		if kind == "code_element" {
			level = "code"
		}
		return f.s.glyph(level)
	case "adr":
		return f.s.accent.Render("§")
	case "documentation":
		return f.s.muted.Render("¶")
	case "api_contract", "event_channel":
		return f.s.muted.Render("⇄")
	case "flow":
		return f.s.muted.Render("↝")
	case "project":
		return f.s.brand.Render("◇")
	}
	return f.s.muted.Render("•")
}

// View implements tea.Model.
func (f *Finder) View() tea.View {
	if f.width == 0 {
		return tea.NewView("")
	}
	header := " " + f.s.brand.Render("◆ archyl") + f.s.muted.Render("  search")
	inputBox := lipgloss.NewStyle().Padding(1, 2, 0, 2).Render(f.input.View()) + "\n" + f.s.subtle.Render(strings.Repeat("─", f.width))

	bodyHeight := max(f.height-lipgloss.Height(header)-lipgloss.Height(inputBox)-2, 4)
	listWidth := min(max(f.width/2, 36), f.width-24)
	previewWidth := f.width - listWidth

	var lines []string
	switch {
	case f.err != nil:
		lines = append(lines, lipgloss.NewStyle().Foreground(f.s.t.Danger).Render("✗ "+f.err.Error()))
	case len([]rune(strings.TrimSpace(f.input.Value()))) < 2:
		lines = append(lines, f.s.muted.Render("Type at least two characters."))
	case f.loading && len(f.results) == 0:
		lines = append(lines, f.s.muted.Render("Searching…"))
	case len(f.results) == 0:
		lines = append(lines, f.s.muted.Render("Nothing found."))
	}
	start := max(f.cursor-bodyHeight+3, 0)
	for i := start; i < len(f.results) && len(lines) < bodyHeight-2; i++ {
		r := f.results[i]
		line := f.resultGlyph(r.Type) + " " + f.s.title.Render(r.Name) + "  " + f.s.muted.Render(strings.ReplaceAll(r.Type, "_", " "))
		if i == f.cursor {
			lines = append(lines, f.s.selected.Render(fit("› "+ansiStrip(line), listWidth-4)))
		} else {
			lines = append(lines, fit("  "+line, listWidth-4))
		}
	}
	list := f.s.panel.Width(listWidth).Height(bodyHeight).Render(strings.Join(lines, "\n"))
	preview := f.s.rule.Width(previewWidth).Height(bodyHeight).Render(f.preview(previewWidth-6, bodyHeight-2))

	footer := " " + f.s.hints("↑↓", "move", "enter", "show", "ctrl+o", "open in web", "esc", "quit")
	if f.loading {
		footer = " " + f.s.accent.Render("⠿ searching…") + "   " + footer
	}
	if f.status != "" {
		footer = " " + f.s.accent.Render("✓ "+f.status)
	}
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, inputBox, lipgloss.JoinHorizontal(lipgloss.Top, list, preview), footer))
	v.AltScreen = true
	v.WindowTitle = "archyl search"
	return v
}

func (f *Finder) preview(width, height int) string {
	if f.cursor >= len(f.results) {
		return f.s.muted.Render("Results show up here as you type.")
	}
	r := f.results[f.cursor]
	lines := []string{
		f.resultGlyph(r.Type) + " " + lipgloss.NewStyle().Bold(true).Foreground(f.s.t.Text).Render(r.Name),
		f.s.muted.Render(strings.ReplaceAll(r.Type, "_", " ")),
		"",
	}
	if r.ProjectName != "" {
		lines = append(lines, f.s.muted.Render("project  ")+r.ProjectName, "")
	}
	if d := wrap(r.Description, width); len(d) > 0 {
		lines = append(lines, d...)
	} else {
		lines = append(lines, f.s.muted.Render("No description."))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// String renders a result for non-interactive output.
func ResultLine(t ui.Theme, r api.SearchResult) string {
	f := &Finder{s: newStyles(t)}
	line := f.resultGlyph(r.Type) + " " + f.s.title.Render(r.Name) + "  " + f.s.muted.Render(strings.ReplaceAll(r.Type, "_", " "))
	if r.ProjectName != "" {
		line += f.s.muted.Render(fmt.Sprintf("  · %s", r.ProjectName))
	}
	return line
}
