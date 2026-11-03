// Package shell is the archyl session: a prompt where you ask about the
// architecture in plain language or type /commands, Codex-style. Answers and
// command output flow into the terminal's scrollback; full-screen views
// (map, runs, search) take the screen and give it back.
package shell

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/archyl-com/cli/internal/history"
)

// Command is something the session can run: by the user as /name, and, when
// AI is set, by the assistant as an action.
type Command struct {
	Name  string
	Usage string
	Help  string
	// Base is the archyl command line the arguments are added to.
	Base []string
	// Split turns the arguments typed after the name into argv tokens; nil
	// when the command takes none.
	Split func(args string) []string
	// FullScreen commands take over the terminal instead of printing inline.
	FullScreen bool
	// AI exposes the command to the assistant.
	AI bool
	// Writes asks the user before the assistant runs it.
	Writes bool
}

// Argv builds the archyl command line from the arguments the user typed after
// the name. The user may pass flags; the assistant goes through ModelArgs.
func (c Command) Argv(args string) []string {
	out := append([]string(nil), c.Base...)
	if c.Split != nil {
		out = append(out, c.Split(args)...)
	}
	return out
}

// ErrModelArgs is why arguments chosen by the assistant were refused.
var ErrModelArgs = errors.New("refused arguments")

// ModelArgs splits arguments the assistant chose into argv tokens. They are
// data, never flags: a token that starts with "-" is refused, so the model
// cannot reach a flag such as diagram's --output, and the caller still puts
// "--" before them.
func (c Command) ModelArgs(args string) ([]string, error) {
	if c.Split == nil {
		if strings.TrimSpace(args) != "" {
			return nil, fmt.Errorf("%w: %s takes no arguments; call it with none", ErrModelArgs, c.Name)
		}
		return nil, nil
	}
	tokens := c.Split(args)
	for _, tok := range tokens {
		if strings.HasPrefix(tok, "-") {
			return nil, fmt.Errorf("%w: %q starts with \"-\"; flags cannot be passed, only %s", ErrModelArgs, tok, c.Usage)
		}
	}
	return tokens, nil
}

// whole passes the arguments as one token.
func whole(args string) []string {
	if args = strings.TrimSpace(args); args != "" {
		return []string{args}
	}
	return nil
}

// firstAndRest splits "3f2a9c4e keep the v1 endpoint" into the run and the text.
func firstAndRest(args string) []string {
	head, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	out := []string{head}
	if rest = strings.TrimSpace(rest); rest != "" {
		out = append(out, rest)
	}
	return out
}

// rangeAndRest splits "v1.2..v2.0 Billing API" into the range and the
// element. Text that does not start with a range or a point is one token: an
// element, or a release.
func rangeAndRest(args string) []string {
	args = strings.TrimSpace(args)
	head, _, _ := strings.Cut(args, " ")
	if history.IsRange(head) || history.LooksLikePoint(head) {
		return firstAndRest(args)
	}
	return whole(args)
}

