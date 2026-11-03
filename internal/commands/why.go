package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
)

const (
	// maxWhyElements bounds the elements a file is explained through.
	maxWhyElements = 3
	maxWhyChanges  = 5
	maxWhyMemories = 6
	// commitWindow is how close to a commit a decision must be dated to be
	// shown as made around it.
	commitWindow = 14 * 24 * time.Hour
)

// whyTarget is "path" or "path:line".
var whyTarget = regexp.MustCompile(`^(.+):(\d+)$`)

func newWhyCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "why <file[:line]>",
		Short: "Why this code is the way it is: elements, decisions, owners, history",
		Long: `git blame for the architecture. For a file, or one line of it: the elements it
belongs to, the decisions that apply (accepted first), who owns it, what agents
and people remembered about it, the latest model changes to those elements and
the conformance rules aimed at it.

With a line, the commit that last changed it is tied to the decisions made
around the same time.

In a terminal it renders; piped or run by an agent it prints compact markdown.`,
		Example: `  archyl why internal/billing/invoice.go
  archyl why internal/billing/invoice.go:42
  archyl why --json src/checkout/cart.ts | jq '.decisions[].title'`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveDefault
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer

			arg, line := splitWhyTarget(args[0])
			git, err := app.Repo(ctx)
			if err != nil {
				return err
			}
			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			rel, err := git.Rel(cwd, arg)
			if err != nil {
				return err
			}
			if info, err := os.Stat(arg); err == nil && info.IsDir() {
				return fmt.Errorf("%s is a directory; for a whole tree: archyl where %s", arg, arg)
			}
			if _, err := os.Stat(arg); errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no file %s here", arg)
			}

			in := whyInput{
				ProjectID: project.ID,
				Path:      rel,
				Line:      line,
				Snapshot: func(ctx context.Context) (*cache.ModelSnapshot, error) {
					return whySnapshot(ctx, app, client, project.ID)
				},
				Commit: func(ctx context.Context) (*repo.Commit, error) {
					if line > 0 {
						return git.Blame(ctx, rel, line)
					}
					return git.LastCommit(ctx, rel)
				},
			}
			var r *whyReport
			if err := p.Spin("Tracing the architecture behind it…", func() error {
				r, err = gatherWhy(ctx, client, in)
				return err
			}); err != nil {
				return err
			}

			if p.JSON {
				return p.WriteJSON(r)
			}
			if p.Interactive() || p.Embedded {
				renderWhy(p, r)
				return nil
			}
			_, _ = fmt.Fprintln(p.Out, ui.Sanitize(r.markdown()))
			return nil
		},
	}
}

// splitWhyTarget reads "path:42" as the path and line 42; anything else is a
// path.
func splitWhyTarget(arg string) (string, int) {
	m := whyTarget.FindStringSubmatch(arg)
	if m == nil {
		return arg, 0
	}
	line, err := strconv.Atoi(m[2])
	if err != nil || line <= 0 {
		return arg, 0
	}
	return m[1], line
}

// whySnapshot serves a fresh cached model, else fetches one. Unlike
// loadModel it never spins: it runs inside why's own spinner.
func whySnapshot(ctx context.Context, app *App, client *api.Client, projectID string) (*cache.ModelSnapshot, error) {
	entry := app.cachedModel(projectID)
	if entry.Fresh(modelFreshFor) {
		return &entry.Value, nil
	}
	snap, err := fetchSnapshot(ctx, client, projectID)
	if err != nil {
		if entry != nil && transient(err) {
			return &entry.Value, nil
		}
		return nil, err
	}
	app.saveModel(projectID, snap)
	return snap, nil
}

// whyInput is what gatherWhy needs besides the API.
type whyInput struct {
	ProjectID string
	Path      string
	Line      int
	Snapshot  func(context.Context) (*cache.ModelSnapshot, error)
	// Commit is best effort: a file git does not track has none.
	Commit func(context.Context) (*repo.Commit, error)
}

type whyElement struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Level       string `json:"level"`
	Path        string `json:"path"`
	Match       string `json:"match"`
	Description string `json:"description,omitempty"`
}

type whyDecision struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Date     time.Time `json:"date"`
	Decision string    `json:"decision,omitempty"`
	// LinkedTo is the element of the file's lineage the decision is linked
	// to; empty for a decision only dated near the commit.
	LinkedTo   string `json:"linkedTo,omitempty"`
	NearCommit bool   `json:"nearCommit,omitempty"`
}

