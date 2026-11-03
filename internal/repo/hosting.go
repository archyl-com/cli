package repo

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoRemote is returned when the repository has no remote to read.
var ErrNoRemote = errors.New("this repository has no git remote")

// Hosted is a repository on a git hosting provider, as Archyl names it.
type Hosted struct {
	// Provider is Archyl's provider name: github, gitlab, bitbucket,
	// azure_devops, or a self-hosted one (github_enterprise, gitlab_private,
	// gitea) when the host is given one explicitly.
	Provider string `json:"provider"`
	Host     string `json:"host"`
	// FullName is owner/name; GitLab owners keep their subgroups and Azure
	// DevOps owners are organization/project.
	FullName string `json:"fullName"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	CloneURL string `json:"cloneUrl"`
}

// Owner is the part of FullName before the last slash.
func (h Hosted) Owner() string {
	if i := strings.LastIndex(h.FullName, "/"); i > 0 {
		return h.FullName[:i]
	}
	return ""
}

// knownHosts maps the public hosting services to Archyl's provider names.
var knownHosts = map[string]string{
	"github.com":        "github",
	"gitlab.com":        "gitlab",
	"bitbucket.org":     "bitbucket",
	"dev.azure.com":     "azure_devops",
	"ssh.dev.azure.com": "azure_devops",
}

// SelfHostedProviders are the provider names a host Archyl does not
// recognise can be given.
var SelfHostedProviders = []string{"github_enterprise", "gitlab_private", "gitea"}

// UnsupportedHostError is returned for a remote on a host Archyl cannot
// guess the provider of.
type UnsupportedHostError struct {
	Host string
}

func (e *UnsupportedHostError) Error() string {
	return fmt.Sprintf("Archyl cannot tell which git provider %s is; pass --provider %s",
		e.Host, strings.Join(SelfHostedProviders, "|"))
}

// ParseHosted reads a git remote URL as a hosted repository. provider
// overrides the guess from the host, which a self-hosted instance needs.
func ParseHosted(remote, provider string) (Hosted, error) {
	normalized := NormalizeRemote(remote)
	if normalized == "" {
		return Hosted{}, ErrNoRemote
	}
	host, path, ok := strings.Cut(normalized, "/")
	if !ok || path == "" {
		return Hosted{}, fmt.Errorf("cannot read a repository from the remote %q", remote)
	}
	// Keep the original case of owner and name: providers display it.
	path = originalCase(remote, path)

	if provider == "" {
		provider = knownHosts[host]
		if provider == "" && strings.HasSuffix(host, ".visualstudio.com") {
			provider = "azure_devops"
		}
	}
	if provider == "" {
		return Hosted{}, &UnsupportedHostError{Host: host}
	}

	webHost := host
	if provider == "azure_devops" {
		path = azurePath(host, path)
		webHost = "dev.azure.com"
	}
	segments := strings.Split(path, "/")
	if len(segments) < 2 || segments[0] == "" || segments[len(segments)-1] == "" {
		return Hosted{}, fmt.Errorf("cannot read owner/name from the remote %q", remote)
	}
	name := segments[len(segments)-1]

	h := Hosted{Provider: provider, Host: host, FullName: path, Name: name}
	if provider == "azure_devops" {
		org, project := segments[0], strings.Join(segments[1:len(segments)-1], "/")
		h.URL = "https://" + webHost + "/" + org + "/" + project + "/_git/" + name
		h.CloneURL = h.URL
		return h, nil
	}
	h.URL = "https://" + webHost + "/" + path
	h.CloneURL = h.URL + ".git"
	return h, nil
}

// azurePath turns the remote paths of Azure DevOps into org/project/repo:
// org/project/_git/repo over HTTPS, v3/org/project/repo over SSH, and on the
// legacy <org>.visualstudio.com hosts, where the organization is the
// subdomain, [DefaultCollection/]project/_git/repo. A repository named after
// its project may leave the project out: org/_git/repo.
func azurePath(host, path string) string {
	path = strings.TrimPrefix(path, "v3/")
	if org, ok := strings.CutSuffix(host, ".visualstudio.com"); ok && org != "vs-ssh" {
		if head, rest, ok := strings.Cut(path, "/"); ok && strings.EqualFold(head, "DefaultCollection") {
			path = rest
		}
		path = org + "/" + path
	}
	segments := strings.Split(path, "/")
	if len(segments) == 3 && segments[1] == "_git" {
		return segments[0] + "/" + segments[2] + "/" + segments[2]
	}
	return strings.Replace(path, "/_git/", "/", 1)
}

// originalCase finds path in remote ignoring case and returns it as written.
func originalCase(remote, path string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(remote), "/"), ".git")
	if i := strings.Index(strings.ToLower(trimmed), path); i >= 0 && i+len(path) == len(trimmed) {
		return trimmed[i:]
	}
	return path
}
