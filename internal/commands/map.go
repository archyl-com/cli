package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/auth"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/weblink"
)

// loadGraph returns the project's model indexed, from the cache when the API
// is slow (see loadModel).
func loadGraph(ctx context.Context, app *App, projectID string) (*graph.Graph, *api.Project, error) {
	snap, err := loadModel(ctx, app, projectID)
	if err != nil {
		return nil, nil, err
	}
	return graph.New(&snap.Model), &snap.Project, nil
}

// openGraph is loadGraph for the full-screen explorer: a cached model opens
// at once, and refresh then fetches the current one for the open view.
func openGraph(ctx context.Context, app *App, projectID string) (*graph.Graph, *api.Project, func(func(*graph.Graph)), error) {
	entry := app.cachedModel(projectID)
	if entry == nil {
		g, project, err := loadGraph(ctx, app, projectID)
		return g, project, func(func(*graph.Graph)) {}, err
	}
	client, err := app.Client()
	if err != nil {
		return nil, nil, nil, err
	}
	cached, _ := json.Marshal(entry.Value.Model)
	refresh := func(updated func(*graph.Graph)) {
		go func() {
			ctx, cancel := context.WithTimeout(ctx, modelFetchTimeout)
			defer cancel()
			snap, err := fetchSnapshot(ctx, client, projectID)
			if err != nil {
				if !transient(err) && ctx.Err() == nil {
					app.InvalidateModel(projectID)
				}
				return
			}
			app.saveModel(projectID, snap)
			if fresh, _ := json.Marshal(snap.Model); !bytes.Equal(fresh, cached) {
				updated(graph.New(&snap.Model))
			}
		}()
	}
	return graph.New(&entry.Value.Model), &entry.Value.Project, refresh, nil
}

// appURL is the web app base URL of the active profile.
func (a *App) appURL() string {
	sess, err := a.Session()
	if err != nil {
		return ""
	}
	return weblink.AppURL(sess.Host, sess.AppURL)
}

func newMapCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "map [element]",
		Short: "Explore the C4 model in a full-screen view",
		Long: `Browse systems, containers, components and code in a tree, with each
element's description, technologies, relationships in both directions and
blast radius, or as a C4 diagram. Name an element to open on it.

  ↑↓ / j k     move               /     filter by name (fuzzy)
  → / enter    open               u     jump to what it uses
  ←            close, or parent   b     jump to what uses it
  E / C        expand / collapse  o     open in the web app
  y            copy the ID        q     quit
  d            diagram of the level: tab selects, enter drills down,
               backspace goes up, arrows and h j k l scroll`,
		Example: `  archyl map
  archyl map "Invoicing Service"`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeElements(app),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			ref, err := app.Project(ctx)
			if err != nil {
				return err
			}
			g, project, refresh, err := openGraph(ctx, app, ref.ID)
			if err != nil {
				return err
			}
			if len(g.Roots) == 0 {
				p.Println(p.Muted("The model of " + project.Name + " is empty."))
				p.Println(p.Hint("Discover it from your code in the web app, or push a DSL file:", "archyl sync"))
				return nil
			}

			focus := ""
			if len(args) == 1 {
				n, _ := g.Find(args[0])
				if n == nil {
					return fmt.Errorf("no element matches %q", args[0])
				}
				focus = n.ID
			}

			if !p.Interactive() {
				return fmt.Errorf("archyl map needs a terminal; use archyl show or --json commands in scripts")
			}

			appURL := app.appURL()
			explorer := tui.NewExplorer(tui.ExplorerOptions{
				Project: project.Name,
				Graph:   g,
				Theme:   p.Theme(),
				Focus:   focus,
				Link:    func(n *graph.Node) string { return weblink.Element(appURL, project.ID, n) },
				Open:    auth.OpenBrowser,
				Copy:    clipboard.WriteAll,
			})
			program := tea.NewProgram(explorer, tea.WithContext(ctx))
			refresh(func(g *graph.Graph) { program.Send(tui.GraphUpdatedMsg{Graph: g}) })
			_, err = program.Run()
			return err
		},
	}
}

// completeElements completes element names from the linked project's cached
// model. It never waits on the network: a stale or missing copy is refreshed
// in the background for the next tab.
func completeElements(app *App) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ref, err := app.Project(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		model, err := completionModel(cmd.Context(), app, ref.ID)
		if err != nil || model == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, n := range graph.New(model).All() {
			out = append(out, n.Name+"\t"+n.Level)
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}

func completionModel(ctx context.Context, app *App, projectID string) (*api.Model, error) {
	if !app.modelCacheOn() {
		client, err := app.Client()
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return client.Model(ctx, projectID)
	}
	entry := app.cachedModel(projectID)
	if !entry.Fresh(modelStaleAfter) {
		app.refreshModelLater(projectID)
	}
	if entry == nil {
		return nil, nil
	}
	return &entry.Value.Model, nil
}
