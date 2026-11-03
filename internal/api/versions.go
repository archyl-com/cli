package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// versionsPageSize is the most versions the API returns per page.
const versionsPageSize = 100

// maxVersionPages bounds how many pages Versions walks.
const maxVersionPages = 20

// Version is a recorded state of a project's model. Archyl records one when
// the model is edited; edits by the same person within a few minutes are
// merged into one version.
type Version struct {
	Number      int       `json:"number"`
	Description string    `json:"description,omitempty"`
	Author      string    `json:"userName,omitempty"`
	EntityCount int       `json:"entityCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	// Snapshot is the model as recorded, flat: systems, containers,
	// components and code elements reference their parent by ID. Listings
	// leave it out.
	Snapshot json.RawMessage `json:"snapshotData,omitempty"`
}

// Versions lists a project's versions, oldest first, without their
// snapshots.
func (c *Client) Versions(ctx context.Context, projectID string) ([]Version, error) {
	var all []Version
	for page := 1; page <= maxVersionPages; page++ {
		var out struct {
			Data  []Version `json:"data"`
			Pages int       `json:"pages"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(versionsPageSize)}}
		if err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/versions", q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if page >= out.Pages || len(out.Data) == 0 {
			break
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Number < all[j].Number })
	return all, nil
}

// Version returns one version with its snapshot.
func (c *Client) Version(
	ctx context.Context,
	projectID string,
	number int,
) (*Version, error) {
	var out Version
	path := "/projects/" + url.PathEscape(projectID) + "/versions/" + strconv.Itoa(number)
	err := c.do(ctx, http.MethodGet, path, nil, nil, &out)
	return &out, err
}

// snapshotModel is the flat shape of a version's snapshot.
type snapshotModel struct {
	Systems    []System `json:"systems"`
	Containers []struct {
		Container
		SystemID string `json:"systemId"`
	} `json:"containers"`
	Components []struct {
		Component
		ContainerID string `json:"containerId"`
	} `json:"components"`
	CodeElements []struct {
		CodeElement
		ComponentID string `json:"componentId"`
	} `json:"codeElements"`
	Relationships []Relationship `json:"relationships"`
}

// Model nests the version's snapshot into a model. Elements whose parent the
// snapshot lacks are left out. Snapshots record no technologies.
func (v *Version) Model() (*Model, error) {
	if len(v.Snapshot) == 0 || string(v.Snapshot) == "null" {
		return nil, fmt.Errorf("version %d came without its snapshot", v.Number)
	}
	var flat snapshotModel
	if err := json.Unmarshal(v.Snapshot, &flat); err != nil {
		return nil, fmt.Errorf("read the snapshot of version %d: %w", v.Number, err)
	}

	codes := map[string][]CodeElement{}
	for _, code := range flat.CodeElements {
		codes[code.ComponentID] = append(codes[code.ComponentID], code.CodeElement)
	}
	components := map[string][]Component{}
	for _, cp := range flat.Components {
		comp := cp.Component
		comp.CodeElements = codes[comp.ID]
		components[cp.ContainerID] = append(components[cp.ContainerID], comp)
	}
	containers := map[string][]Container{}
	for _, c := range flat.Containers {
		ctr := c.Container
		ctr.Components = components[ctr.ID]
		containers[c.SystemID] = append(containers[c.SystemID], ctr)
	}
	m := &Model{Relationships: flat.Relationships}
	for _, s := range flat.Systems {
		s.Containers = containers[s.ID]
		m.Systems = append(m.Systems, s)
	}
	return m, nil
}

// ProjectModel returns the project's current model the way a version records
// it: the relationships of other projects that touch shared systems, which
// the full model includes, are left out.
func (c *Client) ProjectModel(ctx context.Context, projectID string) (*Model, error) {
	var out struct {
		Systems       []System `json:"systems"`
		Relationships []struct {
			Relationship
			ProjectID string `json:"projectId"`
		} `json:"relationships"`
	}
	if err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/c4", nil, nil, &out); err != nil {
		return nil, err
	}
	m := &Model{Systems: out.Systems}
	for _, r := range out.Relationships {
		if r.ProjectID == "" || r.ProjectID == projectID {
			m.Relationships = append(m.Relationships, r.Relationship)
		}
	}
	return m, nil
}
