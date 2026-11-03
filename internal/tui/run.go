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
	"github.com/archyl-com/cli/internal/runstate"
	"github.com/archyl-com/cli/internal/ui"
)

// RunActions are what a person can do to a run from the live view.
type RunActions struct {
	Run     func(ctx context.Context) (*api.Run, error)
	Events  func(ctx context.Context, since int64) ([]api.RunEvent, error)
	Respond func(ctx context.Context, resp api.RunResponse) error
	Approve func(ctx context.Context) error
	Steer   func(ctx context.Context, message string) error
	Cancel  func(ctx context.Context) error
	// Continue starts a follow-up run and returns its ID.
	Continue func(ctx context.Context, instructions string) (string, error)
	Open     func(url string) error
}

// RunView is the live view of one managed agent run.
type RunView struct {
	s       styles
	a       RunActions
	run     *api.Run
	state   *runstate.State
	width   int
	height  int
	scroll  int // lines scrolled up from the bottom; 0 follows the feed
	frame   int
	mode    string // "", "answer", "feedback", "steer", "continue", "cancel"
	input   textinput.Model
	status  string
	err     error
	polling bool

	// Next is the follow-up run started from this view, if any.
	Next string
}

type runPolled struct {
	run    *api.Run
	events []api.RunEvent
	err    error
}

type runTick struct{}

type runActed struct {
	status string
	next   string
	err    error
	// settled is the human request the action answered, so the view stops
	// asking before the server's echo arrives.
	settled string
}

// NewRunView starts following the run at once.
func NewRunView(t ui.Theme, actions RunActions) *RunView {
	v := &RunView{s: newStyles(t), a: actions, state: runstate.New(), polling: true}
	v.input = v.s.input("")
	v.input.Prompt = "❯ "
	return v
}

func (v *RunView) poll() tea.Cmd {
	since := v.state.LastSeq
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		run, err := v.a.Run(ctx)
		if err != nil {
			return runPolled{err: err}
		}
		events, err := v.a.Events(ctx, since)
		return runPolled{run: run, events: events, err: err}
	}
}

func (v *RunView) act(status string, fn func(ctx context.Context) (string, error)) tea.Cmd {
	return v.actOn("", status, fn)
}

// actOn runs an action that answers the human request settled.
func (v *RunView) actOn(settled, status string, fn func(ctx context.Context) (string, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		next, err := fn(ctx)
		return runActed{status: status, next: next, err: err, settled: settled}
	}
}

// Init implements tea.Model.
func (v *RunView) Init() tea.Cmd {
	return v.poll()
}

func (v *RunView) startInput(mode, placeholder string) tea.Cmd {
	v.mode = mode
	v.input.SetValue("")
	v.input.Placeholder = placeholder
	return v.input.Focus()
}

// Update implements tea.Model.
func (v *RunView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		v.width, v.height = msg.Width, msg.Height
	case runPolled:
		if msg.err != nil {
			v.err = msg.err
		} else {
			v.err = nil
			v.run = msg.run
			v.state.Apply(msg.events)
		}
		if v.run != nil && v.run.Ended() && v.state.Pending() == "" {
			v.polling = false
			return v, nil
		}
		return v, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return runTick{} })
	case runTick:
		v.frame++
		return v, v.poll()
	case runActed:
		if msg.err != nil {
			v.status = "✗ " + msg.err.Error()
		} else {
			v.status = "✓ " + msg.status
			if q := v.state.Question; q != nil && q.ID == msg.settled {
				q.Answered = true
			}
			if pl := v.state.Plan; pl != nil && pl.ID == msg.settled {
				pl.AwaitingApproval = false
			}
			if msg.next != "" {
				v.Next = msg.next
				return v, tea.Quit
			}
		}
		if !v.polling {
			v.polling = true
			return v, v.poll()
		}
	case tea.KeyPressMsg:
		if v.mode != "" {
			return v.updateInput(msg)
		}
		return v.updateKeys(msg)
	}
	return v, nil
}

