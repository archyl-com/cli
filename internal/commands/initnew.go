package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/config"
	"github.com/archyl-com/cli/internal/diagram"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/weblink"
)

// errCreateProject is what chooseProject returns when the user picks a new
// project over the existing ones.
var errCreateProject = errors.New("create a new project")

// initOptions are the flags of archyl init.
type initOptions struct {
	force      bool
	createNew  bool
	yes        bool
	noDiscover bool
	noSetup    bool
	name       string
	team       string
	provider   string
	branch     string
}

// initNewResult is the --json output of archyl init when it creates the
// project.
type initNewResult struct {
	Link       *repo.Link     `json:"link"`
	Project    api.Project    `json:"project"`
	Repository repo.Hosted    `json:"repository"`
	Discovery  *initDiscovery `json:"discovery,omitempty"`
}

type initDiscovery struct {
	Job      *api.DiscoveryJob `json:"job,omitempty"`
	Pending  int               `json:"pending"`
	Approved int               `json:"approved"`
	// Problem names why the discovery did not run or failed: credential,
	// plan, access or failed.
	Problem string `json:"problem,omitempty"`
	Error   string `json:"error,omitempty"`
}

// initNew creates a project for the repository, connects the repository,
// discovers its architecture and hands over to the review.
func initNew(ctx context.Context, app *App, git *repo.Git, opts initOptions) error {
	p := app.Printer
	hosted, err := repo.ParseHosted(git.Remote(ctx), opts.provider)
	var unsupported *repo.UnsupportedHostError
	switch {
	case errors.Is(err, repo.ErrNoRemote):
		if !opts.noDiscover {
			return errors.New("this repository has no git remote, and a discovery reads the code from your git provider: " +
				"push it to GitHub, GitLab, Bitbucket or Azure DevOps, or skip the discovery with archyl init --new --no-discover")
		}
		hosted = repo.Hosted{Name: filepath.Base(git.Root)}
		hosted.FullName = hosted.Name
	case errors.As(err, &unsupported):
		return err
	case err != nil:
		return err
	}

	client, err := app.Client()
	if err != nil {
		return err
	}
	sess, err := app.Session()
	if err != nil {
		return err
	}

	team, err := pickTeam(ctx, app, client, sess.OrganizationID, opts)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(firstNonEmpty(opts.name, hosted.Name))
	discoverNow := !opts.noDiscover

	if p.Interactive() && !opts.yes {
		where := ""
		if team.Name != "" {
			where = " in " + team.Name
		}
		create := true
		fields := []huh.Field{
			huh.NewInput().Title("Project name").Value(&name).Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a project needs a name")
				}
				return nil
			}),
			confirmCreate(hosted, where, &create),
		}
		if !opts.noDiscover {
			fields = append(fields, huh.NewConfirm().
				Title("Discover its architecture with AI now?").
				Description("Counts as one AI discovery on your plan. Nothing joins the model before you review it.").
				Value(&discoverNow))
		}
		if err := huh.NewForm(huh.NewGroup(fields...)).Run(); err != nil {
			return err
		}
		if !create {
			p.Println(p.Muted("Nothing created."))
			return nil
		}
		name = strings.TrimSpace(name)
	}

	var project *api.Project
	if err := p.Spin("Creating "+name+"…", func() error {
		project, err = client.CreateProject(ctx, api.CreateProjectRequest{
			Name:        name,
			Slug:        projectSlug(name, time.Now()),
			Description: "Architecture of " + hosted.FullName,
			TeamID:      team.ID,
		})
		return err
	}); err != nil {
		return fmt.Errorf("create the project: %w", err)
	}

	branch := firstNonEmpty(opts.branch, strings.TrimPrefix(git.DefaultBase(ctx), "origin/"))
	if err := connectRepository(ctx, p, client, project.ID, hosted, branch); err != nil {
		return fmt.Errorf("connect %s to %s (the project was created; connect it in the web app: %s): %w",
			hosted.FullName, project.Name, projectSettingsURL(app, project.ID), err)
	}

	link := &repo.Link{ProjectID: project.ID, ProjectName: project.Name, OrganizationID: sess.OrganizationID}
	if sess.Host != config.DefaultHost {
		link.APIURL = sess.Host
	}
	if err := repo.WriteLink(git.Root, link); err != nil {
		return err
	}
	result := initNewResult{Link: link, Project: *project, Repository: hosted}

	if !p.JSON {
		p.Println(p.Check() + " Created " + p.Element("system", project.Name) + p.Muted("  "+app.appURLFor(project.ID)))
		if hosted.Provider != "" {
			p.Println(p.Check() + " Connected " + p.Bold(hosted.FullName) + p.Muted("  "+providerLabel(hosted.Provider)))
		}
		p.Println(p.Check() + " Linked " + p.Bold(filepath.Base(git.Root)) + p.Muted("  wrote "+repo.LinkFile+" — commit it to share the link"))
	}

	if discoverNow {
		result.Discovery = runFirstDiscovery(ctx, app, client, project, hosted, branch, opts)
	}

	if p.JSON {
		if err := p.WriteJSON(result); err != nil {
			return err
		}
	} else {
		finishInit(ctx, app, project, result.Discovery, opts)
	}
	if d := result.Discovery; d != nil && (d.Problem == "failed" || d.Problem == "access") {
		return &ExitError{Code: 1}
	}
	return nil
}

