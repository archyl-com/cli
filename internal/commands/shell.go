package commands

import (
	"context"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/shell"
)

// runShell opens the session: a prompt for questions and /commands. resume
// reopens the repository's last conversation.
func runShell(cmd *cobra.Command, app *App, resume bool) error {
	ctx := cmd.Context()
	p := app.Printer
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	o := shell.Options{
		Theme:       p.Theme(),
		Dark:        p.Dark(),
		Version:     buildinfo.Version,
		Exe:         exe,
		HistoryPath: filepath.Join(cache.Dir(), "history"),
		Resume:      resume,
	}
	if app.profile != "" {
		o.GlobalArgs = append(o.GlobalArgs, "--profile", app.profile)
	}
	if app.project != "" {
		o.GlobalArgs = append(o.GlobalArgs, "--project", app.project)
	}

	quick, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if git, err := app.Repo(quick); err == nil {
		o.Root = git.Root
		o.Branch = git.Branch(quick)
		o.Files = func() []string {
			files, _ := git.Files(context.Background())
			return files
		}
	}
	if sess, err := app.Session(); err == nil {
		o.Host = hostLabel(sess.Host)
		if _, err := sess.Credential(); err == nil {
			o.Client = sess.Client()
		}
	}
	if ref, err := app.Project(quick); err == nil && o.Client != nil {
		o.Linked, o.ProjectID = true, ref.ID
		if project, err := o.Client.Project(quick, ref.ID); err == nil {
			o.Project = project.Name
		}
		if drift, err := o.Client.LatestDrift(quick, ref.ID); err == nil && drift != nil && drift.TotalElements > 0 {
			o.Score, o.HasScore = drift.Score, true
		}
		client, projectID := o.Client, ref.ID
		o.Elements = func() []string {
			model := shellModel(app, client, projectID)
			if model == nil {
				return nil
			}
			var names []string
			for _, n := range graph.New(model).All() {
				names = append(names, n.Name)
			}
			return names
		}
	}

	sessionRoot := o.Root
	if sessionRoot == "" {
		sessionRoot, _ = os.Getwd()
	}
	o.SessionDir = shell.SessionDir(cache.Dir(), sessionRoot, o.ProjectID)

	_, err = tea.NewProgram(shell.New(o), tea.WithContext(ctx)).Run()
	return err
}

// shellModel feeds # suggestions: the cached model when there is one (then
// refreshed in the background), the API's otherwise.
func shellModel(app *App, client *api.Client, projectID string) *api.Model {
	if entry := app.cachedModel(projectID); entry != nil {
		if !entry.Fresh(modelStaleAfter) {
			app.refreshModelLater(projectID)
		}
		return &entry.Value.Model
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := fetchSnapshot(ctx, client, projectID)
	if err != nil {
		return nil
	}
	app.saveModel(projectID, snap)
	return &snap.Model
}
