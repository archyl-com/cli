package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/report"
	"github.com/archyl-com/cli/internal/ui"
)

// fixResult is what `archyl fix` did, for --json.
type fixResult struct {
	Base      string          `json:"base,omitempty"`
	Agent     string          `json:"agent,omitempty"`
	Selected  []api.Violation `json:"selected"`
	Prompt    string          `json:"prompt,omitempty"`
	ExitCode  *int            `json:"agentExitCode,omitempty"`
	Remaining []api.Violation `json:"remaining,omitempty"`
	// NotHandedOver are the violations the second check still reports that
	// were not selected; they do not fail the command.
	NotHandedOver []api.Violation `json:"notHandedOver,omitempty"`
	Fixed         int             `json:"fixed"`
}

func newFixCmd(app *App) *cobra.Command {
	var base, severity, agent string
	var printOnly, yes, changeRequest bool

	cmd := &cobra.Command{
		Use:   "fix [flags] [-- <agent> [args...]]",
		Short: "Hand the branch's rule violations to a coding agent",
		Long: `Run the conformance check on the branch (like archyl check), let you pick the
violations to fix, and hand them to a coding agent inside a work session: each
violation with its rule, severity, location and suggestion, plus the brief of
the elements the files belong to and the decisions that apply.

When the agent exits, the check runs again and shows what is left. Nothing is
committed: review the changes with git diff.

The agent is Claude Code or Codex, whichever is installed (--agent picks one).
After --, any command runs instead; it reads the task from
$ARCHYL_FIX_TASK_FILE. --print writes the task for any other agent.`,
		Example: `  archyl fix
  archyl fix --severity high --agent codex
  archyl fix --print | pbcopy
  archyl fix -- claude --model opus`,
		RunE: func(cmd *cobra.Command, args []string) error {
			minSeverity := report.Severity(severity)
			if minSeverity == 0 {
				return fmt.Errorf("--severity must be critical, high, medium or low, not %q", severity)
			}
			return runFix(cmd.Context(), app, fixOptions{
				Base: base, MinSeverity: minSeverity, Agent: agent, Command: args,
				PrintOnly: printOnly, Yes: yes, ChangeRequest: changeRequest,
			})
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "ref to compare with (default: the pull request target, or origin's default branch)")
	cmd.Flags().StringVar(&severity, "severity", "low", "lowest severity to fix: critical, high, medium or low")
	cmd.Flags().StringVar(&agent, "agent", "", "agent to run: claude, codex or gemini (default: the first one installed)")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the task prompt instead of running an agent")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "fix every violation without asking")
	cmd.Flags().BoolVar(&changeRequest, "change-request", false, "open a draft Architecture Change Request when the session closes")
	return cmd
}

type fixOptions struct {
	Base        string
	MinSeverity int
	Agent       string
	Command     []string
	PrintOnly   bool
	Yes         bool
	// ChangeRequest opens a draft change request when the session closes.
	ChangeRequest bool
	// LookPath finds agents; exec.LookPath by default.
	LookPath func(string) (string, error)
}

func runFix(ctx context.Context, app *App, opts fixOptions) error {
	p := app.Printer
	env := ci.Detect(os.Getenv)
	git, err := app.Repo(ctx)
	if err != nil {
		return err
	}
	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	base, err := resolveBase(ctx, git, opts.Base, env)
	if err != nil {
		return err
	}

	var before *checkRun
	if err := p.Spin("Checking changes against the architecture rules…", func() error {
		before, err = runCheck(ctx, app, git, project.ID, base, env)
		return err
	}); err != nil {
		return err
	}
	names := before.Result.RuleNames()
	violations := atLeast(sortedViolations(before.Result.Violations), opts.MinSeverity)
	result := &fixResult{Base: base, Selected: []api.Violation{}}
	if len(violations) == 0 {
		if p.JSON {
			return p.WriteJSON(result)
		}
		p.Println(p.Check() + " " + p.Bold("Nothing to fix") + p.Muted(" · no violation in "+ui.Plural(len(before.Changes), "changed file", "changed files")))
		return nil
	}

	selected := violations
	if !opts.PrintOnly && !opts.Yes && p.Interactive() {
		for _, v := range violations {
			p.Println()
			p.Println(severityBadge(p, v.Severity) + " " + p.Bold(v.Title) + "  " + p.Muted(names[v.RuleID]))
			p.Println(codeFrame(p, git.Root, v))
		}
		p.Println()
		if selected, err = pickViolations(violations, names); err != nil {
			return err
		}
	}
	result.Selected = selected

	var brief *briefData
	if paths := violationPaths(selected); len(paths) > 0 {
		_ = p.Spin("Gathering the architecture around them…", func() error {
			brief, _ = gatherBrief(ctx, client, project.ID, paths)
			return nil
		})
	}
	prompt := fixPrompt(base, selected, before.Result.Rules, brief)

	if opts.PrintOnly {
		if p.JSON {
			result.Prompt = prompt
			return p.WriteJSON(result)
		}
		_, _ = fmt.Fprintln(p.Out, prompt)
		return nil
	}

	taskFile, err := writeTaskFile(prompt)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(taskFile) }()

	argv, agentName, err := fixAgentArgs(opts.Agent, opts.Command, taskFile, opts.LookPath)
	if err != nil {
		return err
	}
	result.Agent = agentName
	if !opts.Yes && p.Interactive() {
		proceed := true
		if err := huh.NewConfirm().
			Title(fmt.Sprintf("Hand %s to %s?", ui.Plural(len(selected), "violation", "violations"), agentName)).
			Description("It works in this tree under a work session. Nothing is committed.").
			Value(&proceed).
			Run(); err != nil {
			return err
		}
		if !proceed {
			return nil
		}
	}

	code, err := runHarnessed(ctx, app, git, project, harnessRun{
		Task:          fixSessionTask(selected, names),
		AgentName:     agentName,
		Args:          argv,
		ChangeRequest: opts.ChangeRequest,
		Env:           []string{"ARCHYL_FIX_TASK_FILE=" + taskFile},
	})
	if err != nil {
		return err
	}
	result.ExitCode = &code

	var after *checkRun
	if err := p.Spin("Checking again…", func() error {
		after, err = runCheck(context.WithoutCancel(ctx), app, git, project.ID, base, env)
		return err
	}); err != nil {
		return err
	}
	remaining, others := splitRemaining(selected, atLeast(sortedViolations(after.Result.Violations), opts.MinSeverity))
	result.Remaining, result.NotHandedOver = remaining, others
	result.Fixed = len(selected) - len(remaining)

	if p.JSON {
		if err := p.WriteJSON(result); err != nil {
			return err
		}
	} else {
		renderFixOutcome(p, len(selected), remaining, others, result.Fixed, names, git.Root)
	}
	if code != 0 {
		return &ExitError{Code: code}
	}
	if len(remaining) > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

// atLeast keeps the violations at or above a severity.
func atLeast(violations []api.Violation, minSeverity int) []api.Violation {
	out := make([]api.Violation, 0, len(violations))
	for _, v := range violations {
		if report.Severity(v.Severity) >= minSeverity {
			out = append(out, v)
		}
	}
	return out
}

func pickViolations(violations []api.Violation, names map[string]string) ([]api.Violation, error) {
	options := make([]huh.Option[int], 0, len(violations))
	for i, v := range violations {
		label := fmt.Sprintf("%-8s %s  %s", strings.ToUpper(v.Severity), v.Title, violationLocation(v))
		if name := names[v.RuleID]; name != "" {
			label += "  (" + name + ")"
		}
		options = append(options, huh.NewOption(truncate(label, 110), i).Selected(true))
	}
	var picked []int
	err := huh.NewMultiSelect[int]().
		Title("Which violations should the agent fix?").
		Options(options...).
		Value(&picked).
		Validate(func(v []int) error {
			if len(v) == 0 {
				return errors.New("pick at least one")
			}
			return nil
		}).
		Run()
	if err != nil {
		return nil, err
	}
	out := make([]api.Violation, 0, len(picked))
	for _, i := range picked {
		out = append(out, violations[i])
	}
	return out, nil
}

func violationLocation(v api.Violation) string {
	where := firstNonEmpty(v.FilePath, "(whole model)")
	if v.LineStart > 0 {
		where += ":" + strconv.Itoa(v.LineStart)
		if v.LineEnd > v.LineStart {
			where += "-" + strconv.Itoa(v.LineEnd)
		}
	}
	return where
}

func violationPaths(violations []api.Violation) []string {
	seen := map[string]bool{}
	var paths []string
	for _, v := range violations {
		if v.FilePath != "" && !seen[v.FilePath] {
			seen[v.FilePath] = true
			paths = append(paths, v.FilePath)
		}
	}
	return paths
}

// fixPrompt is the task handed to the agent: every violation in full, then
// the architecture the touched files sit in.
func fixPrompt(base string, violations []api.Violation, rules []api.Rule, brief *briefData) string {
	byID := make(map[string]api.Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	var b strings.Builder
	what := "the architecture violation below"
	if len(violations) > 1 {
		what = fmt.Sprintf("the %d architecture violations below", len(violations))
	}
	fmt.Fprintf(&b, "Fix %s, reported by `archyl check` on the changes of this branch", what)
	if base != "" {
		fmt.Fprintf(&b, " against `%s`", base)
	}
	b.WriteString(".\n\n")
	b.WriteString(`- Change the code so each violation is resolved; follow the suggestion when there is one.
- Keep the change to what the fix needs: no unrelated refactoring.
- Respect the decisions in the brief. If a fix would contradict one, stop and explain instead.
- Do not commit: leave the changes in the working tree for review.
- When you are done, run ` + "`archyl check --fail-on never`" + ` to confirm the violations are gone.

`)
	fmt.Fprintf(&b, "## Violations\n")
	for i, v := range violations {
		fmt.Fprintf(&b, "\n### %d. [%s] %s\n\n", i+1, strings.ToUpper(v.Severity), v.Title)
		if rule, ok := byID[v.RuleID]; ok {
			fmt.Fprintf(&b, "- **Rule:** %s", rule.Name)
			if d := strings.TrimSpace(rule.Description); d != "" {
				fmt.Fprintf(&b, " — %s", d)
			}
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- **Location:** `%s`\n", violationLocation(v))
		if d := strings.TrimSpace(v.Description); d != "" {
			fmt.Fprintf(&b, "- **Finding:** %s\n", d)
		}
		if s := strings.TrimSpace(v.Suggestion); s != "" {
			fmt.Fprintf(&b, "- **Suggestion:** %s\n", s)
		}
	}
	if brief != nil && len(brief.Elements) > 0 {
		b.WriteString("\n")
		b.WriteString(demoteHeadings(brief.markdown()))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// demoteHeadings nests a markdown document one level down.
func demoteHeadings(md string) string {
	lines := strings.Split(md, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "#") {
			lines[i] = "#" + line
		}
	}
	return strings.Join(lines, "\n")
}

func fixSessionTask(violations []api.Violation, names map[string]string) string {
	seen := map[string]bool{}
	var rules []string
	for _, v := range violations {
		name := firstNonEmpty(names[v.RuleID], v.Title)
		if !seen[name] {
			seen[name] = true
			rules = append(rules, name)
		}
	}
	return truncate(fmt.Sprintf("Fix %s: %s", ui.Plural(len(violations), "architecture violation", "architecture violations"), strings.Join(rules, ", ")), 200)
}

// fixAgentArgs builds the agent's command line and names the agent. A custom
// command runs as given, with a prompt pointing at the task file added for
// the agents known to take one as an argument: the task itself can be too
// long for a command line.
func fixAgentArgs(agent string, custom []string, taskFile string, lookPath func(string) (string, error)) ([]string, string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	prompt := fixTaskPointer(taskFile)
	if len(custom) > 0 {
		argv := append([]string(nil), custom...)
		if withPrompt := agentPromptArgs(filepath.Base(argv[0]), prompt); withPrompt != nil {
			argv = append(argv, withPrompt...)
		}
		return argv, firstNonEmpty(agent, filepath.Base(custom[0])), nil
	}
	if agent == "" {
		for _, candidate := range []string{"claude", "codex"} {
			if _, err := lookPath(candidate); err == nil {
				agent = candidate
				break
			}
		}
		if agent == "" {
			return nil, "", errors.New("no coding agent found: install Claude Code or Codex, pass --agent, or give a command after --")
		}
	}
	if _, err := lookPath(agent); err != nil {
		return nil, "", fmt.Errorf("%s is not installed (not on PATH)", agent)
	}
	return append([]string{agent}, agentPromptArgs(agent, prompt)...), agent, nil
}

// agentPromptArgs is how an agent takes an initial prompt in an interactive
// session; nil when it does not take one on the command line.
func agentPromptArgs(agent, prompt string) []string {
	switch strings.TrimSuffix(agent, ".exe") {
	case "claude", "codex":
		return []string{prompt}
	case "gemini":
		return []string{"--prompt-interactive", prompt}
	}
	return nil
}

// fixTaskPointer is the prompt handed to the agent: where to read the task.
func fixTaskPointer(taskFile string) string {
	return fmt.Sprintf("Fix the architecture violations described in %s (also in $ARCHYL_FIX_TASK_FILE). Read that file first and follow its instructions.", taskFile)
}

func writeTaskFile(prompt string) (string, error) {
	f, err := os.CreateTemp("", "archyl-fix-*.md")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(prompt + "\n"); err != nil {
		return "", err
	}
	return filepath.Abs(f.Name())
}

// splitRemaining sorts what the second check reports into the selected
// violations still there and the others, which were never handed over.
// Violation IDs change from one check to the next and the fix moves lines,
// so they are matched on rule, file and finding, as many times as selected.
func splitRemaining(selected, after []api.Violation) (remaining, others []api.Violation) {
	key := func(v api.Violation) string { return v.RuleID + "\x00" + v.FilePath + "\x00" + v.Title }
	handed := map[string]int{}
	for _, v := range selected {
		handed[key(v)]++
	}
	for _, v := range after {
		if handed[key(v)] > 0 {
			handed[key(v)]--
			remaining = append(remaining, v)
			continue
		}
		others = append(others, v)
	}
	return remaining, others
}

func renderFixOutcome(p *ui.Printer, before int, remaining, others []api.Violation, fixed int, names map[string]string, root string) {
	p.Println()
	for _, v := range remaining {
		head := severityBadge(p, v.Severity) + " " + p.Bold(v.Title)
		if name := names[v.RuleID]; name != "" {
			head += "  " + p.Muted(name)
		}
		p.Println(head)
		p.Println(codeFrame(p, root, v))
		p.Println()
	}
	mark := p.Check()
	if len(remaining) > 0 {
		mark = p.Cross()
	}
	p.Println(mark + " " + p.Bold(fmt.Sprintf("%s → %d", ui.Plural(before, "violation", "violations"), len(remaining))) +
		p.Muted(fmt.Sprintf(" · %d fixed · nothing committed, review with git diff", fixed)))
	if len(others) > 0 {
		p.Println(p.Muted(fmt.Sprintf("  %s not handed over:", ui.Plural(len(others), "violation", "violations"))))
		for _, v := range others {
			p.Println(p.Muted("    " + strings.ToUpper(v.Severity) + " " + v.Title + "  " + violationLocation(v)))
		}
	}
}
