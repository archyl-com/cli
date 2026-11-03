package commands

import (
	"context"
	"fmt"

	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/repo"
)

// maxFileBytes bounds what one file sends to the conformance check.
const maxFileBytes = 512 << 10

// resolveBase picks the ref to compare with: the flag, then the pull
// request's target in CI, then the previous commit on a CI push, then the
// repository's default branch.
func resolveBase(ctx context.Context, git *repo.Git, flag string, env ci.Env) (string, error) {
	base := flag
	if base == "" {
		base = env.BaseRef()
	}
	if base == "" && env.IsCI() && git.Resolves(ctx, "HEAD~1") {
		base = "HEAD~1"
	}
	if base == "" {
		base = git.DefaultBase(ctx)
	}
	if base != "" && !git.Resolves(ctx, base) {
		return "", fmt.Errorf("cannot find %s in this clone; fetch the full history (actions/checkout fetch-depth: 0, GitLab GIT_DEPTH: 0) or pass --base", base)
	}
	return base, nil
}

// skippedFile is a changed file whose content was not checked.
type skippedFile struct {
	Path   string          `json:"path"`
	Reason repo.SkipReason `json:"reason"`
}

// readChanged loads the content of changed files for the conformance check.
// What cannot be read safely stays out and is reported, so a verdict never
// claims more than was inspected.
func readChanged(root string, changes []repo.Change) (map[string]string, []skippedFile) {
	contents := make(map[string]string, len(changes))
	var skipped []skippedFile
	for _, c := range changes {
		if c.Status == "deleted" {
			continue
		}
		raw, reason := repo.ReadForUpload(root, c.Path, maxFileBytes)
		if reason != "" {
			skipped = append(skipped, skippedFile{Path: c.Path, Reason: reason})
			continue
		}
		contents[c.Path] = string(raw)
	}
	return contents, skipped
}
