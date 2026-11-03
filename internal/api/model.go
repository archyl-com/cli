package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Technology is a technology attached to an element.
type Technology struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// CodeElement is a C4 level-4 element.
type CodeElement struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	CodeType     string       `json:"codeType"`
	Language     string       `json:"language"`
	FilePath     string       `json:"filePath"`
	LineStart    int          `json:"lineStart"`
	Signature    string       `json:"signature"`
	Tags         []string     `json:"tags"`
	Technologies []Technology `json:"technologies"`
}

// Component is a C4 level-3 element.
type Component struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	ComponentType string        `json:"componentType"`
	FilePath      string        `json:"filePath"`
	Tags          []string      `json:"tags"`
	Technologies  []Technology  `json:"technologies"`
	CodeElements  []CodeElement `json:"codeElements"`
}

// Container is a C4 level-2 element.
type Container struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description"`
	ContainerType string       `json:"containerType"`
	Tags          []string     `json:"tags"`
	Technologies  []Technology `json:"technologies"`
	Components    []Component  `json:"components"`
}

// System is a C4 level-1 element.
type System struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Type         string       `json:"type"`
	IsExternal   bool         `json:"isExternal"`
	Tags         []string     `json:"tags"`
	Technologies []Technology `json:"technologies"`
	Containers   []Container  `json:"containers"`
}

// Relationship connects two elements.
type Relationship struct {
	ID           string       `json:"id"`
	SourceID     string       `json:"sourceId"`
	TargetID     string       `json:"targetId"`
	Type         string       `json:"type"`
	Description  string       `json:"description"`
	Technologies []Technology `json:"technologies"`
}

// Model is a project's full C4 model.
type Model struct {
	Systems       []System       `json:"systems"`
	Relationships []Relationship `json:"relationships"`
}

// ADRLink ties a decision to an element.
type ADRLink struct {
	ElementID   string `json:"elementId"`
	ElementName string `json:"elementName"`
	ElementType int    `json:"elementType"`
}

// ADR is an Architecture Decision Record.
type ADR struct {
	ID           string    `json:"id"`
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	Context      string    `json:"context"`
	Decision     string    `json:"decision"`
	Consequences string    `json:"consequences"`
	Date         time.Time `json:"date"`
	Tags         []string  `json:"tags"`
	Links        []ADRLink `json:"links"`
}

// SearchResult is one hit of the global search.
type SearchResult struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	SystemID    string `json:"systemId"`
	ContainerID string `json:"containerId"`
	ComponentID string `json:"componentId"`
}

// ChatMessage is one turn of a conversation with the AI assistant.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls are the actions an assistant message asked for, with native
	// tool calling.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolCallID ties a "tool" message, an action's output, to its call.
	ToolCallID string `json:"toolCallId,omitempty"`
}

// Model returns the project's full C4 model.
func (c *Client) Model(ctx context.Context, projectID string) (*Model, error) {
	var out Model
	return &out, c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/c4", nil, nil, &out)
}

