package shell

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	binding "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

// Options is what the session needs from the CLI around it.
type Options struct {
	Theme   ui.Theme
	Dark    bool
	Version string
	// Exe is the archyl binary that runs the commands.
	Exe string
	// GlobalArgs are flags every command inherits (--profile, --project).
	GlobalArgs []string
	// Client is nil when nobody is signed in.
	Client    *api.Client
	ProjectID string
	Project   string
	Host      string
	Branch    string
	Linked    bool
	Score     int
	HasScore  bool
	// Root is the repository root @file mentions resolve against; the
	// working directory when empty.
	Root string
	// Files and Elements feed @ and # completion; called once, lazily.
	Files       func() []string
	Elements    func() []string
	HistoryPath string
	// SessionDir keeps this repository's conversations (see SessionDir);
	// empty keeps none.
	SessionDir string
	// Resume reopens the most recent conversation.
	Resume bool
}

type styles struct {
	t                                  ui.Theme
	accent, muted, subtle, text, title lipgloss.Style
	warn, danger, ok                   lipgloss.Style
}

func newStyles(t ui.Theme) styles {
	return styles{
		t:      t,
		accent: lipgloss.NewStyle().Foreground(t.Accent),
		muted:  lipgloss.NewStyle().Foreground(t.Muted),
		subtle: lipgloss.NewStyle().Foreground(t.Subtle),
		text:   lipgloss.NewStyle().Foreground(t.Text),
		title:  lipgloss.NewStyle().Foreground(t.Text).Bold(true),
		warn:   lipgloss.NewStyle().Foreground(t.Warning),
		danger: lipgloss.NewStyle().Foreground(t.Danger),
		ok:     lipgloss.NewStyle().Foreground(t.Success),
	}
}

type suggestion struct {
	value string // what replaces the token
	label string
	help  string
}

// pending is an action the assistant asked for that writes, awaiting a yes.
type pending struct {
	cmd  Command
	args string
	// callID is the tool call it answers, with native tool calling.
	callID string
	// shown is when the prompt appeared.
	shown time.Time
}

// Model is the session.
type Model struct {
	o     Options
	s     styles
	input textarea.Model
	width int

	busy    string // "", "thinking", "running"
	running []runningCmd
	lastRun int
	live    strings.Builder
	calls   []api.ToolCall // tool calls of the answer being streamed
	frame   int
	cancel  context.CancelFunc
	chunks  chan any

	conversation []api.ChatMessage
	transcript   *Transcript
	actionsUsed  int
	rounds       int
	confirm      *pending
	batch        *toolBatch

	history []string
	histIdx int

	suggestions []suggestion
	sel         int
	files       []string
	elements    []string
	filesLoaded bool
	elemsLoaded bool

	lastCtrlC time.Time
}

type (
	chunkMsg    struct{ text string }
	toolCallMsg struct{ call api.ToolCall }
	streamDone  struct{ err error }
	cmdDone     struct {
		id     int
		name   string
		label  string
		output string
		err    error
		forAI  bool
		callID string
	}
	execDone   struct{ err error }
	tickMsg    struct{}
	frameDrawn struct{}
	elementsIn struct{ names []string }
)

// New builds the session.
func New(o Options) *Model {
	m := &Model{o: o, s: newStyles(o.Theme), width: 100}
	m.input = newInput(m.s)
	m.input.Focus()
	m.history = loadHistory(o.HistoryPath)
	m.histIdx = len(m.history)
	m.transcript = newTranscript(o.ProjectID)
	return m
}

// newInput is the prompt: enter sends, shift+enter, alt+enter and ctrl+j
// start a new line, and it grows with what is typed or pasted.
func newInput(s styles) textarea.Model {
	in := textarea.New()
	in.ShowLineNumbers = false
	in.Placeholder = "Ask about the architecture, or type / for commands"
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = 8
	in.MaxContentHeight = 5000
	in.KeyMap.InsertNewline = binding.NewBinding(binding.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	in.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return "› "
		}
		return "  "
	})
	st := in.Styles()
	for _, state := range []*textarea.StyleState{&st.Focused, &st.Blurred} {
		state.Base = lipgloss.NewStyle()
		state.Text = s.text
		state.CursorLine = s.text
		state.Placeholder = s.subtle
		state.Prompt = s.accent
		state.EndOfBuffer = s.subtle
	}
	st.Cursor.Blink = false
	in.SetStyles(st)
	in.SetHeight(1)
	return in
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	welcome := m.welcome()
	if m.o.Resume {
		welcome += "\n" + m.resumeLatest()
	}
	cmds := []tea.Cmd{m.input.Focus(), printLine(welcome), m.tick()}
	if m.o.Elements != nil {
		load := m.o.Elements
		cmds = append(cmds, func() tea.Msg { return elementsIn{names: load()} })
	}
	return tea.Batch(cmds...)
}

