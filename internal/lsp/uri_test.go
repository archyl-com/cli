package lsp

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
)

func TestRelPath(
	t *testing.T,
) {
	root := t.TempDir()
	testCases := []struct {
		name string
		path string
		want string
	}{
		{name: "inside", path: filepath.Join(root, "a", "b.go"), want: "a/b.go"},
		{name: "the root itself", path: root, want: ""},
		{name: "outside", path: filepath.Join(filepath.Dir(root), "elsewhere.go"), want: ""},
		{name: "through a URI", path: uriToPath(pathToURI(filepath.Join(root, "c d.go"))), want: "c d.go"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, relPath(root, testCase.path))
		})
	}
}

func TestURIToPath(
	t *testing.T,
) {
	testCases := []struct {
		name string
		uri  string
		want string
	}{
		{name: "unix", uri: "file:///home/me/repo/a.go", want: filepath.FromSlash("/home/me/repo/a.go")},
		{name: "escaped", uri: "file:///home/me/my%20repo/a.go", want: filepath.FromSlash("/home/me/my repo/a.go")},
		{name: "windows drive", uri: "file:///C:/repo/a.go", want: filepath.FromSlash("C:/repo/a.go")},
		{name: "not a file", uri: "untitled:Untitled-1", want: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, uriToPath(testCase.uri))
		})
	}
	if runtime.GOOS != "windows" {
		assert.Equal(t, "file:///home/me/my%20repo/a.go", pathToURI("/home/me/my repo/a.go"))
	}
}

func TestDiagnostics(
	t *testing.T,
) {
	text := "line one\nline two é\nline three"
	testCases := []struct {
		name      string
		violation api.Violation
		severity  int
		rng       lspRange
	}{
		{name: "critical is an error", violation: api.Violation{Severity: "critical", LineStart: 2, LineEnd: 2}, severity: severityError,
			rng: lspRange{Start: position{Line: 1}, End: position{Line: 1, Character: 10}}},
		{name: "medium is a warning across lines", violation: api.Violation{Severity: "medium", LineStart: 1, LineEnd: 3}, severity: severityWarning,
			rng: lspRange{Start: position{Line: 0}, End: position{Line: 2, Character: 10}}},
		{name: "low is information", violation: api.Violation{Severity: "low", LineStart: 1}, severity: severityInformation,
			rng: lspRange{Start: position{Line: 0}, End: position{Line: 0, Character: 8}}},
		{name: "no line lands on the first", violation: api.Violation{Severity: "unknown"}, severity: severityHint,
			rng: lspRange{Start: position{Line: 0}, End: position{Line: 0, Character: 8}}},
		{name: "past the end is clamped", violation: api.Violation{Severity: "high", LineStart: 40, LineEnd: 41}, severity: severityError,
			rng: lspRange{Start: position{Line: 2}, End: position{Line: 2, Character: 10}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			diags := diagnostics([]api.Violation{testCase.violation}, nil, text)
			assert.Equal(t, testCase.severity, diags[0].Severity)
			assert.Equal(t, testCase.rng, diags[0].Range)
		})
	}
}
