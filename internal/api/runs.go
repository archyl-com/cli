package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Run is a managed agent run.
type Run struct {
	ID                string     `json:"id"`
	ProjectID         string     `json:"projectId"`
	ProfileID         string     `json:"profileId"`
	TaskSource        string     `json:"taskSource"`
	TaskText          string     `json:"taskText"`
	Status            string     `json:"status"`
	PRURL             string     `json:"prUrl"`
	ErrorMessage      string     `json:"errorMessage"`
	TotalInputTokens  int64      `json:"totalInputTokens"`
	TotalOutputTokens int64      `json:"totalOutputTokens"`
	TotalCostCents    int        `json:"totalCostCents"`
	StartedAt         *time.Time `json:"startedAt"`
	EndedAt           *time.Time `json:"endedAt"`
	WaitingSince      *time.Time `json:"waitingSince"`
	ParentRunID       *string    `json:"parentRunId"`
	CreatedAt         time.Time  `json:"createdAt"`
}

// Ended reports whether the run is over.
func (r *Run) Ended() bool {
	switch r.Status {
	case "succeeded", "failed", "cancelled":
		return true
	}
	return false
}

// RunEvent is one entry of a run's event stream.
type RunEvent struct {
	Seq     int64          `json:"seq"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

// Profile is a managed agent profile.
type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model"`
}

// Runs lists the project's runs, newest first.
func (c *Client) Runs(ctx context.Context, projectID, status string, pageSize int) ([]Run, error) {
	var out struct {
		Data []Run `json:"data"`
	}
	q := url.Values{"projectId": {projectID}, "pageSize": {fmt.Sprint(pageSize)}}
	if status != "" {
		q.Set("status", status)
	}
	err := c.do(ctx, http.MethodGet, "/agents/runs", q, nil, &out)
	return out.Data, err
}

// Run returns one run.
func (c *Client) Run(ctx context.Context, id string) (*Run, error) {
	var out Run
	return &out, c.do(ctx, http.MethodGet, "/agents/runs/"+url.PathEscape(id), nil, nil, &out)
}

// RunEvents returns the events after since (-1 for all).
func (c *Client) RunEvents(ctx context.Context, id string, since int64) ([]RunEvent, error) {
	var out struct {
		Data []RunEvent `json:"data"`
	}
	q := url.Values{"since": {fmt.Sprint(since)}, "limit": {"500"}}
	err := c.do(ctx, http.MethodGet, "/agents/runs/"+url.PathEscape(id)+"/events", q, nil, &out)
	return out.Data, err
}

// Profiles lists the organization's agent profiles.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var out struct {
		Data []Profile `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/agents/profiles", nil, nil, &out)
	return out.Data, err
}

// StartRun queues a run of a profile on a task.
func (c *Client) StartRun(ctx context.Context, projectID, profileID, task string) (*Run, error) {
	var out Run
	body := map[string]any{"profileId": profileID, "taskSource": "manual", "taskText": task}
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/agents/runs", nil, body, &out)
}

// ApproveRun starts a run its preflight gate held for approval.
func (c *Client) ApproveRun(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/agents/runs/"+url.PathEscape(id)+"/approve", nil, nil, nil)
}

// RunResponse answers an agent waiting on a human: a question's answer, or a
// plan review (approved, edited steps, or feedback asking for changes).
type RunResponse struct {
	RequestID string      `json:"requestId"`
	Answer    string      `json:"answer,omitempty"`
	Approved  *bool       `json:"approved,omitempty"`
	Steps     []PlanInput `json:"steps,omitempty"`
	Feedback  string      `json:"feedback,omitempty"`
}

// PlanInput is a plan step as a human edits it.
type PlanInput struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// RespondToRun answers a question or reviews a plan.
func (c *Client) RespondToRun(ctx context.Context, id string, resp RunResponse) error {
	return c.do(ctx, http.MethodPost, "/agents/runs/"+url.PathEscape(id)+"/respond", nil, resp, nil)
}

// SteerRun sends an instruction to a running agent.
func (c *Client) SteerRun(ctx context.Context, id, message string) error {
	return c.do(ctx, http.MethodPost, "/agents/runs/"+url.PathEscape(id)+"/steer", nil, map[string]string{"message": message}, nil)
}

// CancelRun stops a run.
func (c *Client) CancelRun(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/agents/runs/"+url.PathEscape(id)+"/cancel", nil, nil, nil)
}

// ContinueRun starts a run that picks up an ended run's work on its branch.
func (c *Client) ContinueRun(ctx context.Context, id, instructions string) (*Run, error) {
	var out Run
	return &out, c.do(ctx, http.MethodPost, "/agents/runs/"+url.PathEscape(id)+"/continue", nil,
		map[string]string{"instructions": instructions}, &out)
}

// HistoryEntry is one change in the project's history.
type HistoryEntry struct {
	ID          string    `json:"id"`
	ProjectName string    `json:"projectName"`
	UserName    string    `json:"userName"`
	Action      string    `json:"action"`
	EntityType  string    `json:"entityType"`
	EntityID    string    `json:"entityId"`
	EntityName  string    `json:"entityName"`
	Changes     string    `json:"changes"`
	CreatedAt   time.Time `json:"createdAt"`
}

// History lists the project's latest changes, newest first.
func (c *Client) History(ctx context.Context, projectID, entityID string, pageSize int) ([]HistoryEntry, error) {
	var out struct {
		Data []HistoryEntry `json:"data"`
	}
	q := url.Values{"projectId": {projectID}, "pageSize": {fmt.Sprint(pageSize)}}
	if entityID != "" {
		q.Set("entityId", entityID)
	}
	err := c.do(ctx, http.MethodGet, "/history", q, nil, &out)
	return out.Data, err
}
