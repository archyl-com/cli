package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func newDriftCmd(app *App) *cobra.Command {
	var compute bool
	var failUnder int
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Show how well the model matches the code",
		Long: `Show the latest alignment score between the documented architecture and the
code (100 = every documented element found in the code). --compute re-analyses
the connected repository first and waits for the result.

Use --fail-under as a quality gate in CI.`,
		Example: `  archyl drift
  archyl drift --compute --fail-under 80`,
		Args: cobra.NoArgs,
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

			var score *api.DriftScore
			if compute {
				err = p.Spin("Re-analysing the repository and comparing it with the model…", func() error {
					score, err = computeDrift(ctx, client, project.ID, timeout)
					return err
				})
			} else {
				err = p.Spin("Loading the alignment score…", func() error {
					score, err = client.LatestDrift(ctx, project.ID)
					return err
				})
			}
			if err != nil {
				return err
			}

			if p.JSON {
				if err := p.WriteJSON(score); err != nil {
					return err
				}
			} else {
				renderDrift(p, score, failUnder)
			}
			if score != nil && failUnder > 0 && score.Score < failUnder {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&compute, "compute", false, "re-analyse the repository before reporting")
	cmd.Flags().IntVar(&failUnder, "fail-under", 0, "exit 1 when the score is below this value (0-100)")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long --compute waits")
	return cmd
}

func computeDrift(ctx context.Context, client *api.Client, projectID string, timeout time.Duration) (*api.DriftScore, error) {
	started, err := client.ComputeDrift(ctx, projectID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		score, err := client.Drift(ctx, started.ID)
		if err != nil {
			return nil, err
		}
		switch score.Status {
		case "completed":
			return score, nil
		case "failed":
			return nil, fmt.Errorf("drift computation failed: %s", firstNonEmpty(score.ErrorMessage, "no detail"))
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("drift computation still running after %s", timeout)
			}
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func renderDrift(p *ui.Printer, s *api.DriftScore, failUnder int) {
	if s == nil || s.TotalElements == 0 {
		p.Println(p.Muted("No alignment score yet."))
		p.Println(p.Hint("Compute one with", "archyl drift --compute"))
		return
	}
	rows := [][2]string{
		{"alignment", p.Gauge(s.Score, 30)},
		{"matched", fmt.Sprintf("%d of %d elements", s.MatchedCount, s.TotalElements)},
	}
	if s.DriftedCount > 0 {
		rows = append(rows, [2]string{"drifted", p.Warning(fmt.Sprint(s.DriftedCount))})
	}
	if s.MissingInCode > 0 {
		rows = append(rows, [2]string{"missing in code", p.Warning(fmt.Sprint(s.MissingInCode))})
	}
	if s.MissingInModel > 0 {
		rows = append(rows, [2]string{"missing in model", p.Warning(fmt.Sprint(s.MissingInModel))})
	}
	rows = append(rows, [2]string{"computed", p.Muted(ago(s.CreatedAt))})
	if failUnder > 0 {
		gate := p.Check() + " above " + fmt.Sprint(failUnder)
		if s.Score < failUnder {
			gate = p.Cross() + " below " + fmt.Sprint(failUnder)
		}
		rows = append(rows, [2]string{"gate", gate})
	}
	p.Println(p.Panel(p.Brand() + p.Muted("  drift") + "\n\n" + p.KeyValues(rows)))
}
