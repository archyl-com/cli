package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/report"
	"github.com/archyl-com/cli/internal/ui"
)

// checkRun is a conformance check over a set of changes.
type checkRun struct {
	Base    string                      `json:"base,omitempty"`
	Changes []repo.Change               `json:"changedFiles"`
	Result  *api.ConformanceCheckResult `json:"result"`
	// Skipped lists changed files whose content was not sent.
	Skipped []skippedFile `json:"skippedFiles,omitempty"`
}

// runCheck sends the changed files to the project's conformance rules.
func runCheck(ctx context.Context, app *App, git *repo.Git, projectID, base string, env ci.Env) (*checkRun, error) {
	client, err := app.Client()
	if err != nil {
		return nil, err
	}
	changes, err := git.Changes(ctx, base)
	if err != nil {
		return nil, err
	}
	run := &checkRun{Base: base, Changes: changes, Result: &api.ConformanceCheckResult{}}
	if len(changes) == 0 {
		return run, nil
	}

	// Batches, not a cut: a pull request must not pass by being large.
	for chunk := range slices.Chunk(changes, maxPaths) {
		files := make([]api.ChangedFile, 0, len(chunk))
		for _, c := range chunk {
			files = append(files, api.ChangedFile{Path: c.Path, Status: c.Status})
		}
		contents, skipped := readChanged(git.Root, chunk)
		run.Skipped = append(run.Skipped, skipped...)
		result, err := client.CheckConformance(ctx, projectID, api.ConformanceCheckRequest{
			CommitSHA:    firstNonEmpty(env.Commit, git.Head(ctx)),
			Branch:       firstNonEmpty(env.Branch, git.Branch(ctx)),
			ChangedFiles: files,
			FileContents: contents,
		})
		if err != nil {
			return nil, err
		}
		run.Result.Merge(result)
	}
	return run, nil
}

func newCheckCmd(app *App) *cobra.Command {
	var base, failOnFlag, format, output string
	var failOpen bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check changed files against the architecture rules",
		Long: `Run the project's conformance rules on every file that differs from the base
branch, and fail when a violation reaches --fail-on.

In CI the base is the pull request's target branch, and the output adapts:

  GitHub Actions   inline annotations on the pull request, plus a job summary
  GitLab CI        a Code Quality report in gl-code-quality-report.json
                   (declare it under artifacts:reports:codequality)
  anywhere         --format sarif for code scanning dashboards

CI checkouts are often shallow: fetch the base branch, or the full history.`,
		Example: `  archyl check
  archyl check --base origin/develop --fail-on critical
  archyl check --format sarif -o archyl.sarif`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			failOn, err := report.ParseFailOn(failOnFlag)
			if err != nil {
				return err
			}
			err = check(cmd.Context(), app, base, format, output, failOn)
			var exit *ExitError
			if err != nil && failOpen && !errors.As(err, &exit) {
				app.Printer.Eprintln(app.Printer.Bang() + " " + app.Printer.Warning("archyl check skipped: ") + app.Printer.Muted(err.Error()))
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "ref to compare with (default: the pull request target, or origin's default branch)")
	cmd.Flags().StringVar(&failOnFlag, "fail-on", "high", "lowest severity that fails: critical, high, medium, low or never")
	cmd.Flags().StringVar(&format, "format", "auto", "pretty, json, github, gitlab or sarif (auto picks for the CI)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the gitlab or sarif report to a file")
	cmd.Flags().BoolVar(&failOpen, "fail-open", false, "exit 0 when the check cannot run (offline, signed out); only violations fail")
	return cmd
}