// frameWait outlasts a few frames at Bubble Tea's 60 per second.
const frameWait = 50 * time.Millisecond

// maxConfirmLabel bounds the command shown by a write prompt.
const maxConfirmLabel = 400

// confirmGrace ignores a y typed before the prompt could be read.
var confirmGrace = 300 * time.Millisecond

// afterFrame runs cmd once the frame that collapsed the live area is on
// screen. Bubble Tea places printed lines from the last frame it drew: an
// answer printed in the same update that collapses the streaming area would
// scroll the terminal by that area's height, and leave as many blank lines
// under the prompt.
func afterFrame(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	wait := func() tea.Msg {
		time.Sleep(frameWait)
		return frameDrawn{}
	}
	return tea.Sequence(wait, cmd)
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) welcome() string {
	title := m.s.title.Render("archyl") + m.s.muted.Render("  "+m.o.Version)
	var where string
	switch {
	case m.o.Client == nil:
		where = m.s.text.Render("Not signed in yet: ") + m.s.accent.Render("/login")
	case !m.o.Linked:
		where = m.s.text.Render("Repository not linked yet: ") + m.s.accent.Render("/init")
	default:
		where = m.s.text.Render(m.o.Project)
		if m.o.Branch != "" {
			where += m.s.muted.Render(" · " + m.o.Branch)
		}
		if m.o.HasScore {
			where += m.s.muted.Render(fmt.Sprintf(" · alignment %d", m.o.Score))
		}
	}
	hints := m.s.accent.Render("/") + m.s.muted.Render(" commands  ") +
		m.s.accent.Render("@") + m.s.muted.Render(" files  ") +
		m.s.accent.Render("#") + m.s.muted.Render(" elements")
	logo := ui.LogoWith(m.o.Theme, m.o.Dark, title, where, m.s.muted.Render("Ask anything about the architecture."), hints)
	return "\n" + indent(logo, 2) + "\n"
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.SetWidth(max(msg.Width-2, 20))
		return m, nil
	case tickMsg:
		m.frame++
		return m, m.tick()
	case elementsIn:
		m.elements, m.elemsLoaded = msg.names, true
		return m, nil
	case chunkMsg:
		m.live.WriteString(msg.text)
		return m, m.waitChunk()
	case toolCallMsg:
		m.calls = append(m.calls, msg.call)
		return m, m.waitChunk()
	case streamDone:
		model, cmd := m.finishStream(msg.err)
		return model, afterFrame(cmd)
	case frameDrawn:
		return m, nil
	case cmdDone:
		return m.finishCommand(msg)
	case execDone:
		m.busy = ""
		return m, m.input.Focus()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.confirm != nil {
		switch key {
		case "y":
			if time.Since(m.confirm.shown) < confirmGrace {
				return m, nil
			}
			return m, m.approve()
		case "n", "esc", "ctrl+c":
			return m, m.decline()
		}
		return m, nil
	}

	switch key {
	case "ctrl+c":
		if m.busy != "" {
			m.stop()
			return m, afterFrame(printLine(m.s.muted.Render("  ⏹ stopped")))
		}
		if m.input.Value() != "" {
			m.input.SetValue("")
			m.suggestions = nil
			return m, nil
		}
		if time.Since(m.lastCtrlC) < 2*time.Second {
			return m, tea.Quit
		}
		m.lastCtrlC = time.Now()
		return m, nil
	case "ctrl+d":
		return m, tea.Quit
	case "esc":
		if m.busy != "" {
			m.stop()
			return m, afterFrame(printLine(m.s.muted.Render("  ⏹ stopped")))
		}
		m.suggestions = nil
		return m, nil
	case "ctrl+l":
		return m, tea.ClearScreen
	}

	if m.busy != "" {
		return m, nil
	}

	switch key {
	case "tab":
		if len(m.suggestions) > 0 {
			m.complete(m.suggestions[m.sel])
		}
		return m, nil
	case "up":
		if len(m.suggestions) > 0 {
			m.sel = (m.sel - 1 + len(m.suggestions)) % len(m.suggestions)
			return m, nil
		}
		if m.input.Line() > 0 {
			break
		}
		if m.histIdx > 0 {
			m.histIdx--
			m.setInput(m.history[m.histIdx])
		}
		return m, nil
	case "down":
		if len(m.suggestions) > 0 {
			m.sel = (m.sel + 1) % len(m.suggestions)
			return m, nil
		}
		if m.input.Line() < m.input.LineCount()-1 {
			break
		}
		if m.histIdx < len(m.history) {
			m.histIdx++
			value := ""
			if m.histIdx < len(m.history) {
				value = m.history[m.histIdx]
			}
			m.setInput(value)
		}
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		switch {
		// "/imp" + enter completes to /impact and runs it.
		case strings.HasPrefix(text, "/") && !strings.ContainsAny(text, " \n") && len(m.suggestions) > 0:
			if _, exact := Lookup(strings.TrimPrefix(text, "/")); !exact {
				m.complete(m.suggestions[m.sel])
				text = strings.TrimSpace(m.input.Value())
			}
		// "/resume " + enter picks the highlighted conversation.
		case strings.HasPrefix(text, "/resume") && len(m.suggestions) > 0 && strings.HasPrefix(m.suggestions[m.sel].value, "/resume "):
			text = strings.TrimSpace(m.suggestions[m.sel].value)
		}
		m.input.SetValue("")
		m.suggestions = nil
		return m.submit(text)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.refreshSuggestions()
	return m, cmd
}

func (m *Model) setInput(value string) {
	m.input.SetValue(value)
	m.input.MoveToEnd()
}

// currentToken is the word being typed at the end of the input.
func (m *Model) currentToken() (string, int) {
	value := m.input.Value()
	i := strings.LastIndexAny(value, " \t\n")
	return value[i+1:], i + 1
}

func (m *Model) refreshSuggestions() {
	m.suggestions, m.sel = nil, 0
	value := m.input.Value()
	token, _ := m.currentToken()
	switch {
	case strings.HasPrefix(value, "/resume "):
		query := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(value, "/resume ")))
		for _, t := range listTranscripts(m.o.SessionDir) {
			if query != "" && !strings.Contains(strings.ToLower(t.Title+" "+t.ID), query) {
				continue
			}
			help := ago(t.Updated) + fmt.Sprintf(" · %d %s", t.Questions(), plural(t.Questions(), "question", "questions"))
			m.suggestions = append(m.suggestions, suggestion{value: "/resume " + t.ID, label: truncate(t.Title, 44), help: help})
		}
	case strings.HasPrefix(value, "/") && !strings.ContainsAny(value, " \n"):
		for _, c := range Matching(strings.TrimPrefix(value, "/")) {
			label := "/" + c.Name
			if c.Usage != "" {
				label += " " + c.Usage
			}
			m.suggestions = append(m.suggestions, suggestion{value: "/" + c.Name + " ", label: label, help: c.Help})
		}
		for _, b := range builtins {
			if strings.HasPrefix(b.name, strings.TrimPrefix(value, "/")) {
				label := "/" + b.name
				if b.usage != "" {
					label += " " + b.usage
				}
				m.suggestions = append(m.suggestions, suggestion{value: "/" + b.name, label: label, help: b.help})
			}
		}
	case strings.HasPrefix(token, "@") && len(token) > 1:
		if !m.filesLoaded && m.o.Files != nil {
			m.files, m.filesLoaded = m.o.Files(), true
		}
		for _, f := range rank(m.files, strings.TrimPrefix(token, "@")) {
			m.suggestions = append(m.suggestions, suggestion{value: "@" + f + " ", label: "@" + f})
		}
	case strings.HasPrefix(token, "#") && len(token) > 1:
		for _, e := range rank(m.elements, strings.TrimPrefix(token, "#")) {
			value := "#" + e
			if strings.Contains(e, " ") {
				value = `#"` + e + `"`
			}
			m.suggestions = append(m.suggestions, suggestion{value: value + " ", label: "#" + e})
		}
	}
	if len(m.suggestions) > 7 {
		m.suggestions = m.suggestions[:7]
	}
}

