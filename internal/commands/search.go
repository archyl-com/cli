package commands

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/weblink"
)

func newSearchCmd(app *App) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Search elements, decisions, docs and contracts",
		Long: `Search everything you can see in Archyl: C4 elements, decisions, documentation,
API contracts, event channels, flows and projects.

In a terminal, results update as you type with a preview; enter shows the
pick, ctrl+o opens it in the web app. Piped or with --json, it prints the hits.`,
		Example: `  archyl search
  archyl search invoice
  archyl search payments --json | jq -r '.[].name'`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			client, err := app.Client()
			if err != nil {
				return err
			}
			query := strings.Join(args, " ")
			appURL := app.appURL()

			if !p.Interactive() {
				if len([]rune(query)) < 2 {
					return fmt.Errorf("give a query of at least two characters")
				}
				results, err := client.Search(ctx, query, limit)
				if err != nil {
					return err
				}
				if p.JSON {
					return p.WriteJSON(results)
				}
				for _, r := range results {
					p.Println(tui.ResultLine(p.Theme(), r))
				}
				return nil
			}

			finder := tui.NewFinder(tui.FinderOptions{
				Theme: p.Theme(),
				Query: query,
				Search: func(ctx context.Context, q string) ([]api.SearchResult, error) {
					return client.Search(ctx, q, limit)
				},
				Open: func(r api.SearchResult) error { return auth.OpenBrowser(weblink.SearchResult(appURL, r)) },
			})
			if _, err := tea.NewProgram(finder, tea.WithContext(ctx)).Run(); err != nil {
				return err
			}
			if finder.Chosen == nil {
				return nil
			}
			return showResult(ctx, app, *finder.Chosen)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 30, "maximum results")
	return cmd
}

// showResult prints a search hit with the richest view available for it.
func showResult(ctx context.Context, app *App, r api.SearchResult) error {
	switch r.Type {
	case "system", "container", "component", "code_element":
		return showElement(ctx, app, r.ProjectID, r.ID)
	case "adr":
		client, err := app.Client()
		if err != nil {
			return err
		}
		adrs, err := client.ADRs(ctx, r.ProjectID)
		if err != nil {
			return err
		}
		for _, adr := range adrs {
			if adr.ID == r.ID {
				renderADR(app.Printer, adr)
				return nil
			}
		}
	}
	p := app.Printer
	link := weblink.SearchResult(app.appURL(), r)
	p.Println(tui.ResultLine(p.Theme(), r))
	if r.Description != "" {
		p.Println("  " + p.Muted(truncate(r.Description, 120)))
	}
	p.Println("  " + p.Accent(link))
	return nil
}

func newOpenCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "open [element | ADR-n]",
		Short: "Open the project, an element or a decision in the web app",
		Example: `  archyl open
  archyl open "Invoicing Service"
  archyl open ADR-12`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeElements(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			appURL := app.appURL()
			link := weblink.Project(appURL, ref.ID)
			label := firstNonEmpty(ref.Name, "the project")

			if len(args) == 1 {
				client, err := app.Client()
				if err != nil {
					return err
				}
				if n, ok := adrNumber(args[0]); ok {
					adrs, err := client.ADRs(ctx, ref.ID)
					if err != nil {
						return err
					}
					link = ""
					for _, adr := range adrs {
						if adr.Number == n {
							link, label = weblink.ADR(appURL, ref.ID, adr.ID), fmt.Sprintf("ADR-%d", n)
						}
					}
					if link == "" {
						return fmt.Errorf("no ADR-%d in this project", n)
					}
				} else {
					g, _, err := loadGraph(ctx, app, ref.ID)
					if err != nil {
						return err
					}
					node, _ := g.Find(args[0])
					if node == nil {
						return fmt.Errorf("no element matches %q", args[0])
					}
					link, label = weblink.Element(appURL, ref.ID, node), node.Name
				}
			}

			if p.JSON {
				return p.WriteJSON(map[string]string{"url": link})
			}
			if !p.Interactive() {
				_, _ = fmt.Fprintln(p.Out, link)
				return nil
			}
			if err := auth.OpenBrowser(link); err != nil {
				p.Println(link)
				return nil
			}
			p.Println(p.Check() + " Opened " + p.Bold(label) + p.Muted("  "+link))
			return nil
		},
	}
}
