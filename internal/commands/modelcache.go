package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/session"
)

const (
	// modelFreshFor is how long a snapshot is served without asking the API.
	modelFreshFor = 30 * time.Second
	// modelRevalidateTimeout is how long a one-shot command waits for a fresh
	// model before answering from the cache.
	modelRevalidateTimeout = 1500 * time.Millisecond
	// modelFetchTimeout bounds a background refresh.
	modelFetchTimeout = 30 * time.Second
	// modelStaleAfter is the age past which completion asks for a refresh.
	modelStaleAfter = time.Minute
)

// modelCacheOn reports whether commands may read and write the model cache.
func (a *App) modelCacheOn() bool {
	return !a.noCache && !cache.Disabled()
}

// cacheScope is the active profile's cache key. It holds no secret: an
// ARCHYL_API_KEY override only contributes its hash.
func (a *App) cacheScope() (cache.Scope, bool) {
	sess, err := a.Session()
	if err != nil {
		return cache.Scope{}, false
	}
	s := cache.Scope{Profile: sess.Name, Host: sess.Host, OrgID: sess.OrganizationID}
	if key := os.Getenv("ARCHYL_API_KEY"); key != "" {
		sum := sha256.Sum256([]byte(key))
		s.Identity = "key:" + hex.EncodeToString(sum[:])
	}
	return s, true
}

// cachedModel returns the project's snapshot, or nil.
func (a *App) cachedModel(projectID string) *cache.Entry[cache.ModelSnapshot] {
	if !a.modelCacheOn() {
		return nil
	}
	scope, ok := a.cacheScope()
	if !ok {
		return nil
	}
	entry, _ := cache.LoadModel(scope, projectID)
	return entry
}

func (a *App) saveModel(projectID string, snap *cache.ModelSnapshot) {
	if !a.modelCacheOn() {
		return
	}
	if scope, ok := a.cacheScope(); ok {
		_ = cache.SaveModel(scope, projectID, *snap)
	}
}

// InvalidateModel drops the project's cached model. Call it after anything
// that writes the model (sync, DSL push, approved discoveries, new ADRs).
func (a *App) InvalidateModel(projectID string) {
	if scope, ok := a.cacheScope(); ok {
		_ = cache.InvalidateModel(scope, projectID)
	}
}

// refreshModelLater refreshes the snapshot in a detached process, at most once
// a minute per project.
func (a *App) refreshModelLater(projectID string) {
	scope, ok := a.cacheScope()
	if !ok || !a.modelCacheOn() || !cache.ClaimRefresh(scope, projectID, modelStaleAfter) {
		return
	}
	a.spawnForProject("__refresh-model", projectID)
}

// spawnForProject runs a background archyl command against the same profile
// and project, so it writes to the same cache scope.
func (a *App) spawnForProject(command, projectID string, extra ...string) {
	args := append([]string{command}, extra...)
	args = append(args, "--project", projectID)
	if a.profile != "" {
		args = append(args, "--profile", a.profile)
	}
	spawnSelf(args...)
}

// fetchSnapshot loads the project, its model and its decisions in parallel.
// Decisions are best effort: an element still renders without them.
func fetchSnapshot(ctx context.Context, client *api.Client, projectID string) (*cache.ModelSnapshot, error) {
	var (
		project *api.Project
		model   *api.Model
		adrs    []api.ADR
		errs    = make([]error, 2)
		wg      sync.WaitGroup
	)
	wg.Go(func() { project, errs[0] = client.Project(ctx, projectID) })
	wg.Go(func() { model, errs[1] = client.Model(ctx, projectID) })
	wg.Go(func() { adrs, _ = client.ADRs(ctx, projectID) })
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &cache.ModelSnapshot{Project: *project, Model: *model, ADRs: adrs}, nil
}

// loadModel is the one-shot read: a snapshot younger than modelFreshFor is
// served as is; an older one waits briefly for a fresh copy and is the
// fallback when the API is slow or unreachable; without one it fetches as
// usual.
func loadModel(ctx context.Context, app *App, projectID string) (*cache.ModelSnapshot, error) {
	p := app.Printer
	client, err := app.Client()
	if err != nil {
		return nil, err
	}
	entry := app.cachedModel(projectID)
	if entry.Fresh(modelFreshFor) {
		return &entry.Value, nil
	}

	fetchCtx := ctx
	if entry != nil {
		var cancel context.CancelFunc
		fetchCtx, cancel = context.WithTimeout(ctx, modelRevalidateTimeout)
		defer cancel()
	}
	var snap *cache.ModelSnapshot
	err = p.Spin("Loading the model…", func() error {
		snap, err = fetchSnapshot(fetchCtx, client, projectID)
		return err
	})
	switch {
	case err == nil:
		app.saveModel(projectID, snap)
		return snap, nil
	case entry == nil || ctx.Err() != nil:
		return nil, err
	case !transient(err):
		// The API answered and said no: access may be gone, so the snapshot
		// must not outlive it.
		app.InvalidateModel(projectID)
		return nil, err
	}
	app.refreshModelLater(projectID)
	p.Eprintln(p.Muted("Showing the model as cached " + ago(entry.SavedAt) + ": the API did not answer in time."))
	return &entry.Value, nil
}

// transient reports whether err says nothing about access: a timeout, a
// network failure or a server error, as opposed to a 4xx or a lost session.
func transient(err error) bool {
	if errors.Is(err, session.ErrNotLoggedIn) {
		return false
	}
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return apiErr.Status >= 500 || apiErr.Status == http.StatusTooManyRequests
	}
	return true
}

func newRefreshModelCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:    "__refresh-model",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ref, err := app.Project(cmd.Context())
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), modelFetchTimeout)
			defer cancel()
			snap, err := fetchSnapshot(ctx, client, ref.ID)
			if err != nil {
				if !transient(err) {
					app.InvalidateModel(ref.ID)
				}
				return err
			}
			app.saveModel(ref.ID, snap)
			return nil
		},
	}
}

func newCacheCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the local model cache",
		Long: `Commands that read the C4 model answer from a local copy and refresh it from
the API, so they start instantly. The copy lives in your user cache directory,
one per profile, organization and project, readable only by you.

Skip it for one command with --no-cache, or everywhere with ARCHYL_NO_CACHE=1.`,
		Args: cobra.NoArgs,
	}
	var all bool
	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Delete the cached models of the active profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := app.Printer
			if all {
				if err := cache.ClearModels(); err != nil {
					return err
				}
				p.Println(p.Check() + " Cleared the cached models of every profile")
				return nil
			}
			sess, err := app.Session()
			if err != nil {
				return err
			}
			if err := cache.ClearProfile(sess.Name); err != nil {
				return err
			}
			p.Println(p.Check() + " Cleared the cached models of profile " + p.Bold(sess.Name))
			return nil
		},
	}
	clearCmd.Flags().BoolVar(&all, "all", false, "clear every profile's cached models")
	cmd.AddCommand(clearCmd)
	return cmd
}