// rank keeps the candidates containing the query, prefix and short first.
func rank(candidates []string, query string) []string {
	q := strings.ToLower(query)
	var prefix, contains []string
	for _, c := range candidates {
		lc := strings.ToLower(c)
		switch {
		case strings.HasPrefix(lc, q) || strings.HasPrefix(filepath.Base(lc), q):
			prefix = append(prefix, c)
		case strings.Contains(lc, q):
			contains = append(contains, c)
		}
	}
	out := slices.Concat(prefix, contains)
	if len(out) > 7 {
		out = out[:7]
	}
	return out
}

func (m *Model) complete(s suggestion) {
	value := m.input.Value()
	if strings.HasPrefix(s.value, "/") {
		m.setInput(s.value)
	} else {
		_, start := m.currentToken()
		m.setInput(value[:start] + s.value)
	}
	m.suggestions = nil
}

var builtins = []struct{ name, usage, help string }{
	{"help", "", "What the session can do"},
	{"resume", "[conversation]", "Pick up an earlier conversation in this repository"},
	{"new", "", "Start a new conversation"},
	{"clear", "", "Clear the screen"},
	{"exit", "", "Leave the session"},
}

func (m *Model) submit(text string) (tea.Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	m.history = append(m.history, text)
	m.histIdx = len(m.history)
	appendHistory(m.o.HistoryPath, text)
	echo := printLine("\n" + m.echo(text))

	if strings.HasPrefix(text, "/") {
		name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		switch name {
		case "exit", "quit", "q":
			return m, tea.Quit
		case "clear":
			return m, tea.ClearScreen
		case "new":
			m.conversation, m.actionsUsed, m.rounds = nil, 0, 0
			m.transcript = newTranscript(m.o.ProjectID)
			return m, printLine(m.s.muted.Render("  New conversation."))
		case "resume":
			return m.resume(strings.TrimSpace(args))
		case "help", "?":
			return m, tea.Batch(echo, printLine(m.help()))
		}
		cmd, ok := Lookup(name)
		if !ok {
			return m, tea.Batch(echo, printLine(m.s.danger.Render("  Unknown command /"+name)+m.s.muted.Render(" — /help lists them")))
		}
		if cmd.FullScreen {
			return m, tea.Batch(echo, m.exec(cmd, args))
		}
		return m, tea.Batch(echo, m.run(cmd, args, false))
	}

	if m.o.Client == nil {
		return m, tea.Batch(echo, printLine(m.s.warn.Render("  Sign in first: ")+m.s.accent.Render("/login")))
	}
	if len(text) > maxMessage-1024 {
		return m, tea.Batch(echo, printLine(m.s.danger.Render(fmt.Sprintf("  ✗ The question is too long: %d KB, at most %d KB.", len(text)>>10, (maxMessage-1024)>>10))))
	}
	m.record("question", text)
	content, notes := m.withMentions(text)
	printed := "\n" + m.echo(text)
	for _, note := range notes {
		m.record("note", note)
		printed += "\n" + m.s.muted.Render("  "+note)
	}
	m.conversation = append(m.conversation, api.ChatMessage{Role: "user", Content: content})
	m.actionsUsed, m.rounds = 0, 0
	return m, tea.Batch(printLine(printed), m.stream())
}