// ADRs lists the project's decisions.
func (c *Client) ADRs(ctx context.Context, projectID string) ([]ADR, error) {
	var out struct {
		Data []ADR `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/adrs", nil, nil, &out)
	return out.Data, err
}

// Search runs the global search.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	var out []SearchResult
	q := url.Values{"q": {query}, "limit": {fmt.Sprint(limit)}}
	err := c.do(ctx, http.MethodGet, "/search/", q, nil, &out)
	return out, err
}

// ClientAction is a local command the assistant may ask the CLI to run.
type ClientAction struct {
	Name        string `json:"name"`
	Usage       string `json:"usage,omitempty"`
	Description string `json:"description"`
	Writes      bool   `json:"writes,omitempty"`
}

// Chat streams the assistant's answer, calling onChunk for each piece.
func (c *Client) Chat(ctx context.Context, projectID string, messages []ChatMessage, onChunk func(string)) error {
	return c.ChatWithActions(ctx, projectID, messages, nil, onChunk)
}

// ChatWithActions is Chat with local actions the assistant may ask for, in
// the ACTION text protocol.
func (c *Client) ChatWithActions(ctx context.Context, projectID string, messages []ChatMessage, actions []ClientAction, onChunk func(string)) error {
	return c.ChatStream(ctx, ChatRequest{ProjectID: projectID, Messages: messages, Actions: actions}, func(e ChatEvent) {
		if e.Text != "" {
			onChunk(e.Text)
		}
	})
}

// ADRRequest creates a decision.
type ADRRequest struct {
	Title        string   `json:"title"`
	Context      string   `json:"context,omitempty"`
	Decision     string   `json:"decision,omitempty"`
	Consequences string   `json:"consequences,omitempty"`
	Status       string   `json:"status,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

// CreateADR records a decision.
func (c *Client) CreateADR(ctx context.Context, projectID string, req ADRRequest) (*ADR, error) {
	var out ADR
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/adrs", nil, req, &out)
}

// Person is a user shown next to a change request or review.
type Person struct {
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// Name is the person's name, or their email.
func (p *Person) Name() string {
	if p == nil {
		return ""
	}
	if n := strings.TrimSpace(p.FirstName + " " + p.LastName); n != "" {
		return n
	}
	return p.Email
}

// ChangeRequest is an Architecture Change Request.
type ChangeRequest struct {
	ID            string    `json:"id"`
	Project       string    `json:"projectId"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	Author        *Person   `json:"author"`
	ApprovalCount int       `json:"approvalCount"`
	ChangeCount   int       `json:"changeCount"`
	CommentCount  int       `json:"commentCount"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Changes       []struct {
		Operation   string `json:"operation"`
		ElementType string `json:"elementType"`
		ElementData any    `json:"elementData"`
	} `json:"changes"`
}

// ChangeRequests lists a project's change requests, optionally by status.
func (c *Client) ChangeRequests(ctx context.Context, projectID, status string) ([]ChangeRequest, error) {
	var out struct {
		Data []ChangeRequest `json:"data"`
	}
	q := url.Values{"pageSize": {"50"}}
	if status != "" {
		q.Set("status", status)
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/requests", q, nil, &out)
	return out.Data, err
}

// ChangeRequest returns one change request with its changes.
func (c *Client) ChangeRequest(ctx context.Context, id string) (*ChangeRequest, error) {
	var out ChangeRequest
	return &out, c.do(ctx, http.MethodGet, "/requests/"+url.PathEscape(id), nil, nil, &out)
}

// ReviewChangeRequest submits a review: approve, request_changes or comment.
func (c *Client) ReviewChangeRequest(ctx context.Context, id, decision, body string) error {
	return c.do(ctx, http.MethodPost, "/requests/"+url.PathEscape(id)+"/reviews", nil,
		map[string]string{"decision": decision, "body": body}, nil)
}

// Notification is an inbox entry.
type Notification struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	ResourceType string     `json:"resourceType"`
	ResourceID   string     `json:"resourceId"`
	ProjectID    string     `json:"projectId"`
	ReadAt       *time.Time `json:"readAt"`
	CreatedAt    time.Time  `json:"createdAt"`
	Payload      struct {
		ActorName   string `json:"actorName"`
		Subject     string `json:"subject"`
		Detail      string `json:"detail"`
		ProjectName string `json:"projectName"`
		Number      int    `json:"number"`
	} `json:"payload"`
}

// Inbox lists the user's notifications, newest first.
func (c *Client) Inbox(ctx context.Context, limit int) ([]Notification, int, error) {
	var out struct {
		Data        []Notification `json:"data"`
		UnreadCount int            `json:"unreadCount"`
	}
	err := c.do(ctx, http.MethodGet, "/users/me/inbox", url.Values{"limit": {fmt.Sprint(limit)}}, nil, &out)
	return out.Data, out.UnreadCount, err
}

// MarkAllRead clears the inbox's unread state.
func (c *Client) MarkAllRead(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/users/me/inbox/read-all", nil, nil, nil)
}

// MarkRead marks notifications read.
func (c *Client) MarkRead(ctx context.Context, ids ...string) error {
	return c.do(ctx, http.MethodPost, "/users/me/inbox/read", nil, map[string][]string{"ids": ids}, nil)
}

// ProjectID returns the change request's project, falling back to the
// notification that pointed at it.
func (r *ChangeRequest) ProjectID(n Notification) string {
	return firstNonEmpty(r.Project, n.ProjectID)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ElementOwners lists who owns a system, container or component: people and
// teams. Code elements have no owners of their own.
func (c *Client) ElementOwners(ctx context.Context, level, id string) ([]Owner, []TeamOwner, error) {
	var collection string
	switch level {
	case "system":
		collection = "systems"
	case "container":
		collection = "containers"
	case "component":
		collection = "components"
	default:
		return nil, nil, nil
	}
	var out struct {
		Data []struct {
			User *Owner `json:"user"`
		} `json:"data"`
		TeamOwners []struct {
			Team *TeamOwner `json:"team"`
		} `json:"teamOwners"`
	}
	if err := c.do(ctx, http.MethodGet, "/c4/"+collection+"/"+url.PathEscape(id)+"/owners", nil, nil, &out); err != nil {
		return nil, nil, err
	}
	var owners []Owner
	for _, o := range out.Data {
		if o.User != nil {
			owners = append(owners, *o.User)
		}
	}
	var teams []TeamOwner
	for _, t := range out.TeamOwners {
		if t.Team != nil {
			teams = append(teams, *t.Team)
		}
	}
	return owners, teams, nil
}
