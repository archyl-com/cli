package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

// discoverPoll is how often a waited discovery asks for progress.
var discoverPoll = 2 * time.Second

func newDiscoverCmd(app *App) *cobra.Command {
	var repos []string
	var branch string
	var noWait, wait, noReview bool
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Discover the architecture from the project's repositories with AI",
		Long: `Start an AI discovery on the repositories linked to the project and follow it.
What it finds is not added straight away: it waits for review, with
archyl discover review, which opens on its own after a discovery in a terminal.

Each repository counts as one AI discovery against the plan's quota.`,
		Example: `  archyl discover
  archyl discover --repo acme/payments --branch develop
  archyl discover --no-wait --json
  archyl discover review`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if wait && noWait {
				return errors.New("--wait and --no-wait cannot be used together")
			}
			return discover(cmd.Context(), app, repos, branch, !noWait, !noReview)
		},
	}
	cmd.Flags().StringSliceVar(&repos, "repo", nil, "repository to discover, as owner/name (default: every linked repository)")
	cmd.Flags().StringVar(&branch, "branch", "", "branch to read (default: the repository's default branch)")
	cmd.Flags().BoolVar(&wait, "wait", false, "follow the discovery until it ends (the default)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "start the discovery and return its job")
	cmd.Flags().BoolVar(&noReview, "no-review", false, "do not open the review when the discovery ends")
	_ = cmd.RegisterFlagCompletionFunc("repo", completeRepositories(app))
	cmd.AddCommand(newDiscoverReviewCmd(app))
	return cmd
}

// discoverResult is the --json output of archyl discover.
type discoverResult struct {
	Jobs    []*api.DiscoveryJob `json:"jobs"`
	Pending *int                `json:"pending,omitempty"`
}

func discover(ctx context.Context, app *App, repos []string, branch string, wait, review bool) error {
	p := app.Printer
	ref, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	project, err := client.Project(ctx, ref.ID)
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		for _, r := range project.Repositories {
			repos = append(repos, r.FullName)
		}
	}
	if len(repos) == 0 {
		// The server picks the only repository, or explains why it cannot.
		repos = []string{""}
	}

	var jobs []*api.DiscoveryJob
	for _, repo := range repos {
		job, err := client.StartDiscovery(ctx, project.ID, repo, branch)
		if err != nil {
			if len(jobs) == 0 {
				return fmt.Errorf("start the discovery%s: %w", forRepo(repo), err)
			}
			p.Eprintln(p.Cross() + " " + p.Muted("Could not start"+forRepo(repo)+": ") + err.Error())
			continue
		}
		jobs = append(jobs, job)
		if !p.JSON {
			p.Eprintln(p.Accent("◆") + " Discovering " + p.Bold(firstNonEmpty(job.Repository, repo, project.Name)) + p.Muted(" · "+shortID(job.ID)))
		}
	}

	if !wait {
		if p.JSON {
			return p.WriteJSON(discoverResult{Jobs: jobs})
		}
		for _, j := range jobs {
			p.Println(j.ID)
		}
		p.Eprintln(p.Hint("Review what it finds with", "archyl discover review"))
		return nil
	}

	if err := followDiscoveries(ctx, p, client, jobs); err != nil {
		return err
	}

	pending, err := client.PendingDiscoveries(ctx, project.ID)
	if err != nil {
		return err
	}
	failed := 0
	for _, j := range jobs {
		if j.Status == "failed" {
			failed++
		}
	}

	if p.JSON {
		n := len(pending)
		if err := p.WriteJSON(discoverResult{Jobs: jobs, Pending: &n}); err != nil {
			return err
		}
	} else {
		for _, j := range jobs {
			name := p.Bold(firstNonEmpty(j.Repository, project.Name))
			if j.Status == "failed" {
				p.Println(p.Cross() + " " + name + p.Muted(" failed: ") + firstNonEmpty(j.Error, "no reason given"))
			} else {
				p.Println(p.Check() + " " + name + p.Muted(" discovered"))
			}
		}
		switch {
		case len(pending) == 0:
			p.Println(p.Muted("  Nothing new to review: the model already has what it found."))
		case review && p.Interactive():
			if err := runTriage(ctx, app, client, project.Name, project.ID, pending); err != nil {
				return err
			}
		default:
			p.Println("  " + p.Bold(ui.Plural(len(pending), "element", "elements")) + p.Muted(" await review"))
			p.Println("  " + p.Hint("Review them with", "archyl discover review"))
		}
	}
	if failed > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

func forRepo(repo string) string {
	if repo == "" {
		return ""
	}
	return " on " + repo
}

