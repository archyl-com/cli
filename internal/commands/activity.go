package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/activity"
	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func newActivityCmd(app *App) *cobra.Command {
	var follow, notify bool
	var limit int
	var element string
	var every time.Duration

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "What is happening on the project: changes, reviews, agents",
		Long: `One feed of what happens on the project: elements created and changed, change
requests opened and merged, agent runs started, waiting on someone or
finished, work sessions opened and closed.

--follow keeps it open and prints events as they happen, like tail -f;
--notify also raises a desktop notification when an agent waits for you.`,
		Example: `  archyl activity
  archyl activity --follow --notify
  archyl activity --element "Payments"
  archyl activity --json --limit 100 | jq 'select(.kind == "run")'`,
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

			elementID := ""
			if element != "" {
				g, _, err := loadGraph(ctx, app, project.ID)
				if err != nil {
					return err
				}
				n, _ := g.Find(element)
				if n == nil {
					return fmt.Errorf("no element matches %q", element)
				}
				elementID = n.ID
				if !p.JSON {
					p.Println(p.Muted("Activity on ") + p.Element(n.Level, n.Name))
				}
			}

			feed := activity.NewFeed()
			fetch := func() ([]activity.Event, error) { return fetchActivity(ctx, client, project.ID, elementID, limit) }

			var events []activity.Event
			if err := p.Spin("Loading activity…", func() error {
				events, err = fetch()
				return err
			}); err != nil {
				return err
			}
			first := feed.Fresh(events)
			if len(first) > limit {
				first = first[len(first)-limit:]
			}
			for _, e := range first {
				printEvent(p, e)
			}
			if len(first) == 0 && !p.JSON {
				p.Println(p.Muted("Nothing has happened yet."))
			}
			if !follow {
				return nil
			}

			if !p.JSON {
				p.Eprintln(p.Muted("── following · ctrl-c to stop ──"))
			}
			ticker := time.NewTicker(every)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
				events, err := fetch()
				if err != nil {
					// A blip in the connection must not end a long follow.
					continue
				}
				for _, e := range feed.Fresh(events) {
					printEvent(p, e)
					if notify && e.NeedsUser {
						desktopNotify("Archyl: an agent needs you", e.Object)
					}
				}
			}
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing events as they happen")
	cmd.Flags().BoolVar(&notify, "notify", false, "with --follow, a desktop notification when an agent waits for you")
	cmd.Flags().IntVar(&limit, "limit", 30, "how many past events to show")
	cmd.Flags().StringVar(&element, "element", "", "only changes to this element")
	cmd.Flags().DurationVar(&every, "every", 5*time.Second, "how often --follow checks")
	_ = cmd.RegisterFlagCompletionFunc("element", completeElements(app))
	return cmd
}

func fetchActivity(ctx context.Context, client *api.Client, projectID, elementID string, limit int) ([]activity.Event, error) {
	history, err := client.History(ctx, projectID, elementID, min(limit, 50))
	if err != nil {
		return nil, err
	}
	events := activity.FromHistory(history)
	if elementID != "" {
		return events, nil
	}
	if runs, err := client.Runs(ctx, projectID, "", 20); err == nil {
		events = append(events, activity.FromRuns(runs)...)
	}
	if sessions, err := client.Sessions(ctx, projectID, false); err == nil {
		events = append(events, activity.FromSessions(sessions)...)
	}
	return events, nil
}

func printEvent(p *ui.Printer, e activity.Event) {
	if p.JSON {
		// One object per line, so --follow can be piped into jq as it runs.
		if line, err := json.Marshal(e); err == nil {
			_, _ = fmt.Fprintln(p.Out, string(line))
		}
		return
	}
	t := p.Theme()
	when := e.At.Local().Format("15:04")
	if time.Since(e.At) > 20*time.Hour {
		when = e.At.Local().Format("Jan 2")
	}
	marker := p.Muted("•")
	object := p.Bold(e.Object)
	switch e.Kind {
	case activity.KindModel:
		if e.Level != "" {
			object = p.Element(e.Level, e.Object)
		}
	case activity.KindRequest:
		marker = p.Accent("⇄")
	case activity.KindRun:
		marker = lipgloss.NewStyle().Foreground(t.Container).Render("◆")
	case activity.KindSession:
		marker = lipgloss.NewStyle().Foreground(t.Component).Render("●")
	}
	line := fmt.Sprintf("%s  %s %s %s %s", p.Muted(fmt.Sprintf("%6s", when)), marker, p.Bold(e.Actor), p.Muted(e.Verb), object)
	if e.Detail != "" {
		line += "  " + p.Muted(e.Detail)
	}
	if e.NeedsUser {
		line += "  " + p.Warning("→ archyl runs view "+shortRef(e.Ref))
	}
	p.Println(line)
}

func shortRef(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// desktopNotify raises a system notification, silently doing nothing where
// none is available.
func desktopNotify(title, body string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("osascript", "-e", fmt.Sprintf("display notification %q with title %q", body, title))
	case "linux":
		cmd = exec.Command("notify-send", title, body)
	default:
		return
	}
	_ = cmd.Start()
}