// echo is a question as the scrollback shows it.
func (m *Model) echo(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		prefix := "  "
		if i == 0 {
			prefix = m.s.accent.Render("› ")
		}
		lines[i] = prefix + m.s.title.Render(line)
	}
	return strings.Join(lines, "\n")
}

// withMentions adds what @files and #elements point at to a question: the
// files' content, and a hint to look them up in the architecture.
func (m *Model) withMentions(text string) (string, []string) {
	mentions := ParseMentions(text)
	if len(mentions.Files)+len(mentions.Elements) == 0 {
		return text, nil
	}
	content := text + "\n\n(Mentioned"
	if len(mentions.Files) > 0 {
		content += " files: " + strings.Join(mentions.Files, ", ") + ";"
	}
	if len(mentions.Elements) > 0 {
		content += " elements: " + strings.Join(mentions.Elements, ", ") + ";"
	}
	content += " look at them with brief or show before answering.)"
	if len(mentions.Files) == 0 {
		return content, nil
	}
	atts := Attach(m.o.Root, mentions.Files, maxMessage-1024-len(content))
	notes := make([]string, 0, len(atts))
	for _, a := range atts {
		notes = append(notes, a.Note())
	}
	return content + attachmentsBlock(atts), notes
}

func (m *Model) help() string {
	var b strings.Builder
	b.WriteString("\n" + m.s.title.Render("  Ask in plain language") + m.s.muted.Render(" — the assistant reads the architecture and runs commands here when it needs to.\n\n"))
	b.WriteString(m.s.title.Render("  Commands") + "\n")
	for _, c := range Catalog {
		label := "/" + c.Name
		if c.Usage != "" {
			label += " " + c.Usage
		}
		b.WriteString("  " + m.s.accent.Render(fmt.Sprintf("%-26s", label)) + m.s.muted.Render(c.Help) + "\n")
	}
	for _, bi := range builtins {
		label := "/" + bi.name
		if bi.usage != "" {
			label += " " + bi.usage
		}
		b.WriteString("  " + m.s.accent.Render(fmt.Sprintf("%-26s", label)) + m.s.muted.Render(bi.help) + "\n")
	}
	b.WriteString("\n" + m.s.muted.Render("  @file attaches a file and #element points at one · shift+enter new line · ↑↓ history · tab completes · esc stops · ctrl-d leaves") + "\n")
	return b.String()
}