func (v *RunView) updateKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pending := v.state.Pending()
	ended := v.run != nil && v.run.Ended()
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return v, tea.Quit
	case "up", "k":
		v.scroll++
	case "down", "j":
		v.scroll = max(v.scroll-1, 0)
	case "pgup":
		v.scroll += 10
	case "pgdown":
		v.scroll = max(v.scroll-10, 0)
	case "end", "G":
		v.scroll = 0
	case "enter", "r":
		if pending == "question" {
			return v, v.startInput("answer", "your answer")
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if pending == "question" && v.state.Question != nil {
			if i := int(msg.String()[0] - '1'); i < len(v.state.Question.Options) {
				answer, id := v.state.Question.Options[i], v.state.Question.ID
				return v, v.actOn(id, "answered: "+answer, func(ctx context.Context) (string, error) {
					return "", v.a.Respond(ctx, api.RunResponse{RequestID: id, Answer: answer})
				})
			}
		}
	case "a":
		if pending == "plan" {
			id, yes := v.state.Plan.ID, true
			return v, v.actOn(id, "plan approved", func(ctx context.Context) (string, error) {
				return "", v.a.Respond(ctx, api.RunResponse{RequestID: id, Approved: &yes})
			})
		}
		if v.run != nil && v.run.Status == "awaiting_approval" {
			return v, v.act("run approved", func(ctx context.Context) (string, error) { return "", v.a.Approve(ctx) })
		}
	case "c":
		if pending == "plan" {
			return v, v.startInput("feedback", "what should change in the plan?")
		}
	case "s":
		if !ended {
			return v, v.startInput("steer", "tell the agent something")
		}
	case "x":
		if !ended {
			v.mode = "cancel"
		}
	case "n":
		if ended {
			return v, v.startInput("continue", "what should a follow-up run do?")
		}
	case "o":
		if v.state.PRURL != "" && v.a.Open != nil {
			v.status = "✓ opened the pull request"
			if err := v.a.Open(v.state.PRURL); err != nil {
				v.status = "✗ " + err.Error()
			}
		}
	}
	return v, nil
}

func (v *RunView) updateInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if v.mode == "cancel" {
		v.mode = ""
		if msg.String() == "y" {
			return v, v.act("run cancelled", func(ctx context.Context) (string, error) { return "", v.a.Cancel(ctx) })
		}
		return v, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		v.mode = ""
		v.input.Blur()
		return v, nil
	case "enter":
		text := strings.TrimSpace(v.input.Value())
		mode := v.mode
		v.mode = ""
		v.input.Blur()
		if text == "" {
			return v, nil
		}
		switch mode {
		case "answer":
			id := v.state.Question.ID
			return v, v.actOn(id, "answered", func(ctx context.Context) (string, error) {
				return "", v.a.Respond(ctx, api.RunResponse{RequestID: id, Answer: text})
			})
		case "feedback":
			id, no := v.state.Plan.ID, false
			return v, v.actOn(id, "changes requested", func(ctx context.Context) (string, error) {
				return "", v.a.Respond(ctx, api.RunResponse{RequestID: id, Approved: &no, Feedback: text})
			})
		case "steer":
			return v, v.act("sent to the agent", func(ctx context.Context) (string, error) { return "", v.a.Steer(ctx, text) })
		case "continue":
			return v, v.act("follow-up run started", func(ctx context.Context) (string, error) { return v.a.Continue(ctx, text) })
		}
		return v, nil
	}
	var cmd tea.Cmd
	v.input, cmd = v.input.Update(msg)
	return v, cmd
}

func (v *RunView) statusBadge(status string) string {
	t := v.s.t
	c, label := t.Muted, strings.ReplaceAll(status, "_", " ")
	switch status {
	case "running":
		c = t.Accent
		label = []string{"◐", "◓", "◑", "◒"}[v.frame%4] + " running"
	case "succeeded":
		c, label = t.Success, "✓ succeeded"
	case "failed":
		c, label = t.Danger, "✗ failed"
	case "waiting_for_input", "awaiting_approval":
		c, label = t.Warning, "● needs you"
	case "pending":
		label = "○ queued"
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(label)
}

// View implements tea.Model.
func (v *RunView) View() tea.View {
	if v.width == 0 {
		return tea.NewView("")
	}
	header := " " + v.s.brand.Render("◆ archyl") + v.s.muted.Render("  run ")
	if v.run != nil {
		header += v.s.muted.Render(ShortRunID(v.run.ID)+"  ") + v.statusBadge(v.run.Status) + "  " + v.s.title.Render(cut(v.run.TaskText, v.width-40))
	}
	footer := v.footer()
	bodyHeight := max(v.height-lipgloss.Height(header)-lipgloss.Height(footer), 6)
	sideWidth := min(max(v.width/3, 30), 46)
	feedWidth := v.width - sideWidth

	feed := v.s.panel.Width(feedWidth).Height(bodyHeight).Render(v.feedView(feedWidth-4, bodyHeight-2))
	side := v.s.rule.Width(sideWidth).Height(bodyHeight).Render(v.sideView(sideWidth-6, bodyHeight-2))
	view := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, lipgloss.JoinHorizontal(lipgloss.Top, feed, side), footer))
	view.AltScreen = true
	if v.run != nil {
		view.WindowTitle = "archyl run · " + strings.ReplaceAll(v.run.Status, "_", " ")
	}
	return view
}