// followDiscoveries polls the jobs until they end, drawing one progress line
// on stderr, or logging each new step when stderr is not a terminal.
func followDiscoveries(ctx context.Context, p *ui.Printer, client *api.Client, jobs []*api.DiscoveryJob) error {
	live := !p.JSON && term.IsTerminal(os.Stderr.Fd())
	spinner := lipgloss.NewStyle().Foreground(p.Theme().Accent)
	lastStep := map[string]string{}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	nextPoll := time.Now()

	for frame := 0; ; frame++ {
		if time.Now().After(nextPoll) {
			nextPoll = time.Now().Add(discoverPoll)
			for i, j := range jobs {
				if j.Done() {
					continue
				}
				status, err := client.DiscoveryStatus(ctx, j.ID)
				if err != nil {
					if live {
						_, _ = fmt.Fprint(p.Err, "\r\033[2K")
					}
					return fmt.Errorf("follow the discovery %s: %w", shortID(j.ID), err)
				}
				status.Repository, status.Branch = j.Repository, j.Branch
				jobs[i] = status
				if !live && !p.JSON && status.CurrentStep != lastStep[j.ID] {
					lastStep[j.ID] = status.CurrentStep
					p.Eprintln(p.Muted(fmt.Sprintf("  %3d%%  %s", status.Progress, discoveryLabel(status))))
				}
			}
		}

		done := true
		for _, j := range jobs {
			done = done && j.Done()
		}
		if done {
			if live {
				_, _ = fmt.Fprint(p.Err, "\r\033[2K")
			}
			return nil
		}
		if live {
			_, _ = lipgloss.Fprint(p.Err, "\r\033[2K"+spinner.Render(spinnerFrame(frame))+" "+discoveryProgress(p, jobs))
		}

		select {
		case <-ctx.Done():
			if live {
				_, _ = fmt.Fprint(p.Err, "\r\033[2K")
			}
			p.Eprintln(p.Muted("Stopped following; the discovery goes on. Review later with ") + p.Code("archyl discover review"))
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func spinnerFrame(i int) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	return frames[i%len(frames)]
}

// discoveryProgress is the live line: the average progress, then the step
// of the first job still running.
func discoveryProgress(p *ui.Printer, jobs []*api.DiscoveryJob) string {
	total, running := 0, ""
	for _, j := range jobs {
		total += j.Progress
		if running == "" && !j.Done() {
			running = discoveryLabel(j)
		}
	}
	pct := total / max(len(jobs), 1)
	width := 20
	bar := p.Accent(strings.Repeat("━", pct*width/100)) + p.Muted(strings.Repeat("━", width-pct*width/100))
	return bar + " " + p.Bold(fmt.Sprintf("%3d%%", pct)) + "  " + p.Muted(cutText(running, p.Width()-34))
}

func discoveryLabel(j *api.DiscoveryJob) string {
	step := firstNonEmpty(j.CurrentStep, j.Status)
	if j.Repository != "" {
		return j.Repository + " · " + step
	}
	return step
}

func cutText(s string, n int) string {
	if n <= 1 {
		return ""
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// completeRepositories completes --repo from the project's linked
// repositories.
func completeRepositories(app *App) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		ref, err := app.Project(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		client, err := app.Client()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		project, err := client.Project(cmd.Context(), ref.ID)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, r := range project.Repositories {
			out = append(out, r.FullName)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func newDiscoverReviewCmd(app *App) *cobra.Command {
	var approveAll, rejectAll bool
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Approve or reject what discoveries found",
		Long: `Review the elements discoveries propose before they join the model, grouped by
C4 level, with the selected element's details on the right.

  ↑↓ / j k   move              a   approve the selection, or this one
  space      select            r   reject the selection, or this one
  /          filter (fuzzy)    A   approve everything listed
  q          quit

Outside a terminal it lists them; --approve-all or --reject-all settles them
all at once.`,
		Example: `  archyl discover review
  archyl discover review --json
  archyl discover review --approve-all`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if approveAll && rejectAll {
				return errors.New("--approve-all and --reject-all cannot be used together")
			}
			return reviewDiscoveries(cmd.Context(), app, approveAll, rejectAll)
		},
	}
	cmd.Flags().BoolVar(&approveAll, "approve-all", false, "add every pending element to the model")
	cmd.Flags().BoolVar(&rejectAll, "reject-all", false, "discard every pending element")
	return cmd
}

// pendingOut is a pending element in --json output.
type pendingOut struct {
	ID          string `json:"id"`
	Level       string `json:"level"`
	Name        string `json:"name"`
	Parent      string `json:"parent,omitempty"`
	Type        string `json:"type,omitempty"`
	Technology  string `json:"technology,omitempty"`
	Description string `json:"description,omitempty"`
	File        string `json:"file,omitempty"`
	JobID       string `json:"jobId,omitempty"`
}

func reviewDiscoveries(ctx context.Context, app *App, approveAll, rejectAll bool) error {
	p := app.Printer
	ref, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	var project *api.Project
	var pending []api.PendingDiscovery
	if err := p.Spin("Loading pending discoveries…", func() error {
		if project, err = client.Project(ctx, ref.ID); err != nil {
			return err
		}
		pending, err = client.PendingDiscoveries(ctx, ref.ID)
		return err
	}); err != nil {
		return err
	}

	items := tui.TriageItems(pending)
	if approveAll || rejectAll {
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		verb := map[bool]string{true: "approved", false: "rejected"}[approveAll]
		if len(ids) > 0 {
			call := client.RejectDiscoveries
			if approveAll {
				call = client.ApproveDiscoveries
			}
			if err := p.Spin("Settling "+ui.Plural(len(ids), "element", "elements")+"…", func() error {
				return call(ctx, project.ID, ids)
			}); err != nil {
				return err
			}
			if approveAll {
				app.InvalidateModel(project.ID)
			}
		}
		if p.JSON {
			return p.WriteJSON(map[string]int{verb: len(ids)})
		}
		if len(ids) == 0 {
			p.Println(p.Muted("Nothing awaits review."))
			return nil
		}
		p.Println(p.Check() + " " + capitalize(verb) + " " + p.Bold(ui.Plural(len(ids), "element", "elements")))
		return nil
	}

	if p.JSON {
		out := make([]pendingOut, 0, len(items))
		for _, it := range items {
			out = append(out, pendingOut{
				ID: it.ID, Level: it.ElementType, Name: it.Name(), Parent: it.Parent(), Type: it.Element.Type,
				Technology: it.Element.Technology, Description: it.Element.Description, File: it.Element.FilePath,
				JobID: it.DiscoveryJobID,
			})
		}
		return p.WriteJSON(out)
	}
	if len(items) == 0 {
		p.Println(p.Muted("Nothing awaits review."))
		p.Println(p.Hint("Find more with", "archyl discover"))
		return nil
	}
	if !p.Interactive() {
		printPending(p, items)
		return nil
	}
	return runTriage(ctx, app, client, project.Name, project.ID, pending)
}

// printPending lists pending elements by group, for pipes and the shell.
func printPending(p *ui.Printer, items []tui.TriageItem) {
	group := ""
	for _, it := range items {
		if it.ElementType != group {
			if group != "" {
				p.Println()
			}
			group = it.ElementType
			p.Println(p.Heading(tui.TriageGroupTitle(group)))
		}
		line := "  " + p.Element(it.ElementType, it.Name())
		if parent := it.Parent(); parent != "" {
			line += p.Muted("  in " + parent)
		}
		if d := strings.Join(strings.Fields(it.Element.Description), " "); d != "" {
			line += p.Muted("  " + cutText(d, 80))
		}
		p.Println(line)
	}
	p.Println()
	p.Println("  " + p.Bold(ui.Plural(len(items), "element", "elements")) + p.Muted(" await review"))
	p.Println("  " + p.Hint("Settle them all with", "archyl discover review --approve-all"))
}

func runTriage(ctx context.Context, app *App, client *api.Client, projectName, projectID string, pending []api.PendingDiscovery) error {
	p := app.Printer
	triage := tui.NewTriage(tui.TriageOptions{
		Theme:   p.Theme(),
		Project: projectName,
		Pending: pending,
		Approve: func(ctx context.Context, ids []string) error { return client.ApproveDiscoveries(ctx, projectID, ids) },
		Reject:  func(ctx context.Context, ids []string) error { return client.RejectDiscoveries(ctx, projectID, ids) },
	})
	_, err := tea.NewProgram(triage, tea.WithContext(ctx)).Run()
	// Approvals already reached the model even when the view ends in error.
	if triage.Approved > 0 {
		app.InvalidateModel(projectID)
	}
	if err != nil {
		return err
	}
	if triage.Approved+triage.Rejected == 0 {
		p.Println(p.Muted(fmt.Sprintf("%d still pending.", triage.Remaining())))
		return nil
	}
	parts := []string{}
	if triage.Approved > 0 {
		parts = append(parts, p.Check()+" Approved "+p.Bold(ui.Plural(triage.Approved, "element", "elements")))
	}
	if triage.Rejected > 0 {
		parts = append(parts, p.Cross()+" Rejected "+p.Bold(ui.Plural(triage.Rejected, "element", "elements")))
	}
	if n := triage.Remaining(); n > 0 {
		parts = append(parts, p.Muted(fmt.Sprintf("%d still pending", n)))
	}
	p.Println(strings.Join(parts, p.Muted("  ·  ")))
	return nil
}
