package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ReleaseEnvironment is where a release went.
type ReleaseEnvironment struct {
	Name         string `json:"name"`
	IsProduction bool   `json:"isProduction"`
}

// ReleaseDetail is a release as the project's listing returns it.
type ReleaseDetail struct {
	ID          string              `json:"id"`
	Version     string              `json:"version"`
	Status      string              `json:"status"`
	Source      string              `json:"source"`
	SourceURL   string              `json:"sourceUrl"`
	SystemID    string              `json:"systemId,omitempty"`
	ContainerID string              `json:"containerId,omitempty"`
	ReleasedAt  *time.Time          `json:"releasedAt,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
	Environment *ReleaseEnvironment `json:"environment,omitempty"`
}

// When is the time the release went out, or when it was recorded when it
// has not gone out yet.
func (r ReleaseDetail) When() time.Time {
	if r.ReleasedAt != nil && !r.ReleasedAt.IsZero() {
		return *r.ReleasedAt
	}
	return r.CreatedAt
}

// ReleaseFilter narrows a release listing to a system or a container.
type ReleaseFilter struct {
	SystemID    string
	ContainerID string
	Limit       int
}

// Releases lists a project's releases, latest first.
func (c *Client) Releases(ctx context.Context, projectID string, filter ReleaseFilter) ([]ReleaseDetail, error) {
	var out struct {
		Data []ReleaseDetail `json:"data"`
	}
	q := url.Values{}
	if filter.SystemID != "" {
		q.Set("systemId", filter.SystemID)
	}
	if filter.ContainerID != "" {
		q.Set("containerId", filter.ContainerID)
	}
	if filter.Limit > 0 {
		q.Set("pageSize", fmt.Sprint(filter.Limit))
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/releases", q, nil, &out)
	return out.Data, err
}

// Doc is a documentation page, without its content.
type Doc struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	FilePath  string    `json:"filePath,omitempty"`
	Tags      []string  `json:"tags"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ElementDocs lists the documentation linked to an element of any level.
func (c *Client) ElementDocs(ctx context.Context, level, id string) ([]Doc, error) {
	var out struct {
		Data []Doc `json:"data"`
	}
	q := url.Values{"elementId": {id}, "elementType": {level}}
	err := c.do(ctx, http.MethodGet, "/docs/", q, nil, &out)
	return out.Data, err
}