func (v *RunView) feedLines(width int) []string {
	var lines []string
	add := func(l ...string) { lines = append(lines, l...) }
	for _, it := range v.state.Items {
		switch it.Kind {
		case runstate.ItemText:
			add("")
			for _, l := range strings.Split(strings.TrimSpace(it.Body), "\n") {
				add(wrapKeep(l, width)...)
			}
		case runstate.ItemTool:
			mark := v.s.muted.Render("›")
			if it.Error {
				mark = lipgloss.NewStyle().Foreground(v.s.t.Danger).Render("✗")
			}
			add(fit(mark+" "+v.s.accent.Render(it.Title)+"  "+v.s.muted.Render(it.Body), width))
		case runstate.ItemFile:
			add(fit(lipgloss.NewStyle().Foreground(v.s.t.Success).Render("±")+" "+it.Title+"  "+v.s.muted.Render(it.Body), width))
		case runstate.ItemPlan:
			add("", v.s.section.Render("PLAN")+"  "+v.s.muted.Render(it.Title))
			add(wrapKeep(it.Body, width)...)
		case runstate.ItemQuestion:
			add("", lipgloss.NewStyle().Foreground(v.s.t.Warning).Bold(true).Render("? "+it.Title))
			add(wrapKeep(it.Body, width)...)
		case runstate.ItemAnswer, runstate.ItemSteering:
			line := v.s.accent.Render("↳ " + it.Title)
			if it.Body != "" {
				line += "  " + it.Body
			}
			add(fit(line, width))
		case runstate.ItemStatus:
			add(v.s.subtle.Render("── " + it.Title + " " + strings.Repeat("─", max(width-len(it.Title)-4, 0))))
		case runstate.ItemResult:
			add("", v.s.section.Render("RESULT")+"  "+v.statusBadge(it.Title))
			if it.Body != "" {
				add(fit(v.s.accent.Render(it.Body), width))
			}
		case runstate.ItemError:
			add(lipgloss.NewStyle().Foreground(v.s.t.Danger).Render("✗ "+it.Title), fit(it.Body, width))
		}
	}
	return lines
}

func (v *RunView) feedView(width, height int) string {
	lines := v.feedLines(width)
	if len(lines) == 0 {
		msg := "Waiting for the agent…"
		if v.err != nil {
			msg = "✗ " + v.err.Error()
		}
		return v.s.muted.Render(msg)
	}
	v.scroll = min(v.scroll, max(len(lines)-height, 0))
	end := len(lines) - v.scroll
	start := max(end-height, 0)
	return strings.Join(lines[start:end], "\n")
}

