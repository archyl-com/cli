package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/weblink"
)

func newCRCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cr",
		Aliases: []string{"change-request", "requests"},
		Short:   "Review Architecture Change Requests",
		Long: `List, read and review Architecture Change Requests — the proposals, often
opened by agents at the end of a work session, for how the model should catch
up with the code.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listCRs(cmd.Context(), app, "")
		},
	}

	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "List change requests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listCRs(cmd.Context(), app, status)
		},
	}
	list.Flags().StringVar(&status, "status", "", "draft, open, approved, merged or closed")

	show := &cobra.Command{
		Use:   "show <number>",
		Short: "Show a change request and its changes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cr, err := findCR(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			if app.Printer.JSON {
				return app.Printer.WriteJSON(cr)
			}
			renderCR(app, cr)
			return nil
		},
	}

	open := &cobra.Command{
		Use:   "open <number>",
		Short: "Open a change request in the web app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cr, err := findCR(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			ref, _ := app.Project(cmd.Context())
			link := weblink.ChangeRequest(app.appURL(), ref.ID, cr.ID)
			if err := auth.OpenBrowser(link); err != nil {
				app.Printer.Println(link)
			}
			return nil
		},
	}

	cmd.AddCommand(list, show, open,
		reviewCmd(app, "approve", "approve", "Approve a change request"),
		reviewCmd(app, "request-changes", "request_changes", "Ask for changes on a change request"),
		reviewCmd(app, "comment", "comment", "Comment on a change request"),
	)
	return cmd
}

func reviewCmd(app *App, use, decision, short string) *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:   use + " <number>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if decision != "approve" && strings.TrimSpace(message) == "" {
				return fmt.Errorf("say what to change or comment with -m")
			}
			cr, err := findCR(cmd.Context(), app, args[0])
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			if err := client.ReviewChangeRequest(cmd.Context(), cr.ID, decision, message); err != nil {
				return err
			}
			verbs := map[string]string{"approve": "Approved", "request_changes": "Requested changes on", "comment": "Commented on"}
			app.Printer.Println(app.Printer.Check() + " " + verbs[decision] + " " + app.Printer.Accent(fmt.Sprintf("#%d", cr.Number)) + " " + app.Printer.Bold(cr.Title))
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "review comment")
	return cmd
}

func listCRs(ctx context.Context, app *App, status string) error {
	p := app.Printer
	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	var crs []api.ChangeRequest
	if err := p.Spin("Loading change requests…", func() error {
		crs, err = client.ChangeRequests(ctx, project.ID, status)
		return err
	}); err != nil {
		return err
	}
	if p.JSON {
		return p.WriteJSON(crs)
	}
	if len(crs) == 0 {
		p.Println(p.Muted("No change request."))
		return nil
	}
	for _, cr := range crs {
		meta := []string{ui.Plural(cr.ChangeCount, "change", "changes")}
		if cr.ApprovalCount > 0 {
			meta = append(meta, ui.Plural(cr.ApprovalCount, "approval", "approvals"))
		}
		if name := cr.Author.Name(); name != "" {
			meta = append(meta, name)
		}
		meta = append(meta, ago(cr.UpdatedAt))
		p.Println(fmt.Sprintf("  %s %s  %s  %s", crStatus(p, cr.Status), p.Accent(fmt.Sprintf("#%-3d", cr.Number)), cr.Title, p.Muted(strings.Join(meta, " · "))))
	}
	p.Println()
	p.Println(p.Hint("Read one:", "archyl cr show <number>"))
	return nil
}

func crStatus(p *ui.Printer, status string) string {
	switch status {
	case "open":
		return p.Success("● open    ")
	case "approved":
		return p.Accent("● approved")
	case "merged":
		return p.Muted("✓ merged  ")
	case "closed":
		return p.Muted("✗ closed  ")
	}
	return p.Muted("○ draft   ")
}

func findCR(ctx context.Context, app *App, ref string) (*api.ChangeRequest, error) {
	number, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return nil, fmt.Errorf("give the change request number, like 12")
	}
	project, err := app.Project(ctx)
	if err != nil {
		return nil, err
	}
	client, err := app.Client()
	if err != nil {
		return nil, err
	}
	crs, err := client.ChangeRequests(ctx, project.ID, "")
	if err != nil {
		return nil, err
	}
	for _, cr := range crs {
		if cr.Number == number {
			return client.ChangeRequest(ctx, cr.ID)
		}
	}
	return nil, fmt.Errorf("no change request #%d in this project", number)
}

func renderCR(app *App, cr *api.ChangeRequest) {
	p := app.Printer
	p.Println()
	p.Println("  " + crStatus(p, cr.Status) + " " + p.Accent(fmt.Sprintf("#%d", cr.Number)) + " " + p.Bold(cr.Title))
	meta := []string{}
	if name := cr.Author.Name(); name != "" {
		meta = append(meta, "by "+name)
	}
	meta = append(meta, ago(cr.CreatedAt), ui.Plural(cr.ApprovalCount, "approval", "approvals"))
	p.Println("  " + p.Muted(strings.Join(meta, " · ")))
	if cr.Description != "" {
		p.Println()
		p.Println(indent(wrapText(cr.Description, 88), 2))
	}
	if len(cr.Changes) > 0 {
		p.Println()
		p.Println(p.Heading(fmt.Sprintf(" Changes  %s", p.Muted(fmt.Sprint(len(cr.Changes))))))
		for _, c := range cr.Changes {
			sign := map[string]string{"create": p.Success("+"), "update": p.Warning("~"), "delete": p.Danger("−")}[c.Operation]
			if sign == "" {
				sign = p.Muted("•")
			}
			level := strings.TrimSuffix(c.ElementType, "_element")
			p.Println("  " + sign + " " + p.Element(level, changeName(c.ElementData)) + "  " + p.Muted(c.Operation+" "+strings.ReplaceAll(c.ElementType, "_", " ")))
		}
	}
	p.Println()
	p.Println("  " + p.Hint("Review:", fmt.Sprintf("archyl cr approve %d", cr.Number)) + p.Muted("  or  ") + p.Code(fmt.Sprintf("archyl cr request-changes %d -m …", cr.Number)))
	p.Println()
}

// changeName pulls a readable name out of a change's element data.
func changeName(data any) string {
	if m, ok := data.(map[string]any); ok {
		for _, key := range []string{"name", "title", "label"} {
			if s, ok := m[key].(string); ok && s != "" {
				return s
			}
		}
	}
	if s, ok := data.(string); ok && s != "" {
		return truncate(s, 40)
	}
	return "(unnamed)"
}
