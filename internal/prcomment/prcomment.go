// Package prcomment keeps one Archyl comment on a pull or merge request up to
// date: the first run posts it, later runs edit it in place, so a branch with
// ten pushes still has a single comment.
package prcomment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/safehttp"
)

// Marker identifies Archyl's comment among the others.
const Marker = "<!-- archyl:review -->"

// ErrNoTarget means the run is not on a pull or merge request.
var ErrNoTarget = errors.New("no pull or merge request to comment on")

// ErrNoToken means the run is on a request but cannot comment on it.
var ErrNoToken = errors.New("no token to comment with")

// Target is a pull or merge request the CLI can comment on.
type Target struct {
	Provider string // github or gitlab
	APIURL   string
	Repo     string // owner/name on GitHub, project ID on GitLab
	Number   int
	Token    string
	HTTP     *http.Client
}

var pullRef = regexp.MustCompile(`^refs/pull/(\d+)/`)

// Detect finds the pull or merge request of the current CI run.
func Detect(getenv func(string) string) (*Target, error) {
	switch {
	case getenv("GITHUB_ACTIONS") == "true":
		t := &Target{Provider: "github", APIURL: firstNonEmpty(getenv("GITHUB_API_URL"), "https://api.github.com"),
			Repo: getenv("GITHUB_REPOSITORY"), Token: firstNonEmpty(getenv("GITHUB_TOKEN"), getenv("GH_TOKEN"))}
		if m := pullRef.FindStringSubmatch(getenv("GITHUB_REF")); m != nil {
			t.Number, _ = strconv.Atoi(m[1])
		} else if path := getenv("GITHUB_EVENT_PATH"); path != "" {
			if raw, err := os.ReadFile(path); err == nil {
				var event struct {
					PullRequest struct {
						Number int `json:"number"`
					} `json:"pull_request"`
				}
				if json.Unmarshal(raw, &event) == nil {
					t.Number = event.PullRequest.Number
				}
			}
		}
		if t.Number == 0 {
			return nil, ErrNoTarget
		}
		if t.Token == "" {
			return nil, fmt.Errorf("%w: set GITHUB_TOKEN (permissions: pull-requests: write)", ErrNoToken)
		}
		return t, nil
	case getenv("GITLAB_CI") == "true":
		iid, _ := strconv.Atoi(getenv("CI_MERGE_REQUEST_IID"))
		if iid == 0 {
			return nil, ErrNoTarget
		}
		t := &Target{Provider: "gitlab", APIURL: getenv("CI_API_V4_URL"), Repo: getenv("CI_PROJECT_ID"), Number: iid,
			Token: firstNonEmpty(getenv("ARCHYL_GITLAB_TOKEN"), getenv("GITLAB_TOKEN"))}
		if t.Token == "" {
			return nil, fmt.Errorf("%w: set GITLAB_TOKEN to a token with the api scope (CI_JOB_TOKEN cannot comment)", ErrNoToken)
		}
		return t, nil
	}
	return nil, ErrNoTarget
}

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	URL  string `json:"html_url"`
	// User is the author on GitHub, Author on GitLab.
	User   author `json:"user"`
	Author author `json:"author"`
}

type author struct {
	Login    string `json:"login"`
	Username string `json:"username"`
}

func (a author) name() string {
	if a.Login != "" {
		return a.Login
	}
	return a.Username
}

// actionsBot is who GITHUB_TOKEN posts as. That token cannot read /user.
const actionsBot = "github-actions[bot]"

// self is the account the token posts as.
func (t *Target) self(ctx context.Context) string {
	var me author
	if err := t.call(ctx, http.MethodGet, strings.TrimSuffix(t.APIURL, "/")+"/user", nil, &me); err == nil && me.name() != "" {
		return me.name()
	}
	return actionsBot
}

// Upsert writes body (prefixed with the marker) as the request's Archyl
// comment, editing the previous one when there is one.
func (t *Target) Upsert(ctx context.Context, body string) error {
	body = Marker + "\n" + body
	existing, err := t.find(ctx)
	if err != nil {
		return err
	}
	payload := map[string]string{"body": body}
	if existing != 0 {
		return t.call(ctx, http.MethodPatch, t.commentURL(existing), payload, nil)
	}
	return t.call(ctx, http.MethodPost, t.listURL(), payload, nil)
}

func (t *Target) listURL() string {
	if t.Provider == "gitlab" {
		return fmt.Sprintf("%s/projects/%s/merge_requests/%d/notes", strings.TrimSuffix(t.APIURL, "/"), url.PathEscape(t.Repo), t.Number)
	}
	return fmt.Sprintf("%s/repos/%s/issues/%d/comments", strings.TrimSuffix(t.APIURL, "/"), t.Repo, t.Number)
}

func (t *Target) commentURL(id int64) string {
	if t.Provider == "gitlab" {
		return fmt.Sprintf("%s/%d", t.listURL(), id)
	}
	return fmt.Sprintf("%s/repos/%s/issues/comments/%d", strings.TrimSuffix(t.APIURL, "/"), t.Repo, id)
}

func firstAuthor(c comment) string {
	if name := c.User.name(); name != "" {
		return name
	}
	return c.Author.name()
}

// find returns the ID of the existing Archyl comment, 0 when there is none.
//
// Anyone can comment on a pull request, so the marker alone proves nothing: the
// comment has to be our own, or an attacker's would get edited into a verdict.
func (t *Target) find(ctx context.Context) (int64, error) {
	self := t.self(ctx)
	for page := 1; page <= 10; page++ {
		var comments []comment
		u := t.listURL() + "?per_page=100&page=" + strconv.Itoa(page)
		if err := t.call(ctx, http.MethodGet, u, nil, &comments); err != nil {
			return 0, err
		}
		for _, c := range comments {
			if strings.HasPrefix(c.Body, Marker) && strings.EqualFold(firstAuthor(c), self) {
				return c.ID, nil
			}
		}
		if len(comments) < 100 {
			break
		}
	}
	return 0, nil
}

func (t *Target) call(ctx context.Context, method, target string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.Provider == "gitlab" {
		req.Header.Set("PRIVATE-TOKEN", t.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+t.Token)
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	client := t.HTTP
	if client == nil {
		client = safehttp.Client(20 * time.Second)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: HTTP %d %s", method, t.Provider, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
