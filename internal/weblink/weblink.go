// Package weblink builds links into the Archyl web app from the CLI's API
// host, so any element or decision can be opened in the browser.
package weblink

import (
	"net/url"
	"strings"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
)

// AppURL resolves the web app's base URL: the explicit value when set,
// otherwise the API host with "api." swapped for "app."
// (https://api.archyl.com → https://app.archyl.com). A self-hosted instance on
// one domain serves both, so any other host is returned unchanged.
func AppURL(apiHost, explicit string) string {
	if explicit != "" {
		return strings.TrimSuffix(explicit, "/")
	}
	u, err := url.Parse(strings.TrimSuffix(apiHost, "/"))
	if err != nil || u.Host == "" {
		return strings.TrimSuffix(apiHost, "/")
	}
	if rest, ok := strings.CutPrefix(u.Host, "api."); ok {
		u.Host = "app." + rest
	}
	u.Path = ""
	return u.String()
}

// Project links to a project's diagram.
func Project(app, projectID string) string {
	return app + "/project/" + url.PathEscape(projectID)
}

// Element links to the diagram with an element selected at its level.
func Element(app, projectID string, n *graph.Node) string {
	q := url.Values{"select": {n.ID}, "selectType": {graph.SelectType(n.Level)}}
	for _, ancestor := range n.Path() {
		if ancestor == n {
			break
		}
		q.Set(ancestor.Level+"Id", ancestor.ID)
	}
	return Project(app, projectID) + "?" + q.Encode()
}

// ADR links to a decision, unfolded.
func ADR(app, projectID, adrID string) string {
	return Project(app, projectID) + "/decisions?" + url.Values{"expandId": {adrID}}.Encode()
}

// SearchResult links any global search hit to its page.
func SearchResult(app string, r api.SearchResult) string {
	if r.Type == "project" {
		return Project(app, r.ID)
	}
	base := Project(app, r.ProjectID)
	switch r.Type {
	case "system", "container", "component", "code_element":
		q := url.Values{"select": {r.ID}, "selectType": {r.Type}}
		for key, id := range map[string]string{"systemId": r.SystemID, "containerId": r.ContainerID, "componentId": r.ComponentID} {
			if id != "" {
				q.Set(key, id)
			}
		}
		return base + "?" + q.Encode()
	case "adr":
		return base + "/decisions?" + url.Values{"expandId": {r.ID}}.Encode()
	case "documentation":
		return base + "/documentation?" + url.Values{"expandId": {r.ID}}.Encode()
	case "api_contract":
		return base + "/api-contracts?" + url.Values{"expandId": {r.ID}}.Encode()
	case "event_channel":
		return base + "/api-contracts?" + url.Values{"tab": {"events"}, "expandId": {r.ID}}.Encode()
	}
	return base
}

// ChangeRequest links to a change request.
func ChangeRequest(app, projectID, id string) string {
	return Project(app, projectID) + "/pull-requests/" + url.PathEscape(id)
}
