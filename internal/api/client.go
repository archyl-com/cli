// Package api is a typed client for the slice of the Archyl REST API the CLI
// uses.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/safehttp"
)

// Credentials authenticates requests. Invalidate is called once after a 401;
// returning true retries the request with fresh credentials.
type Credentials interface {
	Apply(ctx context.Context, req *http.Request) error
	Invalidate(ctx context.Context) (bool, error)
}

// Client calls the Archyl REST API.
type Client struct {
	host           string
	organizationID string
	creds          Credentials
	http           *http.Client
}

// NewClient returns a client for host. organizationID may be empty: the API
// then uses the caller's default organization.
func NewClient(
	host string,
	organizationID string,
	creds Credentials,
) *Client {
	return &Client{
		host:           strings.TrimSuffix(host, "/"),
		organizationID: organizationID,
		creds:          creds,
		http:           safehttp.Client(60 * time.Second),
	}
}

// Host returns the API base URL.
func (c *Client) Host() string {
	return c.host
}

// Error is a non-2xx answer from the API.
type Error struct {
	Status  int
	Message string
	// Unsupported is set when the server has no such route: it predates the
	// endpoint, rather than lacking the resource.
	Unsupported bool
}

// ErrServerTooOld is the message of an error on a route the server lacks.
var ErrServerTooOld = errors.New("this Archyl server is too old for this command; update it to the latest release")

// IsUnsupported reports whether err comes from a route the server lacks.
func IsUnsupported(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Unsupported
}

// routeMissing recognises the router's own 404: a bare "Not Found" from
// Archyl's error handler, or Fiber's default "Cannot POST /api/v1/…". Handlers
// name what they did not find ("Project not found").
func routeMissing(status int, message string) bool {
	return status == http.StatusNotFound && (message == "Not Found" || strings.HasPrefix(message, "Cannot "))
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// IsStatus reports whether err is an API error with the given status.
func IsStatus(err error, status int) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Status == status
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}

	for attempt := 0; ; attempt++ {
		err := c.once(ctx, method, path, query, payload, out)
		if attempt > 0 || !IsStatus(err, http.StatusUnauthorized) || c.creds == nil {
			return err
		}
		retry, refreshErr := c.creds.Invalidate(ctx)
		if refreshErr != nil {
			return refreshErr
		}
		if !retry {
			return err
		}
	}
}

func (c *Client) once(ctx context.Context, method, path string, query url.Values, payload []byte, out any) error {
	target := c.host + "/api/v1" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", buildinfo.UserAgent())
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.organizationID != "" {
		req.Header.Set("X-Organization-ID", c.organizationID)
	}
	if c.creds != nil {
		if err := c.creds.Apply(ctx, req); err != nil {
			return err
		}
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("reach %s: %w", c.host, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		message := errorMessage(raw)
		if routeMissing(resp.StatusCode, message) {
			return &Error{Status: resp.StatusCode, Message: ErrServerTooOld.Error(), Unsupported: true}
		}
		return &Error{Status: resp.StatusCode, Message: message}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if s, ok := out.(*string); ok {
		*s = string(raw)
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// errorMessage reads both error shapes the API emits: {"error": "text"} from
// handlers and {"error": true, "message": "text"} from the global handler.
func errorMessage(raw []byte) string {
	var body struct {
		Error   any    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return strings.TrimSpace(string(raw))
	}
	if body.Message != "" {
		return body.Message
	}
	if s, ok := body.Error.(string); ok {
		return s
	}
	return ""
}
