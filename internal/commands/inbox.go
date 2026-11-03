package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/weblink"
)

func newInboxCmd(app *App) *cobra.Command {
	var markRead, watch bool
	var limit int

	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Your notifications — and act on them: review, answer, open",
		Long: `Mentions, reviews, agent runs and tasks. In a terminal, pick a notification to
act on it right there: approve or request changes on a change request, answer
an agent that waits for you, or open the rest in the web app.

--watch stays open and raises a desktop notification for each new one.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			client, err := app.Client()
			if err != nil {
				return err
			}
			if watch {
				return watchInbox(ctx, app, client)
			}

			var items []api.Notification
			var unread int
			if err := p.Spin("Loading your inbox…", func() error {
				items, unread, err = client.Inbox(ctx, limit)
				return err
			}); err != nil {
				return err
			}
			if markRead && unread > 0 {
				if err := client.MarkAllRead(ctx); err != nil {
					return err
				}
			}
			if p.JSON {
				return p.WriteJSON(map[string]any{"unread": unread, "notifications": items})
			}
			if len(items) == 0 {
				p.Println(p.Check() + " " + p.Muted("Inbox zero."))
				return nil
			}
			if p.Interactive() && !markRead {
				return actOnInbox(ctx, app, client, items, unread)
			}
			printInbox(p, items, unread)
			if markRead {
				p.Println(p.Check() + p.Muted(" Marked as read."))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&markRead, "read", false, "mark everything as read")
	cmd.Flags().BoolVar(&watch, "watch", false, "stay open; desktop notification for each new one")
	cmd.Flags().IntVar(&limit, "limit", 20, "how many notifications")
	return cmd
}

func printInbox(p *ui.Printer, items []api.Notification, unread int) {
	p.Println()
	p.Println("  " + p.Brand() + p.Muted("  inbox  ") + p.Bold(ui.Plural(unread, "unread", "unread")))
	p.Println()
	for _, n := range items {
		p.Println("  " + inboxLine(p, n))
		if n.Payload.Detail != "" {
			p.Println("      " + p.Muted("“"+truncate(n.Payload.Detail, 90)+"”"))
		}
	}
	p.Println()
}

func inboxLine(p *ui.Printer, n api.Notification) string {
	marker := p.Muted("  ")
	if n.ReadAt == nil {
		marker = p.Accent("● ")
	}
	line := marker + notificationText(p, n)
	if n.Payload.ProjectName != "" {
		line += p.Muted("  · " + n.Payload.ProjectName)
	}
	return line + p.Muted("  "+ago(n.CreatedAt))
}

// actOnInbox lets the user pick notifications one after another and act.
func actOnInbox(ctx context.Context, app *App, client *api.Client, items []api.Notification, unread int) error {
	p := app.Printer
	for {
		options := []huh.Option[int]{huh.NewOption(p.Muted("Done"), -1)}
		for i, n := range items {
			options = append(options, huh.NewOption(inboxLine(p, n), i))
		}
		choice := -1
		keys := huh.NewDefaultKeyMap()
		keys.Quit = key.NewBinding(key.WithKeys("ctrl+c", "q"))
		err := huh.NewForm(huh.NewGroup(huh.NewSelect[int]().
			Title(fmt.Sprintf("Inbox · %s", ui.Plural(unread, "unread", "unread"))).
			Description("Pick one to act on it. q leaves.").
			Options(options...).
			Height(min(len(options)+2, 16)).
			Value(&choice))).WithKeyMap(keys).Run()
		if err != nil || choice < 0 {
			return nil
		}
		n := &items[choice]
		if err := actOnNotification(ctx, app, client, *n); err != nil {
			p.Eprintln(p.Cross() + " " + err.Error())
		}
		if n.ReadAt == nil {
			if client.MarkRead(ctx, n.ID) == nil {
				now := time.Now()
				n.ReadAt = &now
				unread = max(unread-1, 0)
			}
		}
	}
}

func actOnNotification(ctx context.Context, app *App, client *api.Client, n api.Notification) error {
	p := app.Printer
	switch n.ResourceType {
	case "agent_run":
		return followRun(ctx, app, n.ResourceID)
	case "request":
		cr, err := client.ChangeRequest(ctx, n.ResourceID)
		if err != nil {
			return err
		}
		renderCR(app, cr)
		if cr.Status != "open" && cr.Status != "draft" {
			return nil
		}
		action := ""
		if err := huh.NewSelect[string]().Title("Review it?").Options(
			huh.NewOption("Approve", "approve"),
			huh.NewOption("Request changes", "request_changes"),
			huh.NewOption("Comment", "comment"),
			huh.NewOption("Open in the web app", "open"),
			huh.NewOption("Not now", ""),
		).Value(&action).Run(); err != nil || action == "" {
			return nil
		}
		if action == "open" {
			return auth.OpenBrowser(weblink.ChangeRequest(app.appURL(), cr.ProjectID(n), cr.ID))
		}
		message := ""
		if action != "approve" {
			if err := huh.NewText().Title("Your review").Value(&message).Run(); err != nil || strings.TrimSpace(message) == "" {
				return nil
			}
		}
		if err := client.ReviewChangeRequest(ctx, cr.ID, action, message); err != nil {
			return err
		}
		p.Println(p.Check() + " Review sent on " + p.Accent(fmt.Sprintf("#%d", cr.Number)))
		return nil
	}
	link := weblink.Project(app.appURL(), n.ProjectID)
	switch n.ResourceType {
	case "adr":
		link = weblink.ADR(app.appURL(), n.ProjectID, n.ResourceID)
	case "element":
		link += "?select=" + n.ResourceID
	}
	return auth.OpenBrowser(link)
}

// watchInbox polls for new notifications and raises each as a desktop one.
func watchInbox(ctx context.Context, app *App, client *api.Client) error {
	p := app.Printer
	seen := map[string]bool{}
	first := true
	p.Eprintln(p.Muted("Watching your inbox · ctrl-c to stop"))
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		items, _, err := client.Inbox(ctx, 20)
		if err == nil {
			for i := len(items) - 1; i >= 0; i-- {
				n := items[i]
				if seen[n.ID] {
					continue
				}
				seen[n.ID] = true
				if first && n.ReadAt != nil {
					continue
				}
				p.Println(inboxLine(p, n))
				if !first {
					desktopNotify("Archyl", plainNotification(n))
				}
			}
			first = false
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func plainNotification(n api.Notification) string {
	who := firstNonEmpty(n.Payload.ActorName, "Someone")
	switch {
	case strings.HasPrefix(n.Kind, "agent_run.waiting"):
		return "An agent needs your input: " + n.Payload.Subject
	case strings.HasPrefix(n.Kind, "request."):
		return fmt.Sprintf("%s · change request #%d %s", who, n.Payload.Number, n.Payload.Subject)
	}
	return who + " · " + n.Payload.Subject
}

// notificationText phrases a notification the way the web inbox does.
func notificationText(p *ui.Printer, n api.Notification) string {
	who := p.Bold(firstNonEmpty(n.Payload.ActorName, "Someone"))
	subject := p.Bold(n.Payload.Subject)
	if n.Payload.Number > 0 {
		subject = p.Accent(fmt.Sprintf("#%d", n.Payload.Number)) + " " + subject
	}
	verbs := map[string]string{
		"request.opened":            "opened",
		"request.approved":          "approved",
		"request.changes_requested": "requested changes on",
		"request.commented":         "commented on",
		"request.merged":            "merged",
		"request.closed":            "closed",
		"request.mentioned":         "mentioned you in",
		"comment.replied":           "replied about",
		"comment.mentioned":         "mentioned you about",
		"adr.mentioned":             "mentioned you in",
		"documentation.mentioned":   "mentioned you in",
		"task.assigned":             "assigned you",
		"followed.changed":          "changed",
		"followed.created":          "created",
		"followed.deleted":          "deleted",
		"followed.commented":        "commented on",
		"followed.requested":        "proposed a change to",
	}
	switch {
	case strings.HasPrefix(n.Kind, "agent_run."):
		state := map[string]string{
			"agent_run.waiting":   p.Warning("needs your input"),
			"agent_run.succeeded": p.Success("finished"),
			"agent_run.failed":    p.Danger("failed"),
		}[n.Kind]
		return "Agent run " + subject + " " + state
	case verbs[n.Kind] != "":
		return who + " " + verbs[n.Kind] + " " + subject
	}
	return strings.ReplaceAll(n.Kind, ".", " ") + " " + subject
}
