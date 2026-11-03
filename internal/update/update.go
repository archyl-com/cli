// Package update tells the user when a newer archyl is out, without ever
// slowing a command down: the check runs in a detached process and its
// answer is read from the cache on the next run.
package update

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/cache"
)

// CacheKey is where the latest known version is cached.
const CacheKey = "update"

// CheckEvery is how often the release feed is consulted.
const CheckEvery = 24 * time.Hour

// Fetch asks GitHub for the latest release and caches it.
func Fetch(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tag, err := Releases{}.Latest(ctx)
	// Cache even an empty answer, so a missing release feed is not retried on
	// every command.
	var status *statusError
	if errors.As(err, &status) {
		tag, err = "", nil
	}
	if err != nil {
		return "", err
	}
	return tag, cache.Save(CacheKey, tag)
}

// Newer reports whether latest is a higher version than current.
func Newer(current, latest string) bool {
	c, okC := parse(current)
	l, okL := parse(latest)
	if !okC || !okL {
		return false
	}
	for i := range 3 {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	var out [3]int
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
