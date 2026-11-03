package api

import (
	"context"
	"net/http"
	"net/url"
)

// RuleDetail is a conformance rule with its scope: the project it belongs to
// (none for organization-wide rules) and its type-specific configuration.
type RuleDetail struct {
	Rule
	ProjectID string         `json:"projectId,omitempty"`
	Enabled   bool           `json:"enabled"`
	Config    map[string]any `json:"config"`
}

// Rules lists the enabled conformance rules that apply to a project: its own
// and the organization's.
func (c *Client) Rules(ctx context.Context, projectID string) ([]RuleDetail, error) {
	var out struct {
		Data []RuleDetail `json:"data"`
	}
	q := url.Values{"projectId": {projectID}, "enabledOnly": {"true"}}
	err := c.do(ctx, http.MethodGet, "/conformance/rules", q, nil, &out)
	return out.Data, err
}
