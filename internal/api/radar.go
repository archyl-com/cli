package api

import (
	"context"
	"net/http"
	"net/url"
)

// RadarTechnology is a technology with its governance ring and usage.
type RadarTechnology struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Category          string `json:"category"`
	RadarRing         string `json:"radarRing"`
	RadarNote         string `json:"radarNote"`
	ElementCount      int    `json:"elementCount"`
	RelationshipCount int    `json:"relationshipCount"`
}

// Radar lists the organization's technologies.
func (c *Client) Radar(ctx context.Context) ([]RadarTechnology, error) {
	var out struct {
		Data []RadarTechnology `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/technologies/radar", nil, nil, &out)
	return out.Data, err
}

// Manifest is a dependency manifest sent for checking.
type Manifest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CheckedDependency is a dependency matched against the radar.
type CheckedDependency struct {
	Name       string           `json:"name"`
	Version    string           `json:"version"`
	Ecosystem  string           `json:"ecosystem"`
	FilePath   string           `json:"filePath"`
	IsDev      bool             `json:"isDev"`
	Technology *RadarTechnology `json:"technology"`
}

// Ring is the governance ring of the matched technology, or "".
func (d CheckedDependency) Ring() string {
	if d.Technology == nil {
		return ""
	}
	return d.Technology.RadarRing
}

// DependencyCheck is the result of checking manifests against the radar.
type DependencyCheck struct {
	Dependencies []CheckedDependency `json:"dependencies"`
	Summary      struct {
		Total        int `json:"total"`
		Adopt        int `json:"adopt"`
		Trial        int `json:"trial"`
		Assess       int `json:"assess"`
		Hold         int `json:"hold"`
		Unclassified int `json:"unclassified"`
	} `json:"summary"`
}

// CheckDependencies matches manifests' dependencies against the radar.
func (c *Client) CheckDependencies(ctx context.Context, projectID string, manifests []Manifest) (*DependencyCheck, error) {
	var out DependencyCheck
	body := map[string]any{"manifests": manifests}
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/technologies/check", nil, body, &out)
}
