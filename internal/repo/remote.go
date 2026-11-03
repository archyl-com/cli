package repo

import (
	"net/url"
	"strings"
)

// NormalizeRemote reduces the many spellings of a git remote to host/path, so
// git@github.com:acme/api.git and https://github.com/acme/api compare equal.
func NormalizeRemote(remote string) string {
	r := strings.TrimSpace(remote)
	if r == "" {
		return ""
	}
	if !strings.Contains(r, "://") {
		// scp-like syntax: user@host:path
		if at := strings.Index(r, "@"); at >= 0 {
			r = r[at+1:]
		}
		r = strings.Replace(r, ":", "/", 1)
	} else if u, err := url.Parse(r); err == nil {
		r = u.Hostname() + u.Path
	}
	r = strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git")
	return strings.ToLower(r)
}