// confirmCreate asks before creating the project, and connecting the repository
// when it has a remote.
func confirmCreate(hosted repo.Hosted, where string, create *bool) huh.Field {
	if hosted.Provider == "" {
		return huh.NewConfirm().Title("Create it" + where + "?").Value(create)
	}
	return huh.NewConfirm().
		Title("Create it" + where + " and connect " + hosted.FullName + "?").
		Description(providerLabel(hosted.Provider) + " · " + hosted.URL).
		Value(create)
}

// connectRepository links the repository to the project. One with no remote has
// nothing to link yet.
func connectRepository(ctx context.Context, p *ui.Printer, client *api.Client, projectID string, hosted repo.Hosted, branch string) error {
	if hosted.Provider == "" {
		return nil
	}
	return p.Spin("Connecting "+hosted.FullName+"…", func() error {
		return client.LinkRepository(ctx, projectID, api.LinkRepositoryRequest{
			Provider: hosted.Provider, Name: hosted.Name, FullName: hosted.FullName,
			URL: hosted.URL, CloneURL: hosted.CloneURL, DefaultBranch: branch,
		})
	})
}

// runFirstDiscovery starts the discovery and follows it: live in a terminal,
// line by line elsewhere, quietly with --json. Problems are explained, not
// returned: the project exists either way.
func runFirstDiscovery(
	ctx context.Context,
	app *App,
	client *api.Client,
	project *api.Project,
	hosted repo.Hosted,
	branch string,
	opts initOptions,
) *initDiscovery {
	p := app.Printer
	out := &initDiscovery{}
	job, err := client.StartDiscovery(ctx, project.ID, hosted.FullName, branch)
	if err != nil {
		out.Problem, out.Error = startProblem(err), err.Error()
		explainDiscoveryProblem(app, project, hosted, out)
		return out
	}

	if p.Interactive() {
		if !p.JSON {
			p.Println()
		}
		view := tui.NewDiscovery(tui.DiscoveryOptions{
			Context:    ctx,
			Theme:      p.Theme(),
			Repository: hosted.FullName,
			Job:        job,
			Poll:       discoverPoll,
			Status:     client.DiscoveryStatus,
			Pending: func(ctx context.Context) ([]api.PendingDiscovery, error) {
				return client.PendingDiscoveries(ctx, project.ID)
			},
		})
		if _, err := tea.NewProgram(view, tea.WithContext(ctx)).Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			out.Problem, out.Error = "failed", err.Error()
			return out
		}
		job = view.Job()
		out.Job = job
		switch {
		case view.Err != nil:
			out.Problem, out.Error = "failed", view.Err.Error()
			p.Println(p.Cross() + " Lost track of the discovery: " + view.Err.Error())
			p.Println("  " + p.Hint("It goes on; review what it finds with", "archyl discover review"))
			return out
		case view.Detached:
			out.Problem = "detached"
			p.Println(p.Muted("  Stopped following; the discovery goes on."))
			p.Println("  " + p.Hint("Review what it finds with", "archyl discover review"))
			return out
		}
	} else {
		if !p.JSON {
			p.Eprintln(p.Accent("◆") + " Discovering " + p.Bold(hosted.FullName) + p.Muted(" · "+shortID(job.ID)))
		}
		jobs := []*api.DiscoveryJob{job}
		if err := followDiscoveries(ctx, p, client, jobs); err != nil {
			out.Problem, out.Error = "failed", err.Error()
			return out
		}
		job = jobs[0]
		out.Job = job
	}

	if job.Status == "failed" {
		out.Problem, out.Error = jobProblem(job.Error), firstNonEmpty(job.Error, "no reason given")
		explainDiscoveryProblem(app, project, hosted, out)
		if out.Problem != "access" {
			out.Problem = "failed"
		}
		return out
	}

	pending, err := client.PendingDiscoveries(ctx, project.ID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Pending = len(pending)
	if !p.Interactive() && !p.JSON && len(pending) > 0 {
		items := tui.TriageItems(pending)
		for _, line := range tui.DiscoveryTree(items, 4) {
			p.Println("  " + line.Prefix + p.Element(line.Level, treeLineName(line)))
		}
		byLevel := map[string]int{}
		for _, it := range items {
			byLevel[it.ElementType]++
		}
		p.Println("  " + p.Muted(tui.DiscoveryCounts(byLevel)))
	}

	switch {
	case len(pending) == 0:
	case opts.yes:
		ids := make([]string, len(pending))
		for i, pd := range pending {
			ids[i] = pd.ID
		}
		if err := p.Spin("Approving "+ui.Plural(len(ids), "element", "elements")+"…", func() error {
			return client.ApproveDiscoveries(ctx, project.ID, ids)
		}); err != nil {
			out.Error = err.Error()
			return out
		}
		app.InvalidateModel(project.ID)
		out.Approved, out.Pending = len(ids), 0
		if !p.JSON {
			p.Println(p.Check() + " Approved " + p.Bold(ui.Plural(len(ids), "element", "elements")))
		}
	case p.Interactive():
		before := len(pending)
		if err := runTriage(ctx, app, client, project.Name, project.ID, pending); err != nil {
			out.Error = err.Error()
			return out
		}
		if left, err := client.PendingDiscoveries(ctx, project.ID); err == nil {
			out.Pending = len(left)
		}
		out.Approved = max(0, before-out.Pending)
	default:
		p.Println("  " + p.Bold(ui.Plural(len(pending), "element", "elements")) + p.Muted(" await review"))
		p.Println("  " + p.Hint("Review them with", "archyl discover review"))
	}
	return out
}

