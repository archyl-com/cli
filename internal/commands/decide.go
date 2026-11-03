package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/decide"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/weblink"
)

// decideDraftTimeout bounds how long decide waits for the AI's draft.
const decideDraftTimeout = 2 * time.Minute

// Linking takes one request per element: a few at a time, all within a
// bound.
const (
	decideLinkConcurrency = 4
	decideLinkTimeout     = 30 * time.Second
)

// errNothingToDecide is returned when there is neither a change nor a word
// about what was decided.
var errNothingToDecide = errors.New(`nothing to draft from; say what was decided: archyl decide "<what was decided>"`)

// decideRepo is the git access decide needs, so tests run without a
// repository.
type decideRepo interface {
	DefaultBase(ctx context.Context) string
	Resolves(ctx context.Context, ref string) bool
	Branch(ctx context.Context) string
	Changes(ctx context.Context, base string) ([]repo.Change, error)
	Diff(ctx context.Context, base string) ([]repo.FileDiff, error)
}

// decideDeps are what decide reaches outside the process for, besides the
// API.
type decideDeps struct {
	openRepo func(ctx context.Context) (decideRepo, error)
	editor   decide.Editor
}

type decideOptions struct {
	base  string
	noAI  bool
	print bool
	yes   bool
}

// decideResult is what decide prints with --json.
type decideResult struct {
	Draft        *decide.Draft    `json:"draft"`
	ADR          *api.ADR         `json:"adr,omitempty"`
	URL          string           `json:"url,omitempty"`
	Linked       []decide.Element `json:"linked,omitempty"`
	LinkFailures []linkFailure    `json:"linkFailures,omitempty"`
}

// linkFailure is an element the new decision could not be linked to.
type linkFailure struct {
	Element decide.Element `json:"element"`
	Error   string         `json:"error"`
}

func newDecideCmd(app *App) *cobra.Command {
	return decideCommand(app, decideDeps{
		openRepo: func(ctx context.Context) (decideRepo, error) {
			git, err := app.Repo(ctx)
			if err != nil {
				return nil, err
			}
			return git, nil
		},
		editor: decide.SystemEditor(decide.EditorCommand(os.Getenv)),
	})
}

