package api

import (
	"context"
	"net/http"
	"net/url"
)

// Team is a team of an organization; projects belong to one.
type Team struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Role           string `json:"role"`
}

// CanEdit reports whether the caller may create projects in the team.
func (t Team) CanEdit() bool {
	return t.Role == "" || t.Role == "admin" || t.Role == "editor"
}

// CreateProjectRequest is a new project. Slug is unique across Archyl.
type CreateProjectRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description,omitempty"`
	TeamID      string `json:"teamId,omitempty"`
}

// LinkRepositoryRequest connects a git repository to a project.
type LinkRepositoryRequest struct {
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	FullName      string `json:"fullName"`
	URL           string `json:"url"`
	CloneURL      string `json:"cloneUrl,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

// Teams lists the organization's teams the caller can see.
func (c *Client) Teams(ctx context.Context, organizationID string) ([]Team, error) {
	var out struct {
		Data []Team `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/organizations/"+url.PathEscape(organizationID)+"/teams", nil, nil, &out)
	return out.Data, err
}

// CreateProject creates a project.
func (c *Client) CreateProject(ctx context.Context, req CreateProjectRequest) (*Project, error) {
	var out Project
	return &out, c.do(ctx, http.MethodPost, "/projects/", nil, req, &out)
}

// LinkRepository connects a repository to a project, replacing the one
// already connected.
func (c *Client) LinkRepository(ctx context.Context, projectID string, req LinkRepositoryRequest) error {
	return c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/repository", nil, req, nil)
}