func treeLineName(line tui.TreeLine) string {
	if line.More > 0 {
		return fmt.Sprintf("+%d more", line.More)
	}
	return line.Name
}

// startProblem classifies why a discovery did not start.
func startProblem(err error) string {
	switch {
	case api.IsStatus(err, http.StatusConflict) && strings.Contains(err.Error(), "cannot read"):
		return "access"
	case api.IsStatus(err, http.StatusConflict):
		return "credential"
	case api.IsStatus(err, http.StatusForbidden) && planMessage(err.Error()):
		return "plan"
	}
	return "failed"
}

func planMessage(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "plan") || strings.Contains(s, "quota")
}

// providerRefusal is a git provider turning the request down, as the
// backend's git adapters word it: a 401, 403 or 404 status, or GitHub's
// "Bad credentials".
var providerRefusal = regexp.MustCompile(`\b(401|403|404)\b|bad credentials`)

// jobProblem tells a repository the provider refused from any other failure.
// Only the repository fetch counts: the backend fails a discovery with
// "Failed to fetch repository: <git adapter error>" there, while an AI
// provider's 404 or "model not found" comes as "AI analysis failed: …".
func jobProblem(message string) string {
	m := strings.ToLower(message)
	fetching := strings.Contains(m, "failed to fetch repository") || strings.HasPrefix(m, "failed to get tree")
	if fetching && !strings.Contains(m, "rate limit") && providerRefusal.MatchString(m) {
		return "access"
	}
	return "failed"
}

// explainDiscoveryProblem says what went wrong and how to go on. The project
// and its link are kept, so the fix always ends with archyl discover.
func explainDiscoveryProblem(app *App, project *api.Project, hosted repo.Hosted, d *initDiscovery) {
	p := app.Printer
	if p.JSON {
		return
	}
	provider := providerLabel(hosted.Provider)
	p.Println()
	switch d.Problem {
	case "credential":
		p.Println(p.Bang() + " Archyl has no " + provider + " access to read " + p.Bold(hosted.FullName) + ".")
		p.Println(p.Muted("  Sign in to the web app with " + provider + ", or give the project an access token:"))
		p.Println("  " + p.Code(projectSettingsURL(app, project.ID)))
	case "plan":
		p.Println(p.Bang() + " " + d.Error)
		p.Println("  " + p.Code(app.appURL()+"/organization/subscription"))
	case "access":
		p.Println(p.Cross() + " " + provider + " refused " + p.Bold(hosted.FullName) + p.Muted(": "+d.Error))
		p.Println(p.Muted("  The " + provider + " account Archyl reads with cannot see this repository. Grant it access"))
		p.Println(p.Muted("  (for GitHub, install the Archyl app on the repository), or set an access token:"))
		p.Println("  " + p.Code(projectSettingsURL(app, project.ID)))
	default:
		p.Println(p.Cross() + " The discovery failed: " + d.Error)
	}
	p.Println("  " + p.Hint(project.Name+" is kept. Try again with", "archyl discover"))
}