func check(ctx context.Context, app *App, base, format, output string, failOn report.FailOn) error {
	p := app.Printer
	env := ci.Detect(os.Getenv)
	format = resolveFormat(format, p.JSON, env)

	git, err := app.Repo(ctx)
	if err != nil {
		return err
	}
	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	base, err = resolveBase(ctx, git, base, env)
	if err != nil {
		return err
	}

	var run *checkRun
	if err := p.Spin("Checking changes against the architecture rules…", func() error {
		run, err = runCheck(ctx, app, git, project.ID, base, env)
		return err
	}); err != nil {
		return err
	}

	if err := writeCheck(p, run, format, output, env, git.Root); err != nil {
		return err
	}
	if failOn.Fails(run.Result.Violations) {
		return &ExitError{Code: 1}
	}
	return nil
}

func resolveFormat(format string, jsonFlag bool, env ci.Env) string {
	if jsonFlag {
		return "json"
	}
	if format != "auto" {
		return format
	}
	switch env.Provider {
	case ci.GitHub:
		return "github"
	case ci.GitLab:
		return "gitlab"
	}
	return "pretty"
}

func writeCheck(p *ui.Printer, run *checkRun, format, output string, env ci.Env, root string) error {
	names := run.Result.RuleNames()
	violations := sortedViolations(run.Result.Violations)

	switch format {
	case "json":
		return p.WriteJSON(run)
	case "sarif":
		return withOutput(p, output, func(w io.Writer) error {
			return report.SARIF(w, violations, run.Result.Rules, buildinfo.Version)
		})
	case "gitlab":
		if output == "" {
			output = "gl-code-quality-report.json"
		}
		if err := withOutput(p, output, func(w io.Writer) error {
			return report.GitLabCodeQuality(w, violations, names)
		}); err != nil {
			return err
		}
		renderCheck(p, run, violations, names, root)
		p.Println(p.Muted("Code Quality report written to " + output))
		return nil
	case "github":
		if err := report.GitHubAnnotations(p.Out, violations, names); err != nil {
			return err
		}
		renderCheck(p, run, violations, names, root)
		if env.StepSummary != "" {
			_ = appendFile(env.StepSummary, checkSummaryMarkdown(run, violations, names))
		}
		return nil
	case "pretty":
		renderCheck(p, run, violations, names, root)
		return nil
	}
	return fmt.Errorf("unknown format %q", format)
}

func withOutput(p *ui.Printer, path string, write func(io.Writer) error) error {
	if path == "" {
		return write(p.Out)
	}
	if err := repo.RefuseLink(path); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return write(f)
}

func appendFile(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}

func sortedViolations(in []api.Violation) []api.Violation {
	out := append([]api.Violation(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := report.Severity(out[i].Severity), report.Severity(out[j].Severity)
		if si != sj {
			return si > sj
		}
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		return out[i].LineStart < out[j].LineStart
	})
	return out
}

func severityBadge(p *ui.Printer, severity string) string {
	t := p.Theme()
	c := t.Muted
	switch report.Severity(severity) {
	case 4:
		c = t.Danger
	case 3:
		c = t.Warning
	case 2:
		c = t.Accent
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(fmt.Sprintf("%-8s", strings.ToUpper(severity)))
}

func renderCheck(p *ui.Printer, run *checkRun, violations []api.Violation, names map[string]string, root string) {
	if len(run.Changes) == 0 {
		p.Println(p.Muted("No change against " + firstNonEmpty(run.Base, "HEAD") + "; nothing to check."))
		return
	}
	for _, v := range violations {
		p.Println()
		// One line: a newline in a title could start a workflow command in CI logs.
		head := severityBadge(p, v.Severity) + " " + p.Bold(strings.Join(strings.Fields(v.Title), " "))
		if name := names[v.RuleID]; name != "" {
			head += "  " + p.Muted(name)
		}
		p.Println(head)
		p.Println(codeFrame(p, root, v))
	}
	p.Println()
	p.Println(checkVerdict(p, run))
	if len(run.Skipped) > 0 {
		p.Println(p.Warning(ui.Plural(len(run.Skipped), "file", "files")+" not read:") + " " + p.Muted(skippedSummary(run.Skipped)))
	}
}

// skippedSummary counts skipped files by reason.
func skippedSummary(skipped []skippedFile) string {
	counts := map[repo.SkipReason]int{}
	for _, f := range skipped {
		counts[f.Reason]++
	}
	parts := make([]string, 0, len(counts))
	for _, reason := range []repo.SkipReason{repo.SkipTooLarge, repo.SkipLink, repo.SkipSecret, repo.SkipBinary, repo.SkipUnreadable} {
		if counts[reason] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[reason], reason))
		}
	}
	return strings.Join(parts, " · ")
}