func (v *RunView) sideView(width, height int) string {
	var lines []string
	add := func(l ...string) { lines = append(lines, l...) }
	if p := v.state.Plan; p != nil {
		done := 0
		for _, st := range p.Steps {
			if st.Status == "completed" {
				done++
			}
		}
		add(v.s.section.Render(fmt.Sprintf("PLAN  %d/%d", done, len(p.Steps))))
		for i, st := range p.Steps {
			mark := map[string]string{"completed": "✓", "in_progress": "▸", "failed": "✗", "skipped": "–"}[st.Status]
			style := v.s.muted
			switch st.Status {
			case "completed":
				style = lipgloss.NewStyle().Foreground(v.s.t.Success)
			case "in_progress":
				style = v.s.accent
			}
			if mark == "" {
				mark = "○"
			}
			add(fit(style.Render(mark)+" "+fmt.Sprintf("%d. %s", i+1, st.Title), width))
		}
		add("")
	}
	if len(v.state.Files) > 0 {
		add(v.s.section.Render(fmt.Sprintf("FILES  %d", len(v.state.Files))))
		for i, f := range v.state.Files {
			if i == 8 {
				add(v.s.muted.Render(fmt.Sprintf("+%d more", len(v.state.Files)-8)))
				break
			}
			counts := lipgloss.NewStyle().Foreground(v.s.t.Success).Render(fmt.Sprintf("+%d", f.Additions)) + " " +
				lipgloss.NewStyle().Foreground(v.s.t.Danger).Render(fmt.Sprintf("−%d", f.Deletions))
			add(fit(counts+" "+f.Path, width))
		}
		add("")
	}
	if r := v.run; r != nil {
		add(v.s.section.Render("RUN"))
		if r.StartedAt != nil {
			end := time.Now()
			if r.EndedAt != nil {
				end = *r.EndedAt
			}
			add(v.s.muted.Render("time     ") + end.Sub(*r.StartedAt).Round(time.Second).String())
		}
		add(v.s.muted.Render("tokens   ") + fmt.Sprintf("%s in · %s out", compact(r.TotalInputTokens), compact(r.TotalOutputTokens)))
		if r.TotalCostCents > 0 {
			add(v.s.muted.Render("cost     ") + fmt.Sprintf("€%.2f", float64(r.TotalCostCents)/100))
		}
		if v.state.Branch != "" {
			add(fit(v.s.muted.Render("branch   ")+v.state.Branch, width))
		}
		if pr := firstNonEmptyStr(v.state.PRURL, r.PRURL); pr != "" {
			add(fit(v.s.muted.Render("PR       ")+v.s.accent.Render(pr), width))
		}
		if r.ErrorMessage != "" {
			add("", lipgloss.NewStyle().Foreground(v.s.t.Danger).Render("✗ ")+cut(r.ErrorMessage, width*3))
		}
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (v *RunView) footer() string {
	warn := lipgloss.NewStyle().Foreground(v.s.t.Warning).Bold(true)
	switch v.mode {
	case "answer", "feedback", "steer", "continue":
		label := map[string]string{"answer": "Answer the agent", "feedback": "Ask for changes", "steer": "Steer the agent", "continue": "Continue in a new run"}[v.mode]
		return " " + warn.Render(label) + "  " + v.input.View() + "\n " + v.s.hints("enter", "send", "esc", "cancel")
	case "cancel":
		return " " + warn.Render("Cancel this run?") + "  " + v.s.hints("y", "yes", "any key", "no")
	}

	var hints []string
	switch v.state.Pending() {
	case "question":
		q := v.state.Question
		line := " " + warn.Render("? "+cut(q.Text, v.width-6))
		if len(q.Options) > 0 {
			var opts []string
			for i, o := range q.Options {
				opts = append(opts, v.s.key.Render(fmt.Sprint(i+1))+" "+o)
			}
			line += "\n   " + strings.Join(opts, v.s.subtle.Render("  ·  "))
		}
		hints = []string{"enter", "answer"}
		if len(q.Options) > 0 {
			hints = append(hints, "1-9", "pick")
		}
		return line + "\n " + v.s.hints(append(hints, "s", "steer", "q", "quit")...)
	case "plan":
		return " " + warn.Render("The agent waits for you to review its plan.") + "\n " +
			v.s.hints("a", "approve", "c", "ask for changes", "↑↓", "scroll", "q", "quit")
	}

	ended := v.run != nil && v.run.Ended()
	if v.run != nil && v.run.Status == "awaiting_approval" {
		hints = append(hints, "a", "approve run")
	}
	if !ended {
		hints = append(hints, "s", "steer", "x", "cancel")
	} else {
		hints = append(hints, "n", "continue")
	}
	if v.state.PRURL != "" {
		hints = append(hints, "o", "open PR")
	}
	hints = append(hints, "↑↓", "scroll", "q", "quit")
	line := " " + v.s.hints(hints...)
	if v.status != "" {
		line = " " + v.s.accent.Render(v.status) + "   " + line
	}
	return line
}

func wrapKeep(s string, width int) []string {
	if strings.TrimSpace(s) == "" {
		return []string{""}
	}
	return wrap(s, width)
}

func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func ShortRunID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
