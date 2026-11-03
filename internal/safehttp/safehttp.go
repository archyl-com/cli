// Package safehttp holds the redirect rule for requests that carry secrets.
package safehttp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const maxRedirects = 10

// Client returns a client that only follows redirects to the origin it was
// asked to reach. timeout 0 means none.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: SameOrigin}
}

// SameOrigin refuses a redirect that changes scheme, host or port. Go keeps
// custom headers such as X-API-Key across hosts and replays bodies on a 307,
// and it keeps Authorization across ports and from https to http, so following
// one would hand a token or a refresh token to whatever the Location names.
func SameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	from := via[0].URL
	if req.URL.Scheme != from.Scheme || !strings.EqualFold(req.URL.Host, from.Host) {
		return fmt.Errorf("refusing to follow a redirect from %s://%s to %s://%s: check the configured host",
			from.Scheme, from.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}