// record keeps what the session printed, to print it again on resume.
func (m *Model) record(kind, text string) {
	m.transcript.Turns = append(m.transcript.Turns, Turn{Kind: kind, Text: text})
	if kind == "question" && m.transcript.Title == "" {
		m.transcript.Title = strings.Join(strings.Fields(text), " ")
	}
	m.save()
}

func (m *Model) save() {
	m.transcript.Conversation = m.conversation
	_ = saveTranscript(m.o.SessionDir, m.transcript)
}

// resumeLatest reopens the most recent conversation and returns it printed.
func (m *Model) resumeLatest() string {
	all := listTranscripts(m.o.SessionDir)
	if len(all) == 0 {
		return m.s.muted.Render("  No earlier conversation in this repository.")
	}
	return m.restore(all[0])
}

// resume reopens a conversation by ID, or lists them to pick one.
func (m *Model) resume(id string) (tea.Model, tea.Cmd) {
	if id == "" {
		if len(listTranscripts(m.o.SessionDir)) == 0 {
			return m, printLine(m.s.muted.Render("  No earlier conversation in this repository."))
		}
		m.setInput("/resume ")
		m.refreshSuggestions()
		return m, nil
	}
	t, err := loadTranscript(m.o.SessionDir, id)
	if err != nil {
		return m, printLine(m.s.danger.Render("  ✗ No conversation " + id))
	}
	return m, printLine(m.restore(t))
}

// restore makes t the conversation and returns it printed back.
func (m *Model) restore(t *Transcript) string {
	m.transcript = t
	m.conversation = append([]api.ChatMessage(nil), t.Conversation...)
	m.actionsUsed, m.rounds = 0, 0
	return m.replay(t)
}