// finishInit prints the model, offers to connect the coding agents and says
// what to try next.
func finishInit(ctx context.Context, app *App, project *api.Project, d *initDiscovery, opts initOptions) {
	p := app.Printer
	if d != nil && d.Approved > 0 {
		printContextDiagram(ctx, app, project.ID)
	}

	if !opts.noSetup && p.Interactive() && !opts.yes {
		p.Println()
		connect := true
		if err := huh.NewConfirm().
			Title("Connect your coding agents to Archyl?").
			Description("Claude Code, Cursor, VS Code or Codex: they read the architecture before they write.").
			Value(&connect).Run(); err == nil && connect {
			setup := newSetupCmd(app)
			setup.SetContext(ctx)
			if err := setup.RunE(setup, nil); err != nil {
				p.Println(p.Cross() + " " + err.Error())
				p.Println("  " + p.Hint("Try again with", "archyl setup"))
			}
		}
	}

	p.Println()
	p.Println(p.Heading(" What next"))
	if d == nil || d.Job == nil {
		p.Println("  " + p.Hint("Discover the architecture:", "archyl discover"))
	} else if d.Pending > 0 {
		p.Println("  " + p.Hint("Review what the discovery found:", "archyl discover review"))
	}
	p.Println("  " + p.Hint("Explore the model:", "archyl map"))
	p.Println("  " + p.Hint("See what your branch affects:", "archyl impact"))
	p.Println("  " + p.Hint("Ask about it:", `archyl ask "How does a request reach the database?"`))
	if opts.noSetup || opts.yes || !p.Interactive() {
		p.Println("  " + p.Hint("Connect your coding agents:", "archyl setup"))
	}
}

// printContextDiagram draws the approved model: the system context, or the
// containers when the repository is the only system, which is what a first
// discovery usually finds.
func printContextDiagram(ctx context.Context, app *App, projectID string) {
	p := app.Printer
	snap, err := loadModel(ctx, app, projectID)
	if err != nil {
		return
	}
	g := graph.New(&snap.Model)
	if len(g.Roots) == 0 {
		return
	}
	var focus *graph.Node
	if len(g.Roots) == 1 {
		focus = g.Roots[0]
	}
	view := diagram.For(g, focus)
	p.Println()
	p.Println(diagramHeader(p, view))
	p.Println()
	p.Println(diagram.Text(view, diagram.TextOptions{Theme: p.Theme(), Color: true, Width: p.Width()}))
}

// pickTeam chooses the team the project goes to: --team, the only team the
// caller can edit, or a choice. Without an organization or teams it returns
// no team and the server picks the caller's default.
func pickTeam(ctx context.Context, app *App, client *api.Client, organizationID string, opts initOptions) (api.Team, error) {
	if organizationID == "" {
		if opts.team != "" {
			return api.Team{ID: opts.team}, nil
		}
		return api.Team{}, nil
	}
	teams, err := client.Teams(ctx, organizationID)
	if err != nil {
		if opts.team != "" {
			return api.Team{ID: opts.team}, nil
		}
		return api.Team{}, nil
	}
	var editable []api.Team
	for _, t := range teams {
		if t.CanEdit() {
			editable = append(editable, t)
		}
	}
	if opts.team != "" {
		for _, t := range teams {
			if t.ID == opts.team || strings.EqualFold(t.Slug, opts.team) || strings.EqualFold(t.Name, opts.team) {
				return t, nil
			}
		}
		return api.Team{}, fmt.Errorf("no team %q in this organization", opts.team)
	}
	switch {
	case len(editable) == 0:
		return api.Team{}, nil
	case len(editable) == 1 || !app.Printer.Interactive() || opts.yes:
		return editable[0], nil
	}
	options := make([]huh.Option[string], len(editable))
	for i, t := range editable {
		options[i] = huh.NewOption(t.Name, t.ID)
	}
	selected := editable[0].ID
	if err := huh.NewSelect[string]().
		Title("Which team owns the project?").
		Options(options...).
		Value(&selected).
		Run(); err != nil {
		return api.Team{}, err
	}
	for _, t := range editable {
		if t.ID == selected {
			return t, nil
		}
	}
	return editable[0], nil
}

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// projectSlug derives a slug from the name. Slugs are unique across Archyl,
// so a time suffix keeps two "api" projects apart, as the web app does.
func projectSlug(name string, now time.Time) string {
	base := strings.Trim(slugUnsafe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "project"
	}
	return base + "-" + strconv.FormatInt(now.UnixMilli(), 36)
}

func providerLabel(provider string) string {
	switch provider {
	case "github":
		return "GitHub"
	case "gitlab":
		return "GitLab"
	case "bitbucket":
		return "Bitbucket"
	case "azure_devops":
		return "Azure DevOps"
	case "github_enterprise":
		return "GitHub Enterprise"
	case "gitlab_private":
		return "GitLab (self-hosted)"
	case "gitea":
		return "Gitea"
	}
	return provider
}

// appURLFor links to a project in the web app.
func (a *App) appURLFor(projectID string) string {
	return weblink.Project(a.appURL(), projectID)
}

// projectSettingsURL is where a project's git access is configured.
func projectSettingsURL(app *App, projectID string) string {
	return app.appURLFor(projectID) + "/settings/git-integration"
}
