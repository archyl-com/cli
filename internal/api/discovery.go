package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// DiscoveryJob is an AI discovery running on one repository.
type DiscoveryJob struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id,omitempty"`
	Status      string     `json:"status"`
	Progress    int        `json:"progress"`
	CurrentStep string     `json:"current_step"`
	StartedAt   time.Time  `json:"started_at,omitzero"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	Repository  string     `json:"repository,omitempty"`
	Branch      string     `json:"branch,omitempty"`
}

// Done reports whether the job has finished, either way.
func (j *DiscoveryJob) Done() bool {
	return j.Status == "completed" || j.Status == "failed"
}

// PendingDiscovery is an element a discovery proposes, waiting for review.
type PendingDiscovery struct {
	ID             string          `json:"id"`
	DiscoveryJobID string          `json:"discoveryJobId"`
	ElementType    string          `json:"elementType"`
	ElementData    json.RawMessage `json:"elementData"`
	Status         string          `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
}

// PendingElement is the union of the fields the element types carry.
type PendingElement struct {
	Name             string `json:"name"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	Type             string `json:"type"`
	Technology       string `json:"technology"`
	SystemName       string `json:"systemName"`
	ContainerName    string `json:"containerName"`
	ComponentName    string `json:"componentName"`
	Language         string `json:"language"`
	FilePath         string `json:"filePath"`
	Signature        string `json:"signature"`
	SourceName       string `json:"sourceName"`
	TargetName       string `json:"targetName"`
	RelationshipType string `json:"relationshipType"`
	Status           string `json:"status"`
	Context          string `json:"context"`
	Decision         string `json:"decision"`
	Content          string `json:"content"`
}

// Element decodes the element data, tolerating shapes it does not know.
func (p PendingDiscovery) Element() PendingElement {
	var e PendingElement
	_ = json.Unmarshal(p.ElementData, &e)
	return e
}

// StartDiscovery starts an AI discovery on one of the project's linked
// repositories (by full name or ID; empty when the project has one).
func (c *Client) StartDiscovery(ctx context.Context, projectID, repository, branch string) (*DiscoveryJob, error) {
	var out struct {
		JobID       string `json:"job_id"`
		Status      string `json:"status"`
		Progress    int    `json:"progress"`
		CurrentStep string `json:"current_step"`
		Repository  string `json:"repository"`
		Branch      string `json:"branch"`
	}
	body := map[string]string{"repository": repository, "branch": branch}
	if err := c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/discovery", nil, body, &out); err != nil {
		return nil, err
	}
	return &DiscoveryJob{
		ID: out.JobID, ProjectID: projectID, Status: out.Status, Progress: out.Progress,
		CurrentStep: out.CurrentStep, Repository: out.Repository, Branch: out.Branch,
	}, nil
}

// DiscoveryStatus returns a discovery job's progress.
func (c *Client) DiscoveryStatus(ctx context.Context, jobID string) (*DiscoveryJob, error) {
	var out DiscoveryJob
	return &out, c.do(ctx, http.MethodGet, "/discovery/jobs/"+url.PathEscape(jobID), nil, nil, &out)
}

// PendingDiscoveries lists what discoveries propose, waiting for review.
func (c *Client) PendingDiscoveries(ctx context.Context, projectID string) ([]PendingDiscovery, error) {
	var out struct {
		Data []PendingDiscovery `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/pending-discoveries", nil, nil, &out)
	return out.Data, err
}

// ApproveDiscoveries adds pending elements to the model.
func (c *Client) ApproveDiscoveries(ctx context.Context, projectID string, ids []string) error {
	body := map[string][]string{"elementIds": ids}
	return c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/pending-discoveries/approve", nil, body, nil)
}

// RejectDiscoveries discards pending elements.
func (c *Client) RejectDiscoveries(ctx context.Context, projectID string, ids []string) error {
	body := map[string][]string{"elementIds": ids}
	return c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/pending-discoveries/reject", nil, body, nil)
}
