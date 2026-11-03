package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/config"
	"github.com/archyl-com/cli/internal/repo"
)

func newInitCmd(app *App) *cobra.Command {
	var opts initOptions

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Link this repository to an Archyl project",
		Long: `Write .archyl.json at the repository root so every archyl command, and the
Guard hook, knows which project this code belongs to. The file holds no secret;
commit it so your team and CI share the link.

When the repository's remote is connected to a project in Archyl, that project
is picked for you. When none is, init offers to start one: it creates the
project, connects the repository from its git remote, discovers the
architecture with AI while you watch, and opens the review. --new goes
straight there; --yes takes every default (and approves what the discovery
finds) without asking.`,
		Example: `  archyl init
  archyl init --project 3f2a9c4e-…
  archyl init --new
  archyl init --new --yes --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer

			git, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			if existing, err := repo.ReadLink(git.Root); err == nil && !opts.force && app.project == "" {
				if !p.Interactive() || opts.yes {
					return fmt.Errorf("already linked to %s; pass --force to relink", firstNonEmpty(existing.ProjectName, existing.ProjectID))
				}
				relink := false
				if err := huh.NewConfirm().
					Title("Already linked to " + firstNonEmpty(existing.ProjectName, existing.ProjectID)).
					Description("Link it to another project?").
					Value(&relink).Run(); err != nil {
					return err
				}
				if !relink {
					return nil
				}
			}

			if opts.createNew {
				return initNew(ctx, app, git, opts)
			}

			client, err := app.Client()
			if err != nil {
				return err
			}
			var projects []api.Project
			if err := p.Spin("Loading projects…", func() error {
				projects, err = client.Projects(ctx)
				return err
			}); err != nil {
				return err
			}

			remote := git.Remote(ctx)
			chosen, err := chooseProject(app, projects, remote, filepath.Base(git.Root), opts.yes)
			if errors.Is(err, errCreateProject) {
				return initNew(ctx, app, git, opts)
			}
			if err != nil {
				return err
			}

			sess, _ := app.Session()
			link := &repo.Link{ProjectID: chosen.ID, ProjectName: chosen.Name, OrganizationID: sess.OrganizationID}
			if sess.Host != config.DefaultHost {
				link.APIURL = sess.Host
			}
			if err := repo.WriteLink(git.Root, link); err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(link)
			}
			p.Println(fmt.Sprintf("%s Linked %s %s %s", p.Check(),
				p.Bold(filepath.Base(git.Root)), p.Muted("→"), p.Element("system", chosen.Name)))
			p.Println(p.Muted("  wrote " + repo.LinkFile + " — commit it to share the link"))
			p.Println()
			p.Println(p.Hint("See where a file lives:", "archyl where <file>"))
			p.Println(p.Hint("Connect your coding agent:", "archyl setup"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&opts.force, "force", false, "relink without asking")
	cmd.Flags().BoolVar(&opts.createNew, "new", false, "create a new project for this repository")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "take the defaults without asking (creates the project when none matches)")
	cmd.Flags().StringVar(&opts.name, "name", "", "name of the new project (default: the repository's name)")
	cmd.Flags().StringVar(&opts.team, "team", "", "team that owns the new project, by name, slug or ID")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "git provider of a self-hosted remote: github_enterprise, gitlab_private or gitea")
	cmd.Flags().StringVar(&opts.branch, "branch", "", "branch to discover (default: the remote's default branch)")
	cmd.Flags().BoolVar(&opts.noDiscover, "no-discover", false, "create and link the project without running a discovery")
	cmd.Flags().BoolVar(&opts.noSetup, "no-setup", false, "do not offer to connect coding agents")
	return cmd
}

// chooseProject resolves --project, then a project whose connected
// repository matches the remote, then asks, offering a new project first.
// errCreateProject means the new project was chosen, or is the default.
func chooseProject(app *App, projects []api.Project, remote, dirName string, yes bool) (api.Project, error) {
	if app.project != "" {
		for _, pr := range projects {
			if pr.ID == app.project || strings.EqualFold(pr.Slug, app.project) {
				return pr, nil
			}
		}
		return api.Project{}, fmt.Errorf("no project %q in this organization", app.project)
	}

	matches := projectsForRemote(projects, remote)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if yes && len(matches) == 0 {
		return api.Project{}, errCreateProject
	}

	if !app.Printer.Interactive() {
		if len(projects) == 0 {
			return api.Project{}, errors.New("no project in this organization yet; create one with archyl init --new")
		}
		return api.Project{}, errors.New("cannot pick a project without a terminal; pass --project <id>, or --new to create one")
	}
	if len(projects) == 0 {
		return api.Project{}, errCreateProject
	}

	const newProject = "\x00new"
	options := make([]huh.Option[string], 0, len(projects)+1)
	options = append(options, huh.NewOption(app.Printer.Accent("+ New project")+app.Printer.Muted("  create it, connect this repository, discover it"), newProject))
	selected := ""
	for _, pr := range projects {
		label := pr.Name
		if pr.Description != "" {
			label += app.Printer.Muted("  " + truncate(pr.Description, 50))
		}
		options = append(options, huh.NewOption(label, pr.ID))
		if selected == "" && strings.EqualFold(pr.Slug, dirName) {
			selected = pr.ID
		}
	}
	if err := huh.NewSelect[string]().
		Title("Which project is this repository part of?").
		Options(options...).
		Filtering(true).
		Height(min(len(options)+2, 14)).
		Value(&selected).
		Run(); err != nil {
		return api.Project{}, err
	}
	if selected == newProject {
		return api.Project{}, errCreateProject
	}
	for _, pr := range projects {
		if pr.ID == selected {
			return pr, nil
		}
	}
	return api.Project{}, errors.New("no project selected")
}

// projectsForRemote returns the projects with a repository on this remote.
func projectsForRemote(projects []api.Project, remote string) []api.Project {
	want := repo.NormalizeRemote(remote)
	if want == "" {
		return nil
	}
	var out []api.Project
	for _, pr := range projects {
		for _, r := range pr.Repositories {
			if repo.NormalizeRemote(r.URL) == want || repo.NormalizeRemote(r.CloneURL) == want {
				out = append(out, pr)
				break
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
