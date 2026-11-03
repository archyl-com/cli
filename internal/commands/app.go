// Package commands defines the archyl command tree.
package commands

import (
	"context"
	"errors"
	"os"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/config"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/session"
	"github.com/archyl-com/cli/internal/ui"
)

// App is what every command shares: output, configuration, and lazily
// resolved session, repository and project.
type App struct {
	Printer *ui.Printer

	profile string
	project string
	json    bool
	noCache bool

	configDir string
	sess      *session.Session
}

// ConfigDir returns the configuration directory.
func (a *App) ConfigDir() (string, error) {
	if a.configDir != "" {
		return a.configDir, nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	a.configDir = dir
	return dir, nil
}

// Session resolves the active profile.
func (a *App) Session() (*session.Session, error) {
	if a.sess != nil {
		return a.sess, nil
	}
	dir, err := a.ConfigDir()
	if err != nil {
		return nil, err
	}
	sess, err := session.Resolve(dir, session.Options{Context: a.profile})
	if err != nil {
		return nil, err
	}
	a.sess = sess
	return sess, nil
}

// Client returns an authenticated API client, failing early with a hint when
// nobody is signed in.
func (a *App) Client() (*api.Client, error) {
	sess, err := a.Session()
	if err != nil {
		return nil, err
	}
	if _, err := sess.Credential(); err != nil {
		return nil, err
	}
	return sess.Client(), nil
}

// Repo opens the git repository around the working directory.
func (a *App) Repo(ctx context.Context) (*repo.Git, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return repo.Open(ctx, cwd)
}

// repoRoot finds the work tree root without starting git when it can: the
// prompt and status line resolve the project on every shell prompt.
func (a *App) repoRoot(ctx context.Context) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, err := repo.FindRoot(cwd); err == nil {
		return root, nil
	}
	git, err := repo.Open(ctx, cwd)
	if err != nil {
		return "", err
	}
	return git.Root, nil
}

// ProjectRef identifies the project a command acts on.
type ProjectRef struct {
	ID   string
	Name string
}

// Project resolves the project: --project, then ARCHYL_PROJECT_ID, then the
// repository's link file.
func (a *App) Project(ctx context.Context) (ProjectRef, error) {
	if a.project != "" {
		return ProjectRef{ID: a.project}, nil
	}
	if id := os.Getenv("ARCHYL_PROJECT_ID"); id != "" {
		return ProjectRef{ID: id}, nil
	}
	root, err := a.repoRoot(ctx)
	if errors.Is(err, repo.ErrNotARepository) {
		return ProjectRef{}, errNoProject
	}
	if err != nil {
		return ProjectRef{}, err
	}
	link, err := repo.ReadLink(root)
	if errors.Is(err, repo.ErrNotLinked) {
		return ProjectRef{}, errNoProject
	}
	if err != nil {
		return ProjectRef{}, err
	}
	return ProjectRef{ID: link.ProjectID, Name: link.ProjectName}, nil
}

var errNoProject = errors.New("no Archyl project for this directory")