func decideCommand(app *App, deps decideDeps) *cobra.Command {
	var opts decideOptions
	cmd := &cobra.Command{
		Use:   "decide [what was decided]",
		Short: "Draft a decision record from what you just changed",
		Long: `Write the Architecture Decision Record while the decision is fresh. decide
reads your branch (the commits since --base and the uncommitted changes),
places it in the architecture, and has Archyl's AI draft the record: title,
context, decision, consequences, the alternatives the change shows, and the
touched elements it is about. The draft only uses those facts. Without an
argument, the decision is inferred from the diff.

In a terminal the draft is shown, then created, edited in $VISUAL or $EDITOR,
or dropped. It is recorded as proposed and linked to its elements. Piped, or
with --print, it is printed as Markdown and nothing is created unless --yes
is given.

The draft counts against your AI quota; --no-ai starts from a template
filled with the files and elements instead.

When a link fails, the decision is kept and the missing links are listed.
Without a terminal (--yes in a script, --json) decide then exits 1, so the
script notices.`,
		Example: `  archyl decide "Publish order events through an outbox table"
  archyl decide                  # infer the decision from the diff
  archyl decide --no-ai "Use slog for logging"
  archyl decide --print > decision.md
  archyl decide "Drop the cache" --yes --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDecide(cmd.Context(), app, deps, opts, strings.TrimSpace(strings.Join(args, " ")))
		},
	}
	cmd.Flags().StringVar(&opts.base, "base", "", "branch to compare with (default: origin's default branch)")
	cmd.Flags().BoolVar(&opts.noAI, "no-ai", false, "start from a template filled with the facts instead of an AI draft")
	cmd.Flags().BoolVar(&opts.print, "print", false, "print the draft as Markdown instead of asking; with --yes, record it too")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "record the draft without asking")
	return cmd
}

func runDecide(
	ctx context.Context,
	app *App,
	deps decideDeps,
	opts decideOptions,
	decided string,
) error {
	p := app.Printer
	interactive := p.Interactive() && !opts.print
	if decided == "" && interactive && !opts.yes {
		var err error
		if decided, err = askDecided(opts.noAI); err != nil {
			return err
		}
	}
	if decided == "" && opts.noAI {
		return errNothingToDecide
	}

	project, err := app.Project(ctx)
	if err != nil {
		return err
	}
	client, err := app.Client()
	if err != nil {
		return err
	}
	facts, err := gatherDecision(ctx, p, client, deps, project.ID, opts.base, decided)
	if err != nil {
		return err
	}
	if len(facts.Files) == 0 && decided == "" {
		return errNothingToDecide
	}

	draft := draftDecision(ctx, p, client, project.ID, facts, opts.noAI)
	if d := draft.DuplicateOf; d > 0 {
		p.Eprintln(p.Bang() + " " + p.Muted(fmt.Sprintf("ADR-%d may already record this decision; ", d)) + p.Hint("read it with", fmt.Sprintf("archyl show ADR-%d", d)))
	}
	if n := len(facts.Touched); n > 0 && len(draft.Elements) == 0 {
		p.Eprintln(p.Bang() + " " + p.Muted(fmt.Sprintf(
			"The draft links no element: the change touches %s and none was picked. Name the ones it is about under elements: when you edit it.",
			ui.Plural(n, "element", "elements"))))
	}

	if !interactive {
		if !p.JSON {
			if _, err := fmt.Fprint(p.Out, ui.StripControl(draft.Markdown(facts.Elements()))); err != nil {
				return err
			}
		}
		if !opts.yes {
			if p.JSON {
				return p.WriteJSON(decideResult{Draft: draft})
			}
			return nil
		}
		return recordDecision(ctx, app, client, project.ID, draft, false)
	}

	if opts.yes {
		renderDraft(p, draft)
	} else {
		draft, err = reviewDraft(ctx, p, deps.editor, askRecord, facts, draft)
		if err != nil || draft == nil {
			return err
		}
	}
	return recordDecision(ctx, app, client, project.ID, draft, true)
}

// askDecided asks what was decided. With the AI, the answer may stay empty:
// the draft then infers it from the diff.
func askDecided(required bool) (string, error) {
	var decided string
	description := "Leave it empty to infer it from your change."
	if required {
		description = "A sentence is enough."
	}
	err := huh.NewInput().
		Title("What did you decide?").
		Description(description).
		Placeholder("Publish order events through an outbox table").
		Validate(func(s string) error {
			if required && strings.TrimSpace(s) == "" {
				return errors.New("say what was decided")
			}
			return nil
		}).
		Value(&decided).
		Run()
	return strings.TrimSpace(decided), err
}

// gatherDecision collects the facts a draft draws on: the changed files and
// their diff, where they sit in the architecture, and the decisions already
// recorded. Only the changed files are required; the rest is best effort and
// reported as a warning when missing.
func gatherDecision(
	ctx context.Context,
	p *ui.Printer,
	client *api.Client,
	deps decideDeps,
	projectID string,
	base string,
	decided string,
) (decide.Facts, error) {
	git, err := deps.openRepo(ctx)
	if err != nil {
		return decide.Facts{}, err
	}
	if base == "" {
		base = git.DefaultBase(ctx)
	}
	if base != "" && !git.Resolves(ctx, base) {
		return decide.Facts{}, fmt.Errorf("cannot find %s in this clone; fetch it, or compare with another branch: archyl decide --base <branch>", base)
	}
	changes, err := git.Changes(ctx, base)
	if err != nil {
		return decide.Facts{}, err
	}
	facts := decide.Facts{Decided: decided, Branch: git.Branch(ctx), Base: base, Files: changes}

	var warnings []string
	var mu sync.Mutex
	warn := func(what string, err error) {
		mu.Lock()
		defer mu.Unlock()
		warnings = append(warnings, fmt.Sprintf("%s (%v)", what, err))
	}
	err = p.Spin("Reading your change…", func() error {
		var wg sync.WaitGroup
		if len(changes) > 0 {
			paths := make([]string, 0, len(changes))
			for _, c := range changes[:min(len(changes), maxPaths)] {
				paths = append(paths, c.Path)
			}
			wg.Go(func() {
				impact, err := client.Locate(ctx, projectID, paths, 1)
				if err != nil {
					warn("Not placed in the architecture, so the draft links no element", err)
					return
				}
				facts.Touched = decide.FromImpact(impact)
			})
			wg.Go(func() {
				diffs, err := git.Diff(ctx, base)
				if err != nil {
					warn("No diff for the draft to read", err)
					return
				}
				facts.Diff = decide.TrimDiff(diffs, decide.DiffLimit)
			})
		}
		wg.Go(func() {
			adrs, err := client.ADRs(ctx, projectID)
			if err != nil {
				warn("The recorded decisions could not be read, so duplicates go unnoticed", err)
				return
			}
			facts.ADRs = decide.FromADRs(adrs)
		})
		wg.Wait()
		return nil
	})
	for _, w := range warnings {
		p.Eprintln(p.Bang() + " " + p.Muted(w))
	}
	return facts, err
}

// draftDecision writes the draft with Archyl's AI, or from the template with
// --no-ai or when the AI gives nothing usable.
func draftDecision(
	ctx context.Context,
	p *ui.Printer,
	client *api.Client,
	projectID string,
	facts decide.Facts,
	noAI bool,
) *decide.Draft {
	if noAI {
		return decide.Template(facts)
	}
	var draft *decide.Draft
	var err error
	_ = p.Spin("Drafting the decision…", func() error {
		draft, err = draftWithAI(ctx, client, projectID, facts)
		return nil
	})
	if err != nil {
		p.Eprintln(p.Bang() + " " + p.Muted("No AI draft this time ("+err.Error()+"); starting from the template. --no-ai skips this step."))
		return decide.Template(facts)
	}
	return draft
}

// draftWithAI asks Archyl's AI for the draft.
func draftWithAI(
	ctx context.Context,
	client *api.Client,
	projectID string,
	facts decide.Facts,
) (*decide.Draft, error) {
	ctx, cancel := context.WithTimeout(ctx, decideDraftTimeout)
	defer cancel()
	var reply strings.Builder
	messages := []api.ChatMessage{{Role: "user", Content: decide.Prompt(facts)}}
	if err := client.Chat(ctx, projectID, messages, func(chunk string) { reply.WriteString(chunk) }); err != nil {
		return nil, err
	}
	return decide.ParseReply(reply.String(), facts)
}

// The choices offered on a draft.
const (
	choiceCreate = "create"
	choiceEdit   = "edit"
	choiceCancel = "cancel"
)

// askRecord asks what to do with a draft.
func askRecord(d *decide.Draft) (string, error) {
	choice := choiceCreate
	err := huh.NewSelect[string]().
		Title("Record this decision?").
		Options(
			huh.NewOption("Create it in Archyl, "+d.Status, choiceCreate),
			huh.NewOption("Edit it in your editor", choiceEdit),
			huh.NewOption("Cancel", choiceCancel),
		).
		Value(&choice).
		Run()
	return choice, err
}

// reviewDraft shows the draft and lets the person record it, edit it or drop
// it. It returns nil when they drop it. A file that does not read back is
// reopened as they left it, so nothing they wrote is lost.
func reviewDraft(
	ctx context.Context,
	p *ui.Printer,
	editor decide.Editor,
	choose func(*decide.Draft) (string, error),
	facts decide.Facts,
	draft *decide.Draft,
) (*decide.Draft, error) {
	var text string
	show := true
	for {
		if show {
			renderDraft(p, draft)
		}
		choice, err := choose(draft)
		if err != nil {
			return nil, err
		}
		switch choice {
		case choiceCreate:
			return draft, nil
		case choiceCancel:
			p.Println(p.Muted("Cancelled; nothing recorded."))
			return nil, nil
		}

		if text == "" {
			text = draft.EditorFile(facts.Elements())
		}
		edited, err := decide.Edit(ctx, editor, text)
		if err != nil {
			p.Eprintln(p.Bang() + " " + p.Muted(capitalize(err.Error())+"; the draft is unchanged."))
			show = false
			continue
		}
		text = edited
		next, unknown, err := decide.Parse(edited, facts)
		switch {
		case errors.Is(err, decide.ErrEmpty):
			p.Println(p.Muted("Cancelled: the file was empty; nothing recorded."))
			return nil, nil
		case err != nil:
			p.Eprintln(p.Cross() + " " + capitalize(err.Error()) + p.Muted(". Edit it again to fix it; your text is kept."))
			show = false
			continue
		}
		for _, ref := range unknown {
			p.Eprintln(p.Bang() + " " + p.Muted(fmt.Sprintf("%q is not one of the touched elements, so it will not be linked.", ui.StripControl(ref))))
		}
		draft, show = next, true
	}
}

// renderDraft prints the draft for reading in the terminal. Its text comes
// from the AI, the model or an editor, so it is stripped of control
// characters first.
func renderDraft(p *ui.Printer, d *decide.Draft) {
	p.Println()
	p.Println(p.Brand() + p.Muted("  decision  ") + p.Bold(ui.StripControl(d.Title)))
	meta := p.Accent(ui.StripControl(d.Status))
	if len(d.Elements) > 0 {
		meta += p.Muted(" · links ") + elementNames(p, d.Elements)
	}
	if d.Supersedes > 0 {
		meta += p.Muted(fmt.Sprintf(" · supersedes ADR-%d", d.Supersedes))
	}
	p.Println("  " + meta)
	p.Println()
	p.Println(tui.Markdown(ui.StripControl(d.Body()), p.Width(), p.Dark()))
	p.Println()
}

// elementNames lists elements by level and name, stripped of control
// characters.
func elementNames(p *ui.Printer, elements []decide.Element) string {
	names := make([]string, 0, len(elements))
	for _, e := range elements {
		names = append(names, p.Element(e.Type, ui.StripControl(e.Name)))
	}
	return strings.Join(names, p.Muted(", "))
}

// recordDecision creates the decision and links it to its elements. A link
// that fails is reported with where to add it: the decision itself stays.
// Without a person at the terminal, a failed link then exits 1.
func recordDecision(
	ctx context.Context,
	app *App,
	client *api.Client,
	projectID string,
	draft *decide.Draft,
	interactive bool,
) error {
	p := app.Printer
	if strings.TrimSpace(draft.Title) == "" {
		return fmt.Errorf("%w; say what was decided: archyl decide \"<what was decided>\"", decide.ErrNoTitle)
	}
	result := decideResult{Draft: draft}
	err := p.Spin("Recording the decision…", func() error {
		adr, err := client.CreateADR(ctx, projectID, draft.Request())
		if err != nil {
			return err
		}
		result.ADR = adr
		result.Linked, result.LinkFailures = linkDecision(ctx, client, adr.ID, draft.Elements)
		return nil
	})
	if err != nil {
		return fmt.Errorf("record the decision: %w", err)
	}
	app.InvalidateModel(projectID)
	result.URL = weblink.ADR(app.appURL(), projectID, result.ADR.ID)

	if p.JSON {
		if err := p.WriteJSON(result); err != nil {
			return err
		}
	} else {
		renderRecorded(p, result)
	}
	if len(result.LinkFailures) > 0 && !interactive {
		return &ExitError{Code: 1}
	}
	return nil
}

// linkDecision links the decision to each element, a few at a time, and
// returns the links made and the ones that failed, in the elements' order.
func linkDecision(
	ctx context.Context,
	client *api.Client,
	adrID string,
	elements []decide.Element,
) ([]decide.Element, []linkFailure) {
	ctx, cancel := context.WithTimeout(ctx, decideLinkTimeout)
	defer cancel()
	errs := make([]error, len(elements))
	slots := make(chan struct{}, decideLinkConcurrency)
	var wg sync.WaitGroup
	for i, e := range elements {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			errs[i] = client.LinkADR(ctx, adrID, e.ID, e.Type)
		})
	}
	wg.Wait()

	var linked []decide.Element
	var failures []linkFailure
	for i, e := range elements {
		if errs[i] != nil {
			failures = append(failures, linkFailure{Element: e, Error: errs[i].Error()})
			continue
		}
		linked = append(linked, e)
	}
	return linked, failures
}

func renderRecorded(p *ui.Printer, r decideResult) {
	number := fmt.Sprintf("ADR-%d", r.ADR.Number)
	p.Println(p.Check() + " Recorded " + p.Accent(number) + " " + p.Bold(ui.StripControl(r.ADR.Title)) + p.Muted(" · "+ui.StripControl(r.ADR.Status)))
	if len(r.Linked) > 0 {
		p.Println("  " + p.Muted("linked to ") + elementNames(p, r.Linked))
	}
	for _, f := range r.LinkFailures {
		p.Println("  " + p.Cross() + " " + p.Muted("not linked to ") + elementNames(p, []decide.Element{f.Element}) + p.Muted(": "+ui.StripControl(f.Error)))
	}
	if r.URL != "" {
		p.Println("  " + p.Muted(r.URL))
	}
	if len(r.LinkFailures) > 0 {
		p.Println(p.Hint("Add the missing links on the decision in the web app:", r.URL))
		return
	}
	p.Println(p.Hint("Read it back:", "archyl show "+number))
}
