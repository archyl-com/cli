package lsp

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/weblink"
)

// Backend is what the server asks Archyl. Paths are relative to the
// repository root, with forward slashes.
type Backend interface {
	// Check runs the conformance rules on one file's content and returns
	// the violations found in it, with the rule names by ID.
	Check(ctx context.Context, path, content string) ([]api.Violation, map[string]string, error)
	// Brief describes the element a file belongs to; nil when none does.
	Brief(ctx context.Context, path string) (*Brief, error)
	// Ask puts a question to the Archyl assistant and returns its answer.
	Ask(ctx context.Context, question string) (string, error)
	// ProjectURL links to the project in the web app.
	ProjectURL() string
}

// Brief is what hovering a file shows: its element and what bears on it.
type Brief struct {
	ElementID   string
	Name        string
	Level       string
	Path        string
	Description string
	Owners      []string
	Dependents  int
	Decisions   []api.ADR
	URL         string
}

// modelTTL is how long the model and decisions are reused between hovers.
const modelTTL = 10 * time.Minute

// APIBackend answers through the Archyl API.
type APIBackend struct {
	Client    *api.Client
	ProjectID string
	Branch    string
	// AppURL is the web app base URL, for links.
	AppURL string

	mu      sync.Mutex
	g       *graph.Graph
	adrs    []api.ADR
	fetched time.Time
}

// Check implements Backend.
func (b *APIBackend) Check(ctx context.Context, path, content string) ([]api.Violation, map[string]string, error) {
	if repo.SecretPath(path) {
		return nil, nil, nil
	}
	result, err := b.Client.CheckConformance(ctx, b.ProjectID, api.ConformanceCheckRequest{
		Branch:       b.Branch,
		ChangedFiles: []api.ChangedFile{{Path: path, Status: "modified"}},
		FileContents: map[string]string{path: content},
	})
	if err != nil {
		return nil, nil, err
	}
	var mine []api.Violation
	for _, v := range result.Violations {
		if v.FilePath == path {
			mine = append(mine, v)
		}
	}
	return mine, result.RuleNames(), nil
}

// Brief implements Backend.
func (b *APIBackend) Brief(ctx context.Context, path string) (*Brief, error) {
	located, err := b.Client.Locate(ctx, b.ProjectID, []string{path}, 3)
	if err != nil {
		return nil, err
	}
	if len(located.Files) == 0 || len(located.Files[0].Elements) == 0 {
		return nil, nil
	}
	primary := located.Files[0].Elements[0]
	brief := &Brief{ElementID: primary.ID, Name: primary.Name, Level: primary.Type, URL: b.ProjectURL()}
	if located.Impact != nil {
		for _, touched := range located.Impact.Elements {
			if touched.ID != primary.ID {
				continue
			}
			brief.Path = touched.Path
			brief.Dependents = len(touched.Dependents)
			for _, o := range touched.TeamOwners {
				brief.Owners = append(brief.Owners, "@"+firstNonEmpty(o.Slug, o.Name))
			}
			for _, o := range touched.Owners {
				brief.Owners = append(brief.Owners, o.Email)
			}
		}
	}

	// The model adds the description, the deep link and the decisions
	// linked to the element or its ancestors; the hover stands without it.
	g, adrs := b.model(ctx)
	if g == nil {
		return brief, nil
	}
	n := g.Node(primary.ID)
	if n == nil {
		return brief, nil
	}
	brief.Level, brief.Description = n.Level, n.Description
	brief.Path = firstNonEmpty(brief.Path, n.PathString())
	if b.AppURL != "" {
		brief.URL = weblink.Element(b.AppURL, b.ProjectID, n)
	}
	lineage := map[string]bool{}
	for _, a := range n.Path() {
		lineage[a.ID] = true
	}
	for _, adr := range adrs {
		for _, l := range adr.Links {
			if lineage[l.ElementID] {
				brief.Decisions = append(brief.Decisions, adr)
				break
			}
		}
	}
	return brief, nil
}

func (b *APIBackend) model(ctx context.Context) (*graph.Graph, []api.ADR) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.g != nil && time.Since(b.fetched) < modelTTL {
		return b.g, b.adrs
	}
	model, err := b.Client.Model(ctx, b.ProjectID)
	if err != nil {
		return b.g, b.adrs
	}
	adrs, _ := b.Client.ADRs(ctx, b.ProjectID)
	b.g, b.adrs, b.fetched = graph.New(model), adrs, time.Now()
	return b.g, b.adrs
}

// Ask implements Backend.
func (b *APIBackend) Ask(ctx context.Context, question string) (string, error) {
	var answer strings.Builder
	err := b.Client.ChatStream(ctx, api.ChatRequest{
		ProjectID: b.ProjectID,
		Messages:  []api.ChatMessage{{Role: "user", Content: question}},
	}, func(e api.ChatEvent) { answer.WriteString(e.Text) })
	return strings.TrimSpace(answer.String()), err
}

// ProjectURL implements Backend.
func (b *APIBackend) ProjectURL() string {
	if b.AppURL == "" {
		return ""
	}
	return weblink.Project(b.AppURL, b.ProjectID)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
