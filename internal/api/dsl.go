package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// DSLChange is one difference between an archyl.yaml and the model.
type DSLChange struct {
	// Action is added, removed or modified. Push never deletes: removed marks
	// what the model has and the file does not.
	Action string `json:"action"`
	// ElementType is the C4 level (system, container, component, code) or
	// relationship.
	ElementType string `json:"elementType"`
	// ElementName is the element's dotted path, or "From → To".
	ElementName string `json:"elementName"`
	Field       string `json:"field,omitempty"`
	DSLValue    string `json:"dslValue,omitempty"`
	ModelValue  string `json:"modelValue,omitempty"`
}

// DSLDiff is what pushing a file would change.
type DSLDiff struct {
	InSync   bool        `json:"inSync"`
	Changes  []DSLChange `json:"changes"`
	Warnings []string    `json:"warnings,omitempty"`
	// Prune is what a pruning push would delete, when the diff was asked.
	// A server that cannot prune leaves it out.
	Prune *DSLPrunePreview `json:"prune,omitempty"`
}

// DSLPruned is one object a pruning push deletes: the model has it and the
// file no longer declares it.
type DSLPruned struct {
	ElementType string `json:"elementType"`
	ElementName string `json:"elementName"`
}

// DSLPrunePreview is what a pruning push would delete.
type DSLPrunePreview struct {
	Entries []DSLPruned `json:"entries"`
	// Refused names each kind the server refuses to prune unless forced,
	// because the push would delete a large share of it.
	Refused []string `json:"refused,omitempty"`
}

// ExportDSL returns the project's model as archyl.yaml.
func (c *Client) ExportDSL(ctx context.Context, projectID string) (string, error) {
	var out string
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/dsl/export", nil, nil, &out)
	return out, err
}

// DSLSource is an archyl.yaml and the files its include directives reach,
// keyed by their path in the repository.
type DSLSource struct {
	Content string `json:"content"`
	// Path is the file's path in the repository, against which its includes
	// resolve.
	Path  string            `json:"path,omitempty"`
	Files map[string]string `json:"files,omitempty"`
	// Prune asks a push to delete what the model has and the file no longer
	// declares, and a diff to report it. Force lets a push delete a large
	// share of a kind.
	Prune bool `json:"prune,omitempty"`
	Force bool `json:"force,omitempty"`
}

// DiffDSL previews what importing a file would change, without writing.
func (c *Client) DiffDSL(ctx context.Context, projectID string, src DSLSource) (*DSLDiff, error) {
	var out DSLDiff
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/dsl/diff", nil, src, &out)
}

// DSLIncludesSupported reports whether the server merges the files an
// archyl.yaml includes. An older server ignores them and imports the root
// alone, so a push must not rely on it silently. Its DSL schema tells:
// servers that merge includes also accept top-level containers.
func (c *Client) DSLIncludesSupported(ctx context.Context) (bool, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := c.do(ctx, http.MethodGet, "/dsl/schema", nil, nil, &schema); err != nil {
		return false, err
	}
	_, ok := schema.Properties["containers"]
	return ok, nil
}
