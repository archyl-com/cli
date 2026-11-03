package api

import (
	"context"
	"net/http"
)

// OwnedElement is a system, container or component with its owners.
type OwnedElement struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	ProjectID  string      `json:"projectId,omitempty"`
	Owners     []Owner     `json:"owners"`
	TeamOwners []TeamOwner `json:"teamOwners"`
}

// OwnershipMap lists every system, container and component of the
// organization with its owners, in one call.
func (c *Client) OwnershipMap(ctx context.Context) ([]OwnedElement, error) {
	var out struct {
		Elements []OwnedElement `json:"elements"`
	}
	if err := c.do(ctx, http.MethodGet, "/global/ownership-map", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Elements, nil
}
