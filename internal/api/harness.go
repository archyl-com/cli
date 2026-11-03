package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var uuidLike = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// PlanStep is one step of a work plan.
type PlanStep struct {
	Order   int    `json:"order"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Element string `json:"element"`
}

// WorkPlan is an architecture-aware implementation plan.
type WorkPlan struct {
	Task           string           `json:"task"`
	Source         string           `json:"source"`
	Steps          []PlanStep       `json:"steps"`
	Constraints    []string         `json:"constraints"`
	Considerations []string         `json:"considerations"`
	Risks          []string         `json:"risks"`
	Context        *RelevantContext `json:"context"`
	Markdown       string           `json:"markdown"`
}

// Plan asks for an implementation plan grounded in the architecture.
func (c *Client) Plan(ctx context.Context, projectID, task string) (*WorkPlan, error) {
	var out WorkPlan
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/harness/plan", nil,
		map[string]string{"task": task}, &out)
}

// Memory is a fact the harness remembers about the architecture.
type Memory struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Title         string    `json:"title"`
	Content       string    `json:"content"`
	ElementName   string    `json:"elementName"`
	ElementType   string    `json:"elementType"`
	AgentName     string    `json:"agentName"`
	Confirmations int       `json:"confirmations"`
	CreatedAt     time.Time `json:"createdAt"`
}

// RecallOptions narrows a recall.
type RecallOptions struct {
	Query   string
	Element string
	Kind    string
	Limit   int
}

// Recall finds memories by meaning, element or kind.
func (c *Client) Recall(ctx context.Context, projectID string, opts RecallOptions) ([]Memory, error) {
	var out struct {
		Memories []Memory `json:"memories"`
	}
	q := url.Values{}
	if opts.Query != "" {
		q.Set("q", opts.Query)
	}
	if opts.Element != "" {
		// A text recall filters by "element" (name or ID); the plain listing
		// only reads "elementId". Send both so either path filters.
		q.Set("element", opts.Element)
		if uuidLike.MatchString(opts.Element) {
			q.Set("elementId", opts.Element)
		}
	}
	if opts.Kind != "" {
		q.Set("kind", opts.Kind)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprint(opts.Limit))
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/harness/memories", q, nil, &out)
	return out.Memories, err
}

// RememberRequest records a memory.
type RememberRequest struct {
	Title      string `json:"title,omitempty"`
	Content    string `json:"content"`
	Element    string `json:"element,omitempty"`
	Kind       string `json:"kind,omitempty"`
	AgentName  string `json:"agentName,omitempty"`
	Supersedes string `json:"supersedes,omitempty"`
}

// RememberResult reports the stored memory, or the one it duplicated.
type RememberResult struct {
	Memory       *Memory `json:"memory"`
	Deduplicated bool    `json:"deduplicated"`
	SimilarTo    []struct {
		Title string `json:"title"`
	} `json:"similarTo"`
}

// Remember records a memory.
func (c *Client) Remember(ctx context.Context, projectID string, req RememberRequest) (*RememberResult, error) {
	var out RememberResult
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/harness/memories", nil, req, &out)
}