func (m *Model) replay(t *Transcript) string {
	var b strings.Builder
	b.WriteString(m.s.subtle.Render(strings.Repeat("─", max(min(m.width, 100), 20))) + "\n")
	b.WriteString(m.s.muted.Render("  Resumed "+truncate(t.Title, 60)+" · "+ago(t.Updated)) + "\n")
	for _, turn := range t.Turns {
		switch turn.Kind {
		case "question":
			b.WriteString("\n" + m.echo(turn.Text) + "\n")
		case "action":
			b.WriteString(m.s.accent.Render("  ⏺ ") + m.s.text.Render(turn.Text) + "\n")
		case "answer":
			b.WriteString("\n" + tui.Markdown(turn.Text, min(m.width-4, 100), m.o.Dark) + "\n")
		default:
			b.WriteString(m.s.muted.Render("  "+turn.Text) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// runningCmd is a command running inline; cancel kills it.
type runningCmd struct {
	id     int
	label  string
	cancel context.CancelFunc
}

func (m *Model) argv(c Command, args string) []string {
	return append(c.Argv(args), m.o.GlobalArgs...)
}

// modelArgv is the command line of an action the assistant asked for: its
// arguments come after "--", so none of them is read as a flag.
func (m *Model) modelArgv(c Command, args string) ([]string, error) {
	tokens, err := c.ModelArgs(args)
	if err != nil {
		return nil, err
	}
	out := append(append([]string(nil), c.Base...), m.o.GlobalArgs...)
	if len(tokens) > 0 {
		out = append(append(out, "--"), tokens...)
	}
	return out, nil
}

func label(c Command, args string) string {
	return "archyl " + strings.Join(c.Argv(args), " ")
}

// confirmLabel is what a write prompt shows. The arguments can come from an
// injected instruction, so runs of blanks collapse and anything cut is counted:
// the person approving sees all of it, or knows how much they do not.
func confirmLabel(c Command, args string, width int) string {
	text := strings.Join(strings.Fields(ui.StripControl(label(c, args))), " ")
	if runes := []rune(text); len(runes) > maxConfirmLabel {
		text = fmt.Sprintf("%s … +%d characters", string(runes[:maxConfirmLabel]), len(runes)-maxConfirmLabel)
	}
	return ansi.Wrap(text, max(width-4, 20), " ")
}

// printLine prints above the session. Bubble Tea's own renderer filters what it
// draws, but this path writes straight to the terminal.
func printLine(line string) tea.Cmd {
	return tea.Println(ui.Sanitize(line))
}

// run executes a command and prints its output inline.
func (m *Model) run(c Command, args string, forAI bool) tea.Cmd {
	return m.runCall(c, args, forAI, "")
}

func (m *Model) runCall(c Command, args string, forAI bool, callID string) tea.Cmd {
	l := label(c, args)
	m.lastRun++
	id := m.lastRun
	done := cmdDone{id: id, name: c.Name + " " + args, label: l, forAI: forAI, callID: callID}
	argv := m.argv(c, args)
	if forAI {
		var err error
		if argv, err = m.modelArgv(c, args); err != nil {
			done.err = err
			done.output = err.Error()
			m.busy = "running"
			m.running = append(m.running, runningCmd{id: id, label: l, cancel: func() {}})
			return func() tea.Msg { return done }
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	m.busy = "running"
	m.running = append(m.running, runningCmd{id: id, label: l, cancel: cancel})
	exe, env := m.o.Exe, embeddedEnv(m.o.Dark, m.width)
	return func() tea.Msg {
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, argv...)
		cmd.Env = env
		killTree(cmd)
		cmd.WaitDelay = time.Second
		out, err := cmd.CombinedOutput()
		done.output, done.err = string(out), err
		return done
	}
}

// doneRunning takes a finished command off the running list. It reports
// false when the command is not on it: the user stopped it.
func (m *Model) doneRunning(id int) bool {
	found := false
	for i, r := range m.running {
		if r.id == id {
			m.running = append(m.running[:i:i], m.running[i+1:]...)
			found = true
			break
		}
	}
	if len(m.running) == 0 && m.busy == "running" {
		m.busy = ""
	}
	return found
}

func (m *Model) finishCommand(msg cmdDone) (tea.Model, tea.Cmd) {
	if msg.callID != "" {
		return m.finishTool(msg)
	}
	found := m.doneRunning(msg.id)
	if !found {
		return m, nil // stopped by the user
	}
	if !msg.forAI {
		out := strings.TrimRight(msg.output, "\n")
		if out == "" && msg.err != nil {
			out = m.s.danger.Render("  ✗ " + msg.err.Error())
		}
		return m, printLine(out)
	}
	m.conversation = append(m.conversation, api.ChatMessage{Role: "user", Content: Observation(strings.TrimSpace(msg.name), msg.output, msg.err)})
	return m, tea.Batch(printLine(m.preview(msg.output)), m.stream())
}

func (m *Model) runningLabels() []string {
	labels := make([]string, len(m.running))
	for i, r := range m.running {
		labels[i] = r.label
	}
	return labels
}

// preview is a command's output shortened under its action line.
func (m *Model) preview(output string) string {
	shown, hidden := Preview(ansi.Strip(output), 6)
	preview := indent(m.s.muted.Render(shown), 4)
	if hidden > 0 {
		preview += "\n" + m.s.subtle.Render(fmt.Sprintf("    … +%d lines", hidden))
	}
	return preview
}

// exec hands the terminal to a full-screen command and takes it back.
func (m *Model) exec(c Command, args string) tea.Cmd {
	m.busy = "running"
	cmd := exec.Command(m.o.Exe, m.argv(c, args)...)
	cmd.Env = append(os.Environ(), "ARCHYL_NO_UPDATE_NOTIFIER=1")
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return execDone{err: err} })
}

func embeddedEnv(dark bool, width int) []string {
	theme := "light"
	if dark {
		theme = "dark"
	}
	return append(os.Environ(),
		"ARCHYL_EMBEDDED=1", "CLICOLOR_FORCE=1", "ARCHYL_NO_UPDATE_NOTIFIER=1",
		"ARCHYL_THEME="+theme, fmt.Sprintf("COLUMNS=%d", max(width-2, 60)))
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var parts []string
	dots := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spin := m.s.accent.Render(dots[m.frame%len(dots)])

	switch {
	case m.confirm != nil:
		parts = append(parts, "", m.s.warn.Render("  The assistant wants to run"), m.s.title.Render(indent(confirmLabel(m.confirm.cmd, m.confirm.args, m.width), 2)),
			m.s.muted.Render("  It changes something. ")+m.s.accent.Render("y")+m.s.muted.Render(" run · ")+m.s.accent.Render("n")+m.s.muted.Render(" skip"))
	case m.busy == "thinking":
		text := m.live.String()
		if text == "" || MaybeAction(text) {
			parts = append(parts, "", "  "+spin+m.s.muted.Render(" thinking…"))
		} else {
			rendered := tui.Markdown(text, min(m.width-4, 100), m.o.Dark)
			lines := strings.Split(rendered, "\n")
			if len(lines) > 18 {
				lines = lines[len(lines)-18:]
			}
			parts = append(parts, "", strings.Join(lines, "\n"), "  "+spin+m.s.muted.Render(" writing…"))
		}
	case m.busy == "running":
		parts = append(parts, "", "  "+spin+m.s.muted.Render(" "+strings.Join(m.runningLabels(), " · ")))
	}

	rule := m.s.subtle.Render(strings.Repeat("─", max(m.width, 20)))
	parts = append(parts, "", rule, m.input.View())
	for i, sug := range m.suggestions {
		label := fmt.Sprintf("%-30s", sug.label)
		if i == m.sel {
			parts = append(parts, "  "+m.s.accent.Bold(true).Render(label)+"  "+m.s.text.Render(sug.help))
		} else {
			parts = append(parts, "  "+m.s.muted.Render(label)+"  "+m.s.subtle.Render(sug.help))
		}
	}
	parts = append(parts, rule, m.statusLine())
	return tea.NewView(strings.Join(parts, "\n"))
}

func (m *Model) statusLine() string {
	var left []string
	if m.o.Project != "" {
		left = append(left, m.o.Project)
	}
	if m.o.Branch != "" {
		left = append(left, m.o.Branch)
	}
	if m.o.HasScore {
		left = append(left, fmt.Sprintf("alignment %d", m.o.Score))
	}
	if len(m.conversation) > 0 {
		left = append(left, fmt.Sprintf("%d messages", len(m.conversation)))
	}
	l := m.s.accent.Render("◆ ") + m.s.muted.Render(strings.Join(left, " · "))
	r := m.s.subtle.Render("/help · shift+enter newline · esc stop · ctrl-d quit")
	gap := max(m.width-lipgloss.Width(l)-lipgloss.Width(r), 2)
	return l + strings.Repeat(" ", gap) + r
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i := range lines {
		if lines[i] != "" {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

func loadHistory(path string) []string {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}
	return lines
}

func appendHistory(path, line string) {
	if path == "" || strings.ContainsAny(line, "\n\r") {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line + "\n")
}
