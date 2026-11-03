// Package radar finds dependency manifests in a repository and turns radar
// findings into the same violations the conformance checks report.
package radar

import (
	"fmt"
	"path"
	"strings"

	"github.com/archyl-com/cli/internal/api"
)

// manifestNames are the files Archyl knows how to parse.
var manifestNames = map[string]bool{
	"package.json": true, "go.mod": true, "requirements.txt": true, "pyproject.toml": true,
	"composer.json": true, "Cargo.toml": true, "pom.xml": true, "Gemfile": true,
	"packages.config": true, "build.gradle": true, "build.gradle.kts": true,
	"CMakeLists.txt": true, "conanfile.txt": true, "conanfile.py": true, "vcpkg.json": true,
}

// IsManifest reports whether a path is a dependency manifest.
func IsManifest(p string) bool {
	base := path.Base(strings.ReplaceAll(p, "\\", "/"))
	return manifestNames[base] || strings.HasSuffix(base, ".csproj") || strings.HasSuffix(base, ".cmake")
}

// Skip reports paths that hold vendored or generated manifests, whose
// dependencies are not the project's own.
func Skip(p string) bool {
	for _, dir := range []string{"node_modules/", "vendor/", "third_party/", ".git/", "dist/", "build/"} {
		if strings.HasPrefix(p, dir) || strings.Contains(p, "/"+dir) {
			return true
		}
	}
	return false
}

// Rank orders rings by how strongly they discourage use; 0 is unclassified.
func Rank(ring string) int {
	switch ring {
	case "hold":
		return 4
	case "assess":
		return 3
	case "trial":
		return 2
	case "adopt":
		return 1
	}
	return 0
}

// Violations converts hold and assess dependencies into conformance-style
// violations, so the CI report formats apply. line finds the line a
// dependency is declared on, 0 when unknown.
func Violations(deps []api.CheckedDependency, line func(file, name string) int) []api.Violation {
	var out []api.Violation
	for _, d := range deps {
		ring := d.Ring()
		if Rank(ring) < 3 {
			continue
		}
		severity, title := "high", fmt.Sprintf("%s is on hold", d.Technology.Name)
		if ring == "assess" {
			severity, title = "medium", fmt.Sprintf("%s is still being assessed", d.Technology.Name)
		}
		version := ""
		if d.Version != "" {
			version = " " + d.Version
		}
		out = append(out, api.Violation{
			RuleID:      "radar-" + ring,
			Severity:    severity,
			Title:       fmt.Sprintf("%s (%s%s)", title, d.Name, version),
			Suggestion:  d.Technology.RadarNote,
			Description: "The technology radar marks " + d.Technology.Name + " as " + ring + ".",
			FilePath:    d.FilePath,
			LineStart:   line(d.FilePath, d.Name),
		})
	}
	return out
}

// Rules describes the radar rules for SARIF.
func Rules() []api.Rule {
	return []api.Rule{
		{ID: "radar-hold", Name: "Technology on hold", Description: "A dependency matches a technology the radar puts on hold."},
		{ID: "radar-assess", Name: "Technology under assessment", Description: "A dependency matches a technology the radar is still assessing."},
	}
}

// DeclarationLine finds the line of content that declares a dependency: the
// first line containing its full name, else its last path segment.
func DeclarationLine(content, name string) int {
	lines := strings.Split(content, "\n")
	candidates := []string{name}
	if i := strings.LastIndexAny(name, "/:"); i >= 0 && i < len(name)-1 {
		candidates = append(candidates, name[i+1:])
	}
	for _, needle := range candidates {
		for i, l := range lines {
			if strings.Contains(l, needle) {
				return i + 1
			}
		}
	}
	return 0
}

// NewHolds returns the hold dependencies of after that before does not have.
func NewHolds(before, after []api.CheckedDependency) []api.CheckedDependency {
	had := map[string]bool{}
	for _, d := range before {
		if d.Ring() == "hold" {
			had[d.Name] = true
		}
	}
	var out []api.CheckedDependency
	for _, d := range after {
		if d.Ring() == "hold" && !had[d.Name] {
			out = append(out, d)
		}
	}
	return out
}