// codeFrame draws the offending line, compiler-style.
func codeFrame(p *ui.Printer, root string, v api.Violation) string {
	gutter := lipgloss.NewStyle().Foreground(p.Theme().Subtle)
	var lines []string
	where := firstNonEmpty(v.FilePath, "(whole model)")
	if v.LineStart > 0 {
		where += ":" + strconv.Itoa(v.LineStart)
	}
	lines = append(lines, gutter.Render("   ╭─ ")+p.Accent(where))
	if source := sourceLine(root, v.FilePath, v.LineStart); source != "" {
		num := fmt.Sprintf("%3d", v.LineStart)
		lines = append(lines, p.Muted(num)+gutter.Render(" │ ")+source)
	}
	tail := gutter.Render("   ╰─ ")
	if v.Suggestion != "" {
		tail += p.Success("→ ") + v.Suggestion
	} else if v.Description != "" {
		tail += p.Muted(truncate(v.Description, 100))
	}
	return strings.Join(append(lines, tail), "\n")
}

// maxSourceBytes bounds the file a code frame reads.
const maxSourceBytes = 8 << 20

func sourceLine(root, path string, line int) string {
	if path == "" || line <= 0 {
		return ""
	}
	// The path comes from the server: only a file of this repository is shown.
	raw, reason := repo.ReadForUpload(root, path, maxSourceBytes)
	if reason != "" {
		return ""
	}
	all := strings.Split(string(raw), "\n")
	if line > len(all) {
		return ""
	}
	return truncate(strings.TrimRight(all[line-1], "\r"), 110)
}

func checkVerdict(p *ui.Printer, run *checkRun) string {
	s := run.Result.Summary
	files := ui.Plural(len(run.Changes), "changed file", "changed files")
	if len(run.Skipped) > 0 {
		files = fmt.Sprintf("%d of %s", len(run.Changes)-len(run.Skipped), files)
	}
	if len(run.Result.Violations) == 0 {
		return p.Check() + " " + p.Bold("No violation") + p.Muted(" in "+files)
	}
	var parts []string
	for _, c := range []struct {
		n     int
		label string
	}{{s.Critical, "critical"}, {s.High, "high"}, {s.Medium, "medium"}, {s.Low, "low"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.label))
		}
	}
	return p.Cross() + " " + p.Bold(ui.Plural(len(run.Result.Violations), "violation", "violations")) +
		p.Muted(" in "+files+" · "+strings.Join(parts, " · "))
}

func checkSummaryMarkdown(run *checkRun, violations []api.Violation, names map[string]string) string {
	var b strings.Builder
	if len(violations) == 0 {
		fmt.Fprintf(&b, "### ✅ Archyl: no architecture violation\n\n%d changed file(s) checked.\n", len(run.Changes))
		return b.String()
	}
	fmt.Fprintf(&b, "### ❌ Archyl: %d architecture violation(s)\n\n", len(violations))
	b.WriteString("| Severity | Rule | File | Finding |\n| --- | --- | --- | --- |\n")
	for _, v := range violations {
		where := "—"
		if v.FilePath != "" {
			where = "`" + codeCell(v.FilePath) + "`"
			if v.LineStart > 0 {
				where = fmt.Sprintf("`%s:%d`", codeCell(v.FilePath), v.LineStart)
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", tableCell(v.Severity), tableCell(firstNonEmpty(names[v.RuleID], "—")), where,
			tableCell(report.Message(v)))
	}
	return b.String()
}
