package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/codeowners"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

// ownersConcurrency bounds the per-element owner requests made when the
// server cannot answer with the whole ownership map.
const ownersConcurrency = 8

type codeownersOptions struct {
	org   string
	file  string
	write bool
	check bool
	yes   bool
}

func newCodeownersCmd(app *App) *cobra.Command {
	var opts codeownersOptions
	cmd := &cobra.Command{
		Use:   "codeowners",
		Short: "Generate CODEOWNERS from the ownership recorded in Archyl",
		Long: `Write the owners of your systems, containers and components into CODEOWNERS,
one line per folder, so reviews go to the people Archyl says own the code.

An element's folders are the ones holding the files the model records for it
and its code elements, else the folder named after it (orders/ for "Orders
Service"), else, for a container or system, the deepest folder holding
everything its components record. An element without owners of its own takes
those of its closest owned ancestor; its line is left out when the ancestor's
folder already covers it with the same owners. Elements sharing a folder share
its line.

A team is written @<org>/<team-slug>, the org being --org, else codeowners.org
in .archyl.json, else the owner of the git remote. A person is written as their
email. Map other handles in .archyl.json:

  "codeowners": {
    "org": "acme",
    "teams": {"platform": "@acme/video-platform"},
    "users": {"ada@example.com": "@ada"}
  }

The lines live between "# BEGIN archyl codeowners" and "# END archyl
codeowners"; everything outside the block is yours and kept. A new block goes
above your rules (and above GitLab [Section] headers), after the comments that
open the file: CODEOWNERS applies the last line that matches, so a rule of
yours below the block wins over a generated one. Keep a catch-all such as
"* @acme/leads" above the block. A block you moved stays where it is. When
nobody owns anything any more, --write removes the block. The file is the
first of .github/CODEOWNERS, CODEOWNERS, docs/CODEOWNERS and
.gitlab/CODEOWNERS that exists, else .github/CODEOWNERS (.gitlab/ for a GitLab
remote).

Without a flag it prints the whole file as it would be. --write updates it,
showing the diff and asking first in a terminal; --check exits 1 when the file
is out of date and prints the diff, for CI.`,
		Example: `  archyl codeowners
  archyl codeowners --write
  archyl codeowners --check        # in CI
  archyl codeowners --org acme --json | jq '.entries[].pattern'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCodeowners(cmd.Context(), app, opts)
		},
	}
	cmd.Flags().StringVar(&opts.org, "org", "", "organization or group prefixing team handles (default: the git remote's owner)")
	cmd.Flags().StringVarP(&opts.file, "file", "f", "", "the CODEOWNERS file, relative to the repository root")
	cmd.Flags().BoolVarP(&opts.write, "write", "w", false, "update the file")
	cmd.Flags().BoolVar(&opts.check, "check", false, "exit 1 when the file is out of date, for CI")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "write without showing the diff and asking")
	cmd.MarkFlagsMutuallyExclusive("write", "check")
	return cmd
}

// codeownersReport is the --json output.
type codeownersReport struct {
	File    string `json:"file"`
	InSync  bool   `json:"inSync"`
	Written bool   `json:"written"`
	Diff    string `json:"diff,omitempty"`
	codeowners.Result
}

// codeownersPlan is everything the modes act on.
type codeownersPlan struct {
	path     string // relative to the root, slash separated
	abs      string
	existing string
	next     string
	result   codeowners.Result
	handles  codeowners.Handles
}

func (c *codeownersPlan) changed() bool {
	return !codeowners.Equal(c.next, c.existing)
}

func (c *codeownersPlan) diff() string {
	return codeowners.Unified("a/"+c.path, "b/"+c.path, codeowners.Diff(c.existing, c.next))
}

func runCodeowners(
	ctx context.Context,
	app *App,
	opts codeownersOptions,
) error {
	p := app.Printer
	plan, err := planCodeowners(ctx, app, opts)
	if err != nil {
		return err
	}
	report := codeownersReport{File: plan.path, InSync: !plan.changed(), Result: plan.result}

	if plan.result.Owned == 0 && !opts.check && !opts.write {
		if p.JSON {
			return p.WriteJSON(report)
		}
		p.Eprintln(p.Muted("No element of this project has owners yet, so there is nothing to write."))
		p.Eprintln(p.Hint("Set owners on an element in the web app:", "archyl open <element>"))
		return nil
	}

	switch {
	case opts.check:
		return checkCodeowners(p, plan, report)
	case opts.write:
		return writeCodeowners(p, plan, report, opts.yes)
	case p.JSON:
		return p.WriteJSON(report)
	}
	if _, err := fmt.Fprint(p.Out, plan.next); err != nil {
		return err
	}
	summarizeCodeowners(p, plan)
	return nil
}

// planCodeowners gathers the repository, the model and its owners, and
// computes the file as it should be.
func planCodeowners(
	ctx context.Context,
	app *App,
	opts codeownersOptions,
) (*codeownersPlan, error) {
	ref, err := app.Project(ctx)
	if err != nil {
		return nil, err
	}
	git, err := app.Repo(ctx)
	if err != nil {
		return nil, err
	}
	files, err := git.Files(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the repository's files: %w", err)
	}
	g, _, err := loadGraph(ctx, app, ref.ID)
	if err != nil {
		return nil, err
	}
	client, err := app.Client()
	if err != nil {
		return nil, err
	}
	var owners map[string]codeowners.Owners
	if err := app.Printer.Spin("Reading who owns what…", func() error {
		owners, err = fetchOwners(ctx, client, g)
		return err
	}); err != nil {
		return nil, fmt.Errorf("read the owners: %w", err)
	}

	cfg, err := codeownersConfig(git.Root)
	if err != nil {
		return nil, err
	}
	remoteOwner, gitlab := remoteHost(git.Remote(ctx))
	handles := codeowners.Handles{
		Org:   firstNonEmpty(opts.org, cfg.Org, remoteOwner),
		Teams: cfg.Teams,
		Users: cfg.Users,
	}
	result := codeowners.Build(codeowners.Input{Graph: g, Files: files, Owners: owners, Handles: handles})

	plan := &codeownersPlan{result: result, handles: handles}
	if plan.path, err = codeownersFile(git.Root, opts.file, gitlab); err != nil {
		return nil, err
	}
	plan.abs = filepath.Join(git.Root, filepath.FromSlash(plan.path))
	if err := insideRoot(git.Root, plan.abs); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(plan.abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", plan.path, err)
	}
	plan.existing = string(raw)
	block := codeowners.Block(result.Entries)
	if result.Owned == 0 {
		// Nobody owns anything any more: the block goes rather than stay
		// stale or list nothing.
		block = ""
	}
	plan.next, err = codeowners.Splice(plan.existing, block)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", plan.path, err)
	}
	return plan, nil
}

// codeownersFile is the CODEOWNERS path relative to the root: --file, which
// must stay inside the repository, else the one the host reads.
func codeownersFile(
	root string,
	file string,
	gitlab bool,
) (string, error) {
	if file == "" {
		path, _ := codeowners.Locate(root, gitlab)
		return path, nil
	}
	clean := filepath.Clean(file)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("--file %s is outside the repository; give a path relative to its root, such as .github/CODEOWNERS", file)
	}
	return filepath.ToSlash(clean), nil
}

// insideRoot refuses a path that a symbolic link, on the file or on a folder
// above it, leads out of the repository.
func insideRoot(root, path string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	existing := path
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return nil
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(realRoot, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s leads outside the repository through a symbolic link; pass --file with a path inside it", path)
	}
	return nil
}

// codeownersConfig reads the handle overrides from .archyl.json; a
// repository without one has none.
func codeownersConfig(root string) (repo.CodeownersConfig, error) {
	link, err := repo.ReadLink(root)
	switch {
	case errors.Is(err, repo.ErrNotLinked):
		return repo.CodeownersConfig{}, nil
	case err != nil:
		return repo.CodeownersConfig{}, err
	case link.Codeowners == nil:
		return repo.CodeownersConfig{}, nil
	}
	return *link.Codeowners, nil
}

// remoteHost reads the owner of the git remote, as written, and whether it
// is on GitLab. A host Archyl does not know still has an owner in its path.
func remoteHost(remote string) (owner string, gitlab bool) {
	if hosted, err := repo.ParseHosted(remote, ""); err == nil {
		return hosted.Owner(), strings.HasPrefix(hosted.Provider, "gitlab")
	}
	host, path := splitRemote(remote)
	if i := strings.LastIndex(path, "/"); i > 0 {
		owner = path[:i]
	}
	return owner, strings.Contains(strings.ToLower(host), "gitlab")
}

// splitRemote cuts a remote URL, or an scp-like user@host:path, into its
// host and its path, keeping their case.
func splitRemote(remote string) (host, path string) {
	r := strings.TrimSpace(remote)
	if u, err := url.Parse(r); err == nil && strings.Contains(r, "://") {
		host, path = u.Hostname(), u.Path
	} else {
		if at := strings.Index(r, "@"); at >= 0 {
			r = r[at+1:]
		}
		host, path, _ = strings.Cut(r, ":")
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	return host, path
}

// fetchOwners reads every owned element in one call to the ownership map,
// and falls back to one call per element on a server without the map or
// when the map does not cover this project. Any other failure is the
// user's to see: falling back would hide it behind slower calls.
func fetchOwners(
	ctx context.Context,
	client *api.Client,
	g *graph.Graph,
) (map[string]codeowners.Owners, error) {
	elements, err := client.OwnershipMap(ctx)
	switch {
	case api.IsUnsupported(err), api.IsStatus(err, http.StatusNotFound):
		return ownersPerElement(ctx, client, g)
	case err != nil:
		return nil, err
	}
	if owners, covered := ownersFromMap(elements, g); covered {
		return owners, nil
	}
	return ownersPerElement(ctx, client, g)
}

// ownersFromMap keeps the map's elements that belong to the model. covered
// is false when none does: the map is the organization's, and a project of
// another organization is not in it.
func ownersFromMap(elements []api.OwnedElement, g *graph.Graph) (map[string]codeowners.Owners, bool) {
	owners := map[string]codeowners.Owners{}
	covered := false
	for _, e := range elements {
		if g.Node(e.ID) == nil {
			continue
		}
		covered = true
		if o := toOwners(e.Owners, e.TeamOwners); !o.Empty() {
			owners[e.ID] = o
		}
	}
	return owners, covered || len(ownable(g)) == 0
}

func ownersPerElement(
	ctx context.Context,
	client *api.Client,
	g *graph.Graph,
) (map[string]codeowners.Owners, error) {
	var mu sync.Mutex
	owners := map[string]codeowners.Owners{}
	// The first failure cancels the calls still running, and is the one
	// returned: the others only report the cancellation.
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(ownersConcurrency)
	for _, n := range ownable(g) {
		group.Go(func() error {
			people, teams, err := client.ElementOwners(ctx, n.Level, n.ID)
			if err != nil {
				return fmt.Errorf("owners of %s: %w", n.Name, err)
			}
			if o := toOwners(people, teams); !o.Empty() {
				mu.Lock()
				owners[n.ID] = o
				mu.Unlock()
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return owners, nil
}

// ownable are the elements that can have owners: systems, containers and
// components of the organization's own systems.
func ownable(g *graph.Graph) []*graph.Node {
	var out []*graph.Node
	for _, n := range g.All() {
		if n.Level != "code" && !n.Path()[0].External {
			out = append(out, n)
		}
	}
	return out
}

func toOwners(people []api.Owner, teams []api.TeamOwner) codeowners.Owners {
	var o codeowners.Owners
	for _, p := range people {
		o.People = append(o.People, codeowners.Person{Email: p.Email, Name: strings.TrimSpace(p.FirstName + " " + p.LastName)})
	}
	for _, t := range teams {
		o.Teams = append(o.Teams, codeowners.Team{Name: t.Name, Slug: t.Slug})
	}
	return o
}

// checkCodeowners fails when the file differs from what would be written.
func checkCodeowners(
	p *ui.Printer,
	plan *codeownersPlan,
	report codeownersReport,
) error {
	switch {
	case p.JSON:
		report.Diff = plan.diff()
		if err := p.WriteJSON(report); err != nil {
			return err
		}
	case plan.changed():
		printDiff(p, plan.diff())
		p.Eprintln(p.Cross() + " " + p.Bold(plan.path) + " is out of date. " + p.Hint("Update it with", "archyl codeowners --write"))
	default:
		p.Eprintln(p.Check() + " " + p.Bold(plan.path) + p.Muted(" matches the owners in Archyl"))
	}
	if !p.JSON {
		summarizeCodeowners(p, plan)
	}
	if plan.changed() {
		return &ExitError{Code: 1}
	}
	return nil
}

// writeCodeowners updates the file, after showing the diff and asking in a
// terminal.
func writeCodeowners(
	p *ui.Printer,
	plan *codeownersPlan,
	report codeownersReport,
	yes bool,
) error {
	if !plan.changed() {
		if p.JSON {
			return p.WriteJSON(report)
		}
		p.Println(p.Check() + " " + p.Bold(plan.path) + p.Muted(" already matches the owners in Archyl"))
		summarizeCodeowners(p, plan)
		return nil
	}
	if !yes && p.Interactive() {
		printDiff(p, plan.diff())
		p.Println()
		write := false
		if err := huh.NewConfirm().Title("Write " + plan.path + "?").Value(&write).Run(); err != nil {
			return err
		}
		if !write {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(plan.abs), 0o755); err != nil {
		return fmt.Errorf("create the folder of %s: %w", plan.path, err)
	}
	if err := os.WriteFile(plan.abs, []byte(plan.next), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", plan.path, err)
	}
	if p.JSON {
		report.Written = true
		report.InSync = true
		return p.WriteJSON(report)
	}
	p.Println(p.Check() + " Wrote " + p.Bold(plan.path) + p.Muted(" · "+ui.Plural(len(plan.result.Entries), "line", "lines")))
	summarizeCodeowners(p, plan)
	return nil
}

// printDiff colors a unified diff; piped, the colors drop out.
func printDiff(p *ui.Printer, diff string) {
	for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			p.Println(p.Bold(line))
		case strings.HasPrefix(line, "@@"):
			p.Println(p.Muted(line))
		case strings.HasPrefix(line, "+"):
			p.Println(p.Success(line))
		case strings.HasPrefix(line, "-"):
			p.Println(p.Danger(line))
		default:
			p.Println(line)
		}
	}
}

// summarizeCodeowners tells, on stderr, what the file could not say: owned
// elements without a folder and owners without a handle.
func summarizeCodeowners(p *ui.Printer, plan *codeownersPlan) {
	res := plan.result
	if len(res.Unplaced) > 0 {
		names := make([]string, 0, len(res.Unplaced))
		for _, e := range res.Unplaced {
			names = append(names, e.Name+" ("+e.Level+")")
		}
		p.Eprintln(p.Bang() + " " + ui.Plural(len(res.Unplaced), "owned element has", "owned elements have") +
			" no folder in this repository: " + strings.Join(names, ", "))
		p.Eprintln("  " + p.Muted("Record a file path on it in Archyl, or name its folder after it."))
	}
	if len(res.Unmapped) > 0 {
		names := make([]string, 0, len(res.Unmapped))
		teams := false
		for _, u := range res.Unmapped {
			names = append(names, u.Kind+" "+u.Name+" ("+strings.Join(u.Elements, ", ")+")")
			teams = teams || u.Kind == "team"
		}
		p.Eprintln(p.Bang() + " " + ui.Plural(len(res.Unmapped), "owner has", "owners have") +
			" no handle and were left out: " + strings.Join(names, "; "))
		fix := "Map them in .archyl.json under codeowners.teams or codeowners.users"
		if teams && plan.handles.Org == "" {
			fix += ", or pass --org"
		}
		p.Eprintln("  " + p.Muted(fix+"."))
	}
}
