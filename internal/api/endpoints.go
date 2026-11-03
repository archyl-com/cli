package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Me returns the signed-in user.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var u User
	return &u, c.do(ctx, http.MethodGet, "/users/me", nil, nil, &u)
}

// Projects lists the projects the caller can see.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out struct {
		Data []Project `json:"data"`
	}
	err := c.do(ctx, http.MethodGet, "/projects/", nil, nil, &out)
	return out.Data, err
}

// Project returns one project.
func (c *Client) Project(ctx context.Context, projectID string) (*Project, error) {
	var p Project
	return &p, c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID), nil, nil, &p)
}

// Locate places files in the architecture and computes their blast radius.
func (c *Client) Locate(ctx context.Context, projectID string, paths []string, maxDepth int) (*LocateResult, error) {
	var out LocateResult
	body := map[string]any{"paths": paths, "maxDepth": maxDepth}
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/harness/locate", nil, body, &out)
}

// AgentContext returns the project's architecture context as markdown.
func (c *Client) AgentContext(ctx context.Context, projectID string) (string, error) {
	var out string
	query := url.Values{"format": {"markdown"}}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/agent/context", query, nil, &out)
	return out, err
}

// StartSession opens a harness work session.
func (c *Client) StartSession(ctx context.Context, projectID string, req StartSessionRequest) (*StartSessionResult, error) {
	var out StartSessionResult
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/harness/sessions", nil, req, &out)
}

// Sessions lists a project's sessions.
func (c *Client) Sessions(ctx context.Context, projectID string, activeOnly bool) ([]Session, error) {
	var out struct {
		Sessions []Session `json:"sessions"`
	}
	query := url.Values{}
	if activeOnly {
		query.Set("active", "true")
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/harness/sessions", query, nil, &out)
	return out.Sessions, err
}

// Heartbeat keeps a session alive.
func (c *Client) Heartbeat(ctx context.Context, sessionID string) error {
	return c.do(ctx, http.MethodPost, "/harness/sessions/"+url.PathEscape(sessionID)+"/heartbeat", nil, nil, nil)
}

// ReportFiles attributes changed files to a session.
func (c *Client) ReportFiles(ctx context.Context, sessionID string, files []ChangedFile) error {
	body := map[string]any{"changedFiles": files}
	return c.do(ctx, http.MethodPost, "/harness/sessions/"+url.PathEscape(sessionID)+"/files", nil, body, nil)
}

// FinishSession closes a session with its outcome.
func (c *Client) FinishSession(ctx context.Context, sessionID string, req FinishSessionRequest) (*FinishSessionResult, error) {
	var out FinishSessionResult
	return &out, c.do(ctx, http.MethodPost, "/harness/sessions/"+url.PathEscape(sessionID)+"/finish", nil, req, &out)
}

// CancelSession ends a session without an outcome.
func (c *Client) CancelSession(ctx context.Context, sessionID string) error {
	return c.do(ctx, http.MethodPost, "/harness/sessions/"+url.PathEscape(sessionID)+"/cancel", nil, nil, nil)
}

// CheckConformance runs the project's rules against file contents.
func (c *Client) CheckConformance(ctx context.Context, projectID string, req ConformanceCheckRequest) (*ConformanceCheckResult, error) {
	var raw string
	if err := c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/conformance/check", nil, req, &raw); err != nil {
		return nil, err
	}
	return decodeCheck(raw)
}

// decodeCheck refuses an answer that says nothing about the rules: a check that
// cannot tell must not read as a clean one.
func decodeCheck(raw string) (*ConformanceCheckResult, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("decode conformance check: %w", err)
	}
	if keys["summary"] == nil && keys["violations"] == nil {
		return nil, errors.New("the server's answer to the conformance check holds no result")
	}
	var out ConformanceCheckResult
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode conformance check: %w", err)
	}
	return &out, nil
}

// LatestDrift returns the latest drift score, or nil when none was computed.
func (c *Client) LatestDrift(ctx context.Context, projectID string) (*DriftScore, error) {
	var out *DriftScore
	err := c.do(ctx, http.MethodGet, "/drift/latest", url.Values{"projectId": {projectID}}, nil, &out)
	return out, err
}

// ComputeDrift starts a drift computation; poll Drift until it completes.
func (c *Client) ComputeDrift(ctx context.Context, projectID string) (*DriftScore, error) {
	var out DriftScore
	return &out, c.do(ctx, http.MethodPost, "/drift/compute", nil, map[string]string{"projectId": projectID}, &out)
}

// Drift returns one drift computation.
func (c *Client) Drift(ctx context.Context, scoreID string) (*DriftScore, error) {
	var out DriftScore
	return &out, c.do(ctx, http.MethodGet, "/drift/"+url.PathEscape(scoreID), nil, nil, &out)
}

// SyncDSL imports an archyl.yaml, with the files it includes, into the
// project.
func (c *Client) SyncDSL(ctx context.Context, projectID string, src DSLSource) (*DSLImport, error) {
	var out struct {
		Import *DSLImport `json:"import"`
	}
	if err := c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/dsl/ingest", nil, src, &out); err != nil {
		return nil, err
	}
	if out.Import == nil {
		return &DSLImport{}, nil
	}
	return out.Import, nil
}

// IngestRelease records a release.
func (c *Client) IngestRelease(ctx context.Context, projectID string, req ReleaseRequest) (*Release, error) {
	var out Release
	return &out, c.do(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/releases/ingest", nil, req, &out)
}

// SessionFile is a file a session changed.
type SessionFile struct {
	Path   string    `json:"path"`
	Status string    `json:"status"`
	SeenAt time.Time `json:"seenAt"`
}

// SessionFiles lists the files attributed to a session.
func (c *Client) SessionFiles(ctx context.Context, sessionID string) ([]SessionFile, error) {
	var out struct {
		Files []SessionFile `json:"files"`
	}
	err := c.do(ctx, http.MethodGet, "/harness/sessions/"+url.PathEscape(sessionID)+"/files", nil, nil, &out)
	return out.Files, err
}
