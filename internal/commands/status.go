package commands

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

type statusReport struct {
	Project  *api.Project      `json:"project"`
	Drift    *api.DriftScore   `json:"drift"`
	Sessions []api.Session     `json:"activeSessions"`
	Branch   string            `json:"branch,omitempty"`
	Changes  int               `json:"changedFiles"`
	Locate   *api.LocateResult `json:"branchImpact,omitempty"`
}

func newStatusCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Summarize the project: drift, your branch, active agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer

			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}

			report := &statusReport{}
			var firstErr error
			err = p.Spin("Gathering status…", func() error {
				var wg sync.WaitGroup
				var mu sync.Mutex
				fail := func(e error) {
					mu.Lock()
					defer mu.Unlock()
					if firstErr == nil {
						firstErr = e
					}
				}
				wg.Go(func() {
					pr, err := client.Project(ctx, project.ID)
					if err != nil {
						fail(err)
						return
					}
					report.Project = pr
				})
				// Drift and sessions are optional: a project without a drift
				// score or with the harness unused still has a status.
				wg.Go(func() { report.Drift, _ = client.LatestDrift(ctx, project.ID) })
				wg.Go(func() { report.Sessions, _ = client.Sessions(ctx, project.ID, true) })
				wg.Go(func() { branchStatus(ctx, app, client, project.ID, report) })
				wg.Wait()
				return firstErr
			})
			if err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(report)
			}
			renderStatus(p, report, app)
			return nil
		},
	}
}

func branchStatus(ctx context.Context, app *App, client *api.Client, projectID string, report *statusReport) {
	git, err := app.Repo(ctx)
	if err != nil {
		return
	}
	report.Branch = git.Branch(ctx)
	changes, err := git.Changes(ctx, git.DefaultBase(ctx))
	if err != nil || len(changes) == 0 {
		return
	}
	report.Changes = len(changes)
	paths := make([]string, 0, len(changes))
	for _, c := range changes {
		paths = append(paths, c.Path)
	}
	if len(paths) > maxPaths {
		paths = paths[:maxPaths]
	}
	report.Locate, _ = client.Locate(ctx, projectID, paths, 1)
}

func renderStatus(p *ui.Printer, r *statusReport, app *App) {
	sess, _ := app.Session()
	var rows [][2]string

	if r.Drift != nil && r.Drift.TotalElements > 0 {
		detail := p.Muted(fmt.Sprintf("  %d drifted · %d missing in code · %s",
			r.Drift.DriftedCount, r.Drift.MissingInCode, ago(r.Drift.CreatedAt)))
		rows = append(rows, [2]string{"alignment", p.Gauge(r.Drift.Score, 24) + detail})
	} else {
		rows = append(rows, [2]string{"alignment", p.Muted("not computed yet")})
	}

	if r.Branch != "" {
		branch := p.Bold(r.Branch)
		switch {
		case r.Changes == 0:
			branch += p.Muted(" · clean")
		case r.Locate != nil && r.Locate.Impact != nil:
			branch += p.Muted(fmt.Sprintf(" · %s · touches %s",
				ui.Plural(r.Changes, "changed file", "changed files"),
				ui.Plural(len(r.Locate.Impact.Elements), "element", "elements")))
		default:
			branch += p.Muted(" · " + ui.Plural(r.Changes, "changed file", "changed files"))
		}
		rows = append(rows, [2]string{"branch", branch})
	}

	if len(r.Sessions) == 0 {
		rows = append(rows, [2]string{"agents", p.Muted("no active session")})
	} else {
		rows = append(rows, [2]string{"agents", p.Bold(ui.Plural(len(r.Sessions), "active session", "active sessions"))})
	}

	title := p.Brand() + p.Muted("  ·  ") + p.Bold(r.Project.Name)
	if sess != nil {
		title += p.Muted("   " + hostLabel(sess.Host))
	}
	body := title + "\n\n" + p.KeyValues(rows)
	for _, s := range r.Sessions {
		body += "\n" + sessionLine(p, s)
	}
	p.Println(p.Panel(body))

	switch {
	case r.Changes > 0:
		p.Println(p.Hint("See what your branch affects:", "archyl impact"))
	case len(r.Sessions) == 0:
		p.Println(p.Hint("Put an agent to work under the harness:", "archyl wrap -- claude"))
	}
}

func sessionLine(p *ui.Printer, s api.Session) string {
	var leased []string
	for i, l := range s.Leases {
		if i == 2 {
			leased = append(leased, p.Muted(fmt.Sprintf("+%d", len(s.Leases)-2)))
			break
		}
		leased = append(leased, p.Element(l.ElementType, l.ElementName))
	}
	gate := p.Success("●")
	switch s.Gate {
	case "warn":
		gate = p.Warning("●")
	case "deny":
		gate = p.Danger("●")
	}
	return fmt.Sprintf("  %s %s  %s  %s  %s", gate, p.Bold(s.AgentName),
		p.Muted("“"+truncate(s.TaskText, 40)+"”"), strings.Join(leased, p.Muted(", ")), p.Muted(ago(s.LastHeartbeatAt)))
}

// ago renders a timestamp as "3m ago".
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
