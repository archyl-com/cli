package commands

import (
	"path/filepath"
	"strings"
)

// The matchers below mirror the server's conformance evaluators, so `why`
// aims a rule at the same files a check would.

// expandBraces expands "*.{ts,tsx}" into "*.ts" and "*.tsx".
func expandBraces(pattern string) []string {
	open := strings.Index(pattern, "{")
	if open == -1 {
		return []string{pattern}
	}
	end := strings.Index(pattern[open:], "}")
	if end == -1 {
		return []string{pattern}
	}
	end += open
	var out []string
	for _, alt := range strings.Split(pattern[open+1:end], ",") {
		out = append(out, expandBraces(pattern[:open]+strings.TrimSpace(alt)+pattern[end+1:])...)
	}
	return out
}

// matchFileGlob matches a rule's fileGlob against a path or its base name.
func matchFileGlob(pattern, path string) bool {
	for _, p := range expandBraces(pattern) {
		if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
			return true
		}
		if matchGlobPath(p, path) {
			return true
		}
	}
	return false
}

// matchGlobPath matches "**/x/*.go", "domain/**", "a/**/b.go" and plain globs.
func matchGlobPath(pattern, path string) bool {
	if suffix, ok := strings.CutPrefix(pattern, "**/"); ok {
		parts := strings.Split(path, "/")
		for i := range parts {
			if ok, _ := filepath.Match(suffix, strings.Join(parts[i:], "/")); ok {
				return true
			}
			if strings.Contains(suffix, "/") {
				continue
			}
			if ok, _ := filepath.Match(suffix, parts[i]); ok {
				return true
			}
		}
		return false
	}
	if prefix, ok := strings.CutSuffix(pattern, "/**"); ok {
		return strings.HasPrefix(path, prefix+"/") || strings.Contains(path, "/"+prefix+"/")
	}
	if prefix, suffix, ok := strings.Cut(pattern, "/**/"); ok {
		inPrefix := strings.HasPrefix(path, prefix+"/") || strings.Contains(path, "/"+prefix+"/")
		base, _ := filepath.Match(suffix, filepath.Base(path))
		return inPrefix && base
	}
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	ok, _ := filepath.Match(pattern, filepath.Base(path))
	return ok
}