// Catalog lists the session's commands.
var Catalog = []Command{
	{Name: "impact", Help: "What the current branch affects, and who owns it", Base: []string{"impact"}, AI: true},
	{Name: "check", Help: "Architecture rule violations in the files the branch changes", Base: []string{"check", "--fail-on", "never", "--format", "pretty"}, AI: true},
	{Name: "review", Help: "Rules, impact, owners and alignment for the branch, before the PR", Base: []string{"review", "--fail-on", "never"}, AI: true},
	{Name: "brief", Usage: "<files>", Help: "What to know before editing these files: elements, dependents, decisions, pitfalls", Base: []string{"brief"}, Split: strings.Fields, AI: true},
	{Name: "where", Usage: "<files>", Help: "Which C4 element files belong to", Base: []string{"where"}, Split: strings.Fields, AI: true},
	{Name: "why", Usage: "<file[:line]>", Help: "Why code is the way it is: its elements, decisions, owners, memories, rules and last commit", Base: []string{"why"}, Split: whole, AI: true},
	{Name: "show", Usage: "<element | ADR-n>", Help: "An element with its relationships and blast radius, or a decision", Base: []string{"show"}, Split: whole, AI: true},
	{Name: "diagram", Usage: "[element]", Help: "A C4 diagram: the system context, or centred on one element of any level and its neighbours", Base: []string{"diagram", "--format", "text"}, Split: whole, AI: true},
	{Name: "incident", Usage: "<element>", Help: "Everything to act on an element during an incident: owners, blast radius, recent changes, runbooks, who to tell", Base: []string{"incident"}, Split: whole, AI: true},
	{Name: "tour", Usage: "[element]", Help: "A guided walk through the codebase: the parts to meet first, in the order requests flow, with the files to read", Base: []string{"tour"}, Split: whole, AI: true},
	{Name: "history", Usage: "[from..to] [element]", Help: "How the architecture changed between two points (versions, releases, dates, 30d..): elements and relationships added, removed and modified", Base: []string{"history"}, Split: rangeAndRest, AI: true},
	{Name: "inside", Usage: "<element>", Help: "The C4 view inside an element: a system's containers or a container's components", Base: []string{"diagram", "--format", "text", "--inside"}, Split: whole, AI: true},
	{Name: "plan", Usage: "<task>", Help: "A plan for a task, grounded in the architecture", Base: []string{"plan"}, Split: whole, AI: true},
	{Name: "recall", Usage: "<topic>", Help: "What Archyl remembers: conventions, pitfalls, notes", Base: []string{"recall"}, Split: whole, AI: true},
	{Name: "radar", Help: "Dependencies checked against the technology radar", Base: []string{"radar", "check", "--fail-on", "never", "--format", "pretty"}, AI: true},
	{Name: "drift", Help: "How well the model matches the code", Base: []string{"drift"}, AI: true},
	{Name: "diff", Help: "What pushing archyl.yaml would change in the model", Base: []string{"dsl", "diff"}, AI: true},
	{Name: "activity", Help: "What happened lately: changes, reviews, agent runs", Base: []string{"activity", "--limit", "20"}, AI: true},
	{Name: "runs", Help: "Managed agent runs and their status", Base: []string{"runs"}, AI: true},
	{Name: "sessions", Help: "Agents at work under the harness", Base: []string{"agents"}, AI: true},
	{Name: "adrs", Help: "The project's decisions", Base: []string{"adr"}, AI: true},
	{Name: "crs", Help: "Architecture change requests", Base: []string{"cr"}, AI: true},
	{Name: "inbox", Help: "Your notifications", Base: []string{"inbox"}, AI: true},
	{Name: "status", Help: "Alignment, branch and agents at a glance", Base: []string{"status"}, AI: true},
	{Name: "doctor", Help: "Check the setup and how to fix it", Base: []string{"doctor"}},

	{Name: "remember", Usage: "<fact>", Help: "Teach Archyl something the code cannot show", Base: []string{"remember", "--kind", "note"}, Split: whole, AI: true, Writes: true},
	{Name: "answer-run", Usage: "<run> <answer>", Help: "Answer the question a managed agent run waits on", Base: []string{"runs", "answer"}, Split: firstAndRest, AI: true, Writes: true},
	{Name: "approve-run", Usage: "<run>", Help: "Approve a run's plan, or a run held for approval", Base: []string{"runs", "approve"}, Split: firstAndRest, AI: true, Writes: true},
	{Name: "steer-run", Usage: "<run> <message>", Help: "Tell a running agent something", Base: []string{"runs", "steer"}, Split: firstAndRest, AI: true, Writes: true},
	{Name: "start-run", Usage: "<task>", Help: "Start a managed agent run on a task", Base: []string{"runs", "start"}, Split: whole, AI: true, Writes: true},
	{Name: "discover", Help: "Start an AI discovery on the project's repositories; results wait for review", Base: []string{"discover", "--no-wait"}, AI: true, Writes: true},

	{Name: "fix", Help: "Hand the branch's rule violations to a coding agent under the harness", Base: []string{"fix"}, FullScreen: true, Writes: true},
	{Name: "map", Usage: "[element]", Help: "Explore the C4 model full screen", Base: []string{"map"}, Split: whole, FullScreen: true},
	{Name: "search", Usage: "[query]", Help: "Search elements, decisions, docs", Base: []string{"search"}, Split: whole, FullScreen: true},
	{Name: "run", Usage: "[run]", Help: "Follow a managed agent run live and act on it", Base: []string{"runs", "view"}, Split: whole, FullScreen: true},
	{Name: "agents", Help: "Watch the agents at work, live", Base: []string{"agents"}, FullScreen: true},
	{Name: "setup", Help: "Connect your coding agents", Base: []string{"setup"}, FullScreen: true},
	{Name: "init", Help: "Link this repository to a project", Base: []string{"init"}, FullScreen: true},
	{Name: "login", Help: "Sign in", Base: []string{"login"}, FullScreen: true},
}

// Lookup finds a command by name.
func Lookup(name string) (Command, bool) {
	for _, c := range Catalog {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// Matching lists the commands whose name starts with prefix, by name.
func Matching(prefix string) []Command {
	var out []Command
	for _, c := range Catalog {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