type whyRule struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Type     string `json:"type"`
	Reason   string `json:"reason"`
}

type whyReport struct {
	File       string             `json:"file"`
	Line       int                `json:"line,omitempty"`
	Elements   []whyElement       `json:"elements"`
	Owners     []api.Owner        `json:"owners,omitempty"`
	TeamOwners []api.TeamOwner    `json:"teamOwners,omitempty"`
	Decisions  []whyDecision      `json:"decisions,omitempty"`
	Memories   []api.Memory       `json:"memories,omitempty"`
	Changes    []api.HistoryEntry `json:"changes,omitempty"`
	Rules      []whyRule          `json:"rules,omitempty"`
	// ProjectWideRules counts the enabled rules with no scope: they apply to
	// every file.
	ProjectWideRules int          `json:"projectWideRules"`
	Commit           *repo.Commit `json:"commit,omitempty"`
	// AroundCommit are decisions not linked to the file's elements but
	// dated near its commit.
	AroundCommit []whyDecision `json:"aroundCommit,omitempty"`
}

func gatherWhy(ctx context.Context, client *api.Client, in whyInput) (*whyReport, error) {
	var (
		located *api.LocateResult
		snap    *cache.ModelSnapshot
		rules   []api.RuleDetail
		commit  *repo.Commit
		errs    = make([]error, 2)
		wg      sync.WaitGroup
	)
	wg.Go(func() { located, errs[0] = client.Locate(ctx, in.ProjectID, []string{in.Path}, 1) })
	wg.Go(func() { snap, errs[1] = in.Snapshot(ctx) })
	wg.Go(func() { rules, _ = client.Rules(ctx, in.ProjectID) })
	if in.Commit != nil {
		wg.Go(func() { commit, _ = in.Commit(ctx) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	g := graph.New(&snap.Model)
	r := &whyReport{File: in.Path, Line: in.Line, Commit: commit}
	for _, f := range located.Files {
		if f.Path != in.Path {
			continue
		}
		for _, le := range f.Elements {
			if len(r.Elements) == maxWhyElements {
				break
			}
			e := whyElement{ID: le.ID, Name: le.Name, Level: le.Type, Path: le.Name, Match: le.Match}
			if n := g.Node(le.ID); n != nil {
				e.Level, e.Path, e.Description = n.Level, n.PathString(), n.Description
			}
			r.Elements = append(r.Elements, e)
		}
	}
	if len(r.Elements) > 0 && located.Impact != nil {
		for _, t := range located.Impact.Elements {
			if t.ID == r.Elements[0].ID {
				r.Owners, r.TeamOwners = t.Owners, t.TeamOwners
			}
		}
	}

	// The elements and their ancestors: what decisions and rules can aim at.
	lineage := map[string]string{}
	for _, e := range r.Elements {
		lineage[e.ID] = e.Name
		if n := g.Node(e.ID); n != nil {
			for _, a := range n.Path() {
				lineage[a.ID] = a.Name
			}
		}
	}
	r.Decisions, r.AroundCommit = whyDecisions(snap.ADRs, lineage, commit)
	r.Rules, r.ProjectWideRules = whyRules(rules, in.Path, lineage)

	var mu sync.Mutex
	var mwg sync.WaitGroup
	seenMemory := map[string]bool{}
	seenChange := map[string]bool{}
	for _, e := range r.Elements {
		mwg.Go(func() {
			history, err := client.History(ctx, in.ProjectID, e.ID, maxWhyChanges)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, h := range history {
				if !seenChange[h.ID] {
					seenChange[h.ID] = true
					r.Changes = append(r.Changes, h)
				}
			}
		})
		mwg.Go(func() {
			memories, err := client.Recall(ctx, in.ProjectID, api.RecallOptions{Element: e.ID, Limit: 5})
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, m := range memories {
				if !seenMemory[m.ID] {
					seenMemory[m.ID] = true
					r.Memories = append(r.Memories, m)
				}
			}
		})
	}
	mwg.Wait()
	sort.SliceStable(r.Changes, func(i, j int) bool { return r.Changes[i].CreatedAt.After(r.Changes[j].CreatedAt) })
	if len(r.Changes) > maxWhyChanges {
		r.Changes = r.Changes[:maxWhyChanges]
	}
	sort.SliceStable(r.Memories, func(i, j int) bool { return memoryRank(r.Memories[i].Kind) < memoryRank(r.Memories[j].Kind) })
	if len(r.Memories) > maxWhyMemories {
		r.Memories = r.Memories[:maxWhyMemories]
	}
	return r, nil
}

// whyDecisions splits decisions into those linked to the lineage, accepted
// first, and those only dated near the commit.
func whyDecisions(adrs []api.ADR, lineage map[string]string, commit *repo.Commit) (linked, around []whyDecision) {
	near := func(adr api.ADR) bool {
		if commit == nil || commit.Uncommitted || commit.Date.IsZero() || adr.Date.IsZero() {
			return false
		}
		d := adr.Date.Sub(commit.Date)
		return d < commitWindow && d > -commitWindow
	}
	for _, adr := range adrs {
		d := whyDecision{Number: adr.Number, Title: adr.Title, Status: adr.Status, Date: adr.Date,
			Decision: strings.TrimSpace(adr.Decision), NearCommit: near(adr)}
		for _, l := range adr.Links {
			if name, ok := lineage[l.ElementID]; ok {
				d.LinkedTo = name
				break
			}
		}
		switch {
		case d.LinkedTo != "":
			linked = append(linked, d)
		case d.NearCommit:
			around = append(around, d)
		}
	}
	sort.SliceStable(linked, func(i, j int) bool {
		if ri, rj := adrRank(linked[i].Status), adrRank(linked[j].Status); ri != rj {
			return ri < rj
		}
		return linked[i].Number > linked[j].Number
	})
	sort.SliceStable(around, func(i, j int) bool {
		return commitDistance(around[i].Date, commit) < commitDistance(around[j].Date, commit)
	})
	if len(around) > 3 {
		around = around[:3]
	}
	return linked, around
}

func commitDistance(t time.Time, commit *repo.Commit) time.Duration {
	d := t.Sub(commit.Date)
	if d < 0 {
		return -d
	}
	return d
}

// adrRank orders decisions by how binding they are.
func adrRank(status string) int {
	switch strings.ToLower(status) {
	case "accepted":
		return 0
	case "proposed":
		return 1
	case "deprecated", "superseded":
		return 3
	}
	return 2
}

// memoryRank puts what bites first: pitfalls, then conventions.
func memoryRank(kind string) int {
	switch kind {
	case "pitfall":
		return 0
	case "convention":
		return 1
	}
	return 2
}

// whyRules lists the rules scoped to the file, by container or path, and
// counts those with no scope at all.
func whyRules(rules []api.RuleDetail, path string, lineage map[string]string) ([]whyRule, int) {
	var out []whyRule
	projectWide := 0
	for _, rule := range rules {
		reason, scoped := ruleReason(rule.Config, path, lineage)
		switch {
		case reason != "":
			out = append(out, whyRule{Name: rule.Name, Severity: rule.Severity, Type: rule.RuleType, Reason: reason})
		case !scoped:
			projectWide++
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return severityRank(out[i].Severity) > severityRank(out[j].Severity) })
	return out, projectWide
}

func severityRank(severity string) int {
	return map[string]int{"critical": 4, "high": 3, "medium": 2, "low": 1}[strings.ToLower(severity)]
}

// ruleReason says why a rule aims at path, "" when it does not. scoped
// reports whether the rule narrows its target at all. It mirrors the server's
// evaluators: fileGlob and the layers' pathPattern both have to match when
// set, while containerIds only names the container — the evaluators do not
// filter on it, so on its own it leaves the rule project-wide.
func ruleReason(config map[string]any, path string, lineage map[string]string) (reason string, scoped bool) {
	var reasons []string
	for _, id := range stringList(config["containerIds"]) {
		if name, ok := lineage[id]; ok {
			reasons = append(reasons, "scoped to "+name)
			break
		}
	}
	if glob, _ := config["fileGlob"].(string); glob != "" {
		scoped = true
		if !matchFileGlob(glob, path) {
			return "", true
		}
		reasons = append(reasons, "files matching "+glob)
	}
	if layers, ok := config["layers"].([]any); ok && len(layers) > 0 {
		scoped = true
		layer := ""
		for _, l := range layers {
			def, _ := l.(map[string]any)
			pattern, _ := def["pathPattern"].(string)
			name, _ := def["name"].(string)
			if pattern != "" && matchGlobPath(pattern, path) {
				layer = fmt.Sprintf("layer %s (%s)", name, pattern)
				break
			}
		}
		if layer == "" {
			return "", true
		}
		reasons = append(reasons, layer)
	}
	if len(reasons) == 0 {
		return "", scoped
	}
	return strings.Join(reasons, ", "), true
}

func stringList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (r *whyReport) markdown() string {
	var s strings.Builder
	target := r.File
	if r.Line > 0 {
		target += fmt.Sprintf(":%d", r.Line)
	}
	fmt.Fprintf(&s, "# Why `%s`\n\n", target)

	if len(r.Elements) == 0 {
		s.WriteString("Not in the model yet: no element declares this path or reads like its name.\n\n")
	} else {
		s.WriteString("## Belongs to\n\n")
		for _, e := range r.Elements {
			fmt.Fprintf(&s, "- **%s** (%s) — `%s`", e.Name, e.Level, e.Path)
			if e.Match != "path" {
				s.WriteString(", name match")
			}
			s.WriteString("\n")
		}
		var owners []string
		for _, t := range r.TeamOwners {
			owners = append(owners, "@"+firstNonEmpty(t.Slug, t.Name))
		}
		for _, u := range r.Owners {
			owners = append(owners, u.Email)
		}
		if len(owners) > 0 {
			fmt.Fprintf(&s, "\n**Owners:** %s\n", strings.Join(owners, ", "))
		}
		s.WriteString("\n")
	}

	if r.Commit != nil {
		s.WriteString("## Last change\n\n")
		if r.Commit.Uncommitted {
			s.WriteString("Not committed yet.\n\n")
		} else {
			fmt.Fprintf(&s, "`%s` %s — %s, %s\n\n", r.Commit.Short(), r.Commit.Subject, r.Commit.Author, r.Commit.Date.Format("2006-01-02"))
		}
	}

	if len(r.Decisions) > 0 {
		s.WriteString("## Decisions that apply\n\n")
		for _, d := range r.Decisions {
			s.WriteString(decisionMarkdown(d))
		}
		s.WriteString("\n")
	}
	if len(r.AroundCommit) > 0 {
		s.WriteString("## Decided around that commit\n\n")
		for _, d := range r.AroundCommit {
			s.WriteString(decisionMarkdown(d))
		}
		s.WriteString("\n")
	}
	if len(r.Memories) > 0 {
		s.WriteString("## What Archyl remembers\n\n")
		s.WriteString(memoriesMarkdown(r.Memories))
		s.WriteString("\n")
	}
	if len(r.Rules) > 0 || r.ProjectWideRules > 0 {
		s.WriteString("## Rules aimed at it\n\n")
		for _, rule := range r.Rules {
			fmt.Fprintf(&s, "- **%s** (%s) — %s\n", rule.Name, rule.Severity, rule.Reason)
		}
		if r.ProjectWideRules > 0 {
			fmt.Fprintf(&s, "- plus %d project-wide rule(s)\n", r.ProjectWideRules)
		}
		s.WriteString("\n")
	}
	if len(r.Changes) > 0 {
		s.WriteString("## Recent model changes\n\n")
		for _, h := range r.Changes {
			fmt.Fprintf(&s, "- %s %s %s %s\n", h.CreatedAt.Format("2006-01-02"), firstNonEmpty(h.UserName, "someone"),
				firstNonEmpty(historyVerbs[h.Action], h.Action), h.EntityName)
		}
	}
	return strings.TrimRight(s.String(), "\n")
}

var historyVerbs = map[string]string{"create": "created", "update": "changed", "delete": "deleted"}

func decisionMarkdown(d whyDecision) string {
	line := fmt.Sprintf("- ADR-%d %s (%s", d.Number, d.Title, d.Status)
	if !d.Date.IsZero() {
		line += ", " + d.Date.Format("2006-01-02")
	}
	line += ")"
	if d.LinkedTo != "" {
		line += " — on " + d.LinkedTo
	}
	if d.NearCommit && d.LinkedTo != "" {
		line += ", decided around the commit"
	}
	if d.Decision != "" {
		line += ": " + truncate(d.Decision, 200)
	}
	return line + "\n"
}

func renderWhy(p *ui.Printer, r *whyReport) {
	target := r.File
	if r.Line > 0 {
		target += fmt.Sprintf(":%d", r.Line)
	}
	p.Println()
	p.Println(p.Brand() + p.Muted("  why  ") + p.Bold(target))

	p.Println()
	p.Println(p.Heading(" Belongs to"))
	if len(r.Elements) == 0 {
		p.Println("  " + p.Muted("not in the model yet — no element declares this path or reads like its name"))
	}
	for i, e := range r.Elements {
		line := "  " + breadcrumb(p, e.Path)
		if i == 0 {
			line += "  " + matchLabel(p, e.Match)
		}
		p.Println(line)
		if i == 0 && e.Description != "" {
			p.Println("  " + p.Muted(truncate(e.Description, max(p.Width()-6, 40))))
		}
	}
	if len(r.Elements) > 0 {
		p.Println("  " + p.Muted("owners ") + ownersLine(p, r.Owners, r.TeamOwners))
	}

	if c := r.Commit; c != nil {
		p.Println()
		p.Println(p.Heading(" Last change"))
		if c.Uncommitted {
			p.Println("  " + p.Muted("not committed yet"))
		} else {
			p.Println("  " + p.Accent(c.Short()) + " " + c.Subject)
			p.Println("  " + p.Muted(c.Author+" · "+c.Date.Local().Format("Jan 2, 2006")+" · "+ago(c.Date)))
		}
	}

	if len(r.Decisions) > 0 {
		p.Println()
		p.Println(p.Heading(" Decisions"))
		for i, d := range r.Decisions {
			if i == 6 {
				p.Println("  " + p.Muted(fmt.Sprintf("+%d more", len(r.Decisions)-6)) + "  " + p.Hint("all:", "archyl adr"))
				break
			}
			p.Println("  " + decisionLine(p, d))
		}
	}
	if len(r.AroundCommit) > 0 {
		p.Println()
		p.Println(p.Heading(" Decided around that commit"))
		for _, d := range r.AroundCommit {
			p.Println("  " + decisionLine(p, d))
		}
	}

	if len(r.Memories) > 0 {
		p.Println()
		p.Println(p.Heading(" Remembered"))
		for _, m := range r.Memories {
			kind := p.Muted(m.Kind)
			if m.Kind == "pitfall" {
				kind = p.Warning(m.Kind)
			}
			text := firstNonEmpty(strings.TrimSpace(m.Title), strings.TrimSpace(m.Content))
			p.Println("  " + kind + "  " + truncate(text, max(p.Width()-16, 40)))
		}
	}

	if len(r.Rules) > 0 || r.ProjectWideRules > 0 {
		p.Println()
		p.Println(p.Heading(" Rules"))
		for _, rule := range r.Rules {
			p.Println("  " + severityBadge(p, rule.Severity) + " " + rule.Name + "  " + p.Muted(rule.Reason))
		}
		if r.ProjectWideRules > 0 {
			p.Println("  " + p.Muted(fmt.Sprintf("+ %s that apply everywhere", ui.Plural(r.ProjectWideRules, "project-wide rule", "project-wide rules"))))
		}
	}

	if len(r.Changes) > 0 {
		p.Println()
		p.Println(p.Heading(" Model changes"))
		for _, h := range r.Changes {
			p.Println(fmt.Sprintf("  %s  %s %s %s", p.Muted(fmt.Sprintf("%-8s", ago(h.CreatedAt))), p.Bold(firstNonEmpty(h.UserName, "someone")),
				p.Muted(firstNonEmpty(historyVerbs[h.Action], h.Action)), h.EntityName))
		}
	}
	p.Println()
}

func decisionLine(p *ui.Printer, d whyDecision) string {
	status := p.Muted(d.Status)
	if strings.EqualFold(d.Status, "accepted") {
		status = p.Success(d.Status)
	}
	line := p.Accent(fmt.Sprintf("ADR-%d", d.Number)) + " " + d.Title + "  " + status
	if d.NearCommit {
		line += p.Muted(" · ") + p.Warning("around this commit")
	}
	if d.LinkedTo != "" {
		line += p.Muted(" · on " + d.LinkedTo)
	}
	return line
}
