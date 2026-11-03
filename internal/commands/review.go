package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/prcomment"
	"github.com/archyl-com/cli/internal/report"
	"github.com/archyl-com/cli/internal/ui"
)

type reviewReport struct {
	Branch string            `json:"branch"`
	Check  *checkRun         `json:"check"`
	Impact *api.LocateResult `json:"impact,omitempty"`
	Drift  *api.DriftScore   `json:"drift,omitempty"`
	Ready  bool              `json:"ready"`
	// Diagram is the impact as Mermaid, with --diagram.
	Diagram *prcomment.Diagram `json:"diagram,omitempty"`
}

func newReviewCmd(app *App) *cobra.Command {
	var base, failOnFlag string
	var comment, diagram bool

	cmd := &cobra.Command{
		Use:   "review",
		Short: "Review your branch's architecture before opening a pull request",
		Long: `One pass over your branch: the rule violations it introduces, the elements it
touches and what depends on them, who to notify, and where the model stands.
Exits 1 when a violation reaches --fail-on, so it can gate a push.`,
		Example: `  archyl review
  archyl review --base origin/develop --fail-on critical
  archyl review --diagram    # the impact as a Mermaid flowchart`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer

			failOn, err := report.ParseFailOn(failOnFlag)
			if err != nil {
				return err
			}
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
			base, err = resolveBase(ctx, git, base, env)
			if err != nil {
				return err
			}

			r := &reviewReport{Branch: git.Branch(ctx)}
			err = p.Spin("Reviewing your branch…", func() error {
				var checkErr error
				r.Check, checkErr = runCheck(ctx, app, git, project.ID, base, env)
				if checkErr != nil || len(r.Check.Changes) == 0 {
					return checkErr
				}
				paths := make([]string, 0, len(r.Check.Changes))
				for _, c := range r.Check.Changes {
					paths = append(paths, c.Path)
				}
				if len(paths) > maxPaths {
					paths = paths[:maxPaths]
				}
				// Impact and drift add context; the review stands without them.
				var wg sync.WaitGroup
				wg.Go(func() { r.Impact, _ = client.Locate(ctx, project.ID, paths, 2) })
				wg.Go(func() { r.Drift, _ = client.LatestDrift(ctx, project.ID) })
				wg.Wait()
				return nil
			})
			if err != nil {
				return err
			}
			r.Ready = !failOn.Fails(r.Check.Result.Violations)
			if diagram && r.Impact != nil {
				r.Diagram = prcomment.Mermaid(r.Impact.Impact)
			}

			if p.JSON {
				if err := p.WriteJSON(r); err != nil {
					return err
				}
			} else {
				renderReview(p, r, failOnFlag)
				if r.Diagram != nil {
					p.Println(p.Heading(" Diagram") + p.Muted("  paste into any Mermaid renderer"))
					p.Println()
					_, _ = fmt.Fprint(p.Out, ui.Sanitize("```mermaid\n"+r.Diagram.Source+"```\n\n"))
				}
			}
			if comment {
				target, err := prcomment.Detect(os.Getenv)
				switch {
				case errors.Is(err, prcomment.ErrNoTarget):
					p.Eprintln(p.Muted("Not on a pull or merge request; no comment posted."))
				case err != nil:
					return err
				default:
					if err := target.Upsert(ctx, reviewMarkdown(r, failOnFlag, env.BuildURL)); err != nil {
						return fmt.Errorf("post the review comment: %w", err)
					}
					p.Eprintln(p.Check() + " Review posted on the " + map[string]string{"github": "pull", "gitlab": "merge"}[target.Provider] + fmt.Sprintf(" request #%d", target.Number))
				}
			}
			if !r.Ready {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "ref to compare with (default: origin's default branch)")
	cmd.Flags().StringVar(&failOnFlag, "fail-on", "high", "lowest severity that blocks: critical, high, medium, low or never")
	cmd.Flags().BoolVar(&comment, "comment", false, "post the review on the pull/merge request, editing it on later runs")
	cmd.Flags().BoolVar(&diagram, "diagram", false, "print the impact as a Mermaid flowchart")
	return cmd
}

func renderReview(p *ui.Printer, r *reviewReport, failOn string) {
	header := p.Brand() + p.Muted("  review  ")
	if r.Branch != "" {
		header += p.Bold(r.Branch) + p.Muted(" ")
	}
	if r.Check.Base != "" {
		header += p.Muted("vs " + r.Check.Base + " · ")
	}
	header += p.Muted(ui.Plural(len(r.Check.Changes), "changed file", "changed files"))
	p.Println()
	p.Println(header)

	if len(r.Check.Changes) == 0 {
		p.Println()
		p.Println("  " + p.Muted("Nothing to review."))
		p.Println()
		return
	}

	p.Println()
	p.Println(p.Heading(" Rules"))
	p.Println("  " + checkVerdict(p, r.Check))
	names := r.Check.Result.RuleNames()
	violations := sortedViolations(r.Check.Result.Violations)
	for i, v := range violations {
		if i == 5 {
			p.Println("  " + p.Muted(fmt.Sprintf("+%d more", len(violations)-5)) + "  " + p.Hint("details:", "archyl check"))
			break
		}
		where := v.FilePath
		if v.LineStart > 0 {
			where += fmt.Sprintf(":%d", v.LineStart)
		}
		line := "  " + severityBadge(p, v.Severity) + " " + v.Title
		if where != "" {
			line += "  " + p.Accent(where)
		}
		if name := names[v.RuleID]; name != "" {
			line += "  " + p.Muted(name)
		}
		p.Println(line)
	}

	if r.Impact != nil && r.Impact.Impact != nil && len(r.Impact.Impact.Elements) > 0 {
		dependents := map[string]bool{}
		for _, e := range r.Impact.Impact.Elements {
			for _, d := range e.Dependents {
				dependents[d.ID] = true
			}
		}
		p.Println()
		p.Println(p.Heading(" Impact"))
		p.Println("  " + p.Muted(fmt.Sprintf("touches %s · %s",
			ui.Plural(len(r.Impact.Impact.Elements), "element", "elements"),
			ui.Plural(len(dependents), "dependent", "dependents"))))
		for i, e := range r.Impact.Impact.Elements {
			if i == 5 {
				p.Println("  " + p.Muted(fmt.Sprintf("+%d more", len(r.Impact.Impact.Elements)-5)) + "  " + p.Hint("details:", "archyl impact"))
				break
			}
			line := "  " + p.Element(e.Type, e.Name)
			if len(e.Dependents) > 0 {
				line += p.Muted("  ← ") + elementList(p, e.Dependents, 3)
			}
			p.Println(line)
		}
		if n := r.Impact.Impact.Notify; len(n.Users)+len(n.Teams) > 0 {
			p.Println()
			p.Println(p.Heading(" Notify"))
			p.Println("  " + ownersLine(p, n.Users, n.Teams))
		}
	}

	if r.Drift != nil && r.Drift.TotalElements > 0 {
		p.Println()
		p.Println(p.Heading(" Alignment"))
		p.Println("  " + p.Gauge(r.Drift.Score, 24) + p.Muted("  "+ago(r.Drift.CreatedAt)))
	}

	p.Println()
	if r.Ready {
		p.Println(p.Check() + " " + p.Bold("Ready for review"))
	} else {
		p.Println(p.Cross() + " " + p.Bold("Not ready") + p.Muted(" — violations at or above "+strings.ToLower(failOn)+" block it"))
	}
	p.Println()
}

// reviewMarkdown is the review as a pull request comment.
func reviewMarkdown(r *reviewReport, failOn, buildURL string) string {
	var b strings.Builder
	violations := sortedViolations(r.Check.Result.Violations)
	names := r.Check.Result.RuleNames()
	switch {
	case r.Ready && len(violations) == 0:
		b.WriteString("### ◆ Archyl review: ready ✅\n\n")
	case r.Ready:
		fmt.Fprintf(&b, "### ◆ Archyl review: passes, %d finding(s) to look at ⚠️\n\n", len(violations))
	default:
		fmt.Fprintf(&b, "### ◆ Archyl review: blocked ❌\n\nViolations at or above **%s** block this change.\n\n", failOn)
	}
	fmt.Fprintf(&b, "%d changed file(s)", len(r.Check.Changes))
	if r.Check.Base != "" {
		fmt.Fprintf(&b, " against `%s`", r.Check.Base)
	}
	b.WriteString(".\n\n")

	if len(violations) == 0 {
		b.WriteString("**Architecture rules:** no violation.\n\n")
	} else {
		fmt.Fprintf(&b, "**Architecture rules:** %d violation(s)\n\n| | Rule | Where | Finding |\n|---|---|---|---|\n", len(violations))
		for i, v := range violations {
			if i == 20 {
				fmt.Fprintf(&b, "| | | | …and %d more |\n", len(violations)-20)
				break
			}
			where := "—"
			if v.FilePath != "" {
				where = "`" + codeCell(v.FilePath)
				if v.LineStart > 0 {
					where += fmt.Sprintf(":%d", v.LineStart)
				}
				where += "`"
			}
			icon := map[int]string{4: "🔴", 3: "🟠", 2: "🟡"}[report.Severity(v.Severity)]
			if icon == "" {
				icon = "⚪"
			}
			fmt.Fprintf(&b, "| %s %s | %s | %s | %s |\n", icon, tableCell(v.Severity), tableCell(firstNonEmpty(names[v.RuleID], "—")), where,
				tableCell(report.Message(v)))
		}
		b.WriteString("\n")
	}

	if r.Impact != nil && r.Impact.Impact != nil && len(r.Impact.Impact.Elements) > 0 {
		b.WriteString("**Architecture touched**\n\n")
		for _, e := range r.Impact.Impact.Elements {
			fmt.Fprintf(&b, "- **%s** (%s)", mdText(e.Name), mdText(e.Type))
			if len(e.Dependents) > 0 {
				var deps []string
				for i, d := range e.Dependents {
					if i == 5 {
						deps = append(deps, fmt.Sprintf("+%d", len(e.Dependents)-5))
						break
					}
					deps = append(deps, mdText(d.Name))
				}
				fmt.Fprintf(&b, " — used by %s", strings.Join(deps, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		var notify []string
		for _, t := range r.Impact.Impact.Notify.Teams {
			notify = append(notify, "`@"+codeCell(firstNonEmpty(t.Slug, t.Name))+"`")
		}
		for _, u := range r.Impact.Impact.Notify.Users {
			notify = append(notify, mdText(u.Email))
		}
		if len(notify) > 0 {
			b.WriteString("**Owners to loop in:** " + strings.Join(notify, ", ") + "\n\n")
		}
		if md := prcomment.Mermaid(r.Impact.Impact).Markdown(); md != "" {
			b.WriteString(md + "\n")
		}
	}
	if r.Drift != nil && r.Drift.TotalElements > 0 {
		fmt.Fprintf(&b, "**Model alignment:** %d/100\n\n", r.Drift.Score)
	}
	b.WriteString("<sub>Updated by [archyl](https://github.com/archyl-com/cli) on every push")
	if buildURL != "" {
		fmt.Fprintf(&b, " · [run](%s)", buildURL)
	}
	b.WriteString("</sub>\n")
	return b.String()
}

// tableCell keeps text on one markdown table row: whitespace and newlines
// collapse to single spaces and pipes are escaped.
func tableCell(s string) string {
	return mdText(s)
}

// mdText escapes text from the model or a rule for a Markdown comment, which is
// posted with the CI token: a name must not forge a heading, a link, an image
// or a mention.
func mdText(s string) string {
	return mdEscaper.Replace(strings.Join(strings.Fields(s), " "))
}

var mdEscaper = strings.NewReplacer(
	"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "\\<", ">", "\\>",
	"#", "\\#", "|", "\\|", "@", "\\@", "~", "\\~", "!", "\\!",
)

// codeCell is text inside a code span in a table.
func codeCell(s string) string {
	s = strings.ReplaceAll(strings.Join(strings.Fields(s), " "), "`", "'")
	return strings.ReplaceAll(s, "|", "\\|")
}
