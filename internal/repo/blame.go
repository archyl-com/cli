package repo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Commit is the commit that last touched a line or a file.
type Commit struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	Email   string    `json:"email,omitempty"`
	Date    time.Time `json:"date"`
	Subject string    `json:"subject"`
	// Uncommitted is set when the line only exists in the working tree.
	Uncommitted bool `json:"uncommitted,omitempty"`
}

// Short is the abbreviated SHA.
func (c *Commit) Short() string {
	if len(c.SHA) > 8 {
		return c.SHA[:8]
	}
	return c.SHA
}

// Blame returns the commit that last changed line of path (relative to the
// root).
func (g *Git) Blame(ctx context.Context, path string, line int) (*Commit, error) {
	out, err := g.run(ctx, "blame", "--porcelain", "-L", fmt.Sprintf("%d,%d", line, line), "--", path)
	if err != nil {
		return nil, err
	}
	return parsePorcelain(out)
}

// LastCommit returns the commit that last changed path, nil when git has no
// history for it.
func (g *Git) LastCommit(ctx context.Context, path string) (*Commit, error) {
	out, err := g.run(ctx, "log", "-1", "--format=%H%x00%an%x00%ae%x00%at%x00%s", "--", path)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSpace(out), "\x00")
	if len(fields) != 5 {
		return nil, nil
	}
	return &Commit{SHA: fields[0], Author: fields[1], Email: fields[2], Date: unixTime(fields[3]), Subject: fields[4]}, nil
}

// parsePorcelain reads the header of `git blame --porcelain` for one line.
func parsePorcelain(out string) (*Commit, error) {
	lines := strings.Split(out, "\n")
	head := strings.Fields(lines[0])
	if len(head) == 0 {
		return nil, fmt.Errorf("git blame: no output")
	}
	c := &Commit{SHA: head[0], Uncommitted: strings.Trim(head[0], "0") == ""}
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, "\t") {
			break
		}
		key, value, _ := strings.Cut(l, " ")
		switch key {
		case "author":
			c.Author = value
		case "author-mail":
			c.Email = strings.Trim(value, "<>")
		case "author-time":
			c.Date = unixTime(value)
		case "summary":
			c.Subject = value
		}
	}
	return c, nil
}

func unixTime(s string) time.Time {
	sec, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
