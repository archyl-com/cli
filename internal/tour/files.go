package tour

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/pathmatch"
)

// entryNames are files that tell a reader where a program starts or what a
// folder is for.
var entryNames = map[string]bool{
	"readme.md": true, "main.go": true, "server.go": true, "app.go": true, "routes.go": true,
	"index.ts": true, "index.tsx": true, "index.js": true, "main.ts": true, "main.tsx": true,
	"app.ts": true, "app.tsx": true, "server.ts": true, "main.py": true, "app.py": true,
	"main.rs": true, "lib.rs": true, "program.cs": true, "application.java": true,
	"dockerfile": true, "package.json": true, "go.mod": true, "cargo.toml": true, "pyproject.toml": true,
}

// ReasonEntry marks a file where a program or package starts.
const ReasonEntry = "entry point"

// minor are files that declare rather than do: a reader learns little from
// them first.
var minor = map[string]bool{
	"ifaces.go": true, "ofaces.go": true, "interfaces.go": true, "types.go": true, "const.go": true,
	"consts.go": true, "constants.go": true, "errors.go": true, "doc.go": true, "fx.go": true,
	"wire.go": true, "wire_gen.go": true, "options.go": true, "models.go": true,
}

// sourceExt are the files a reader learns the code from.
var sourceExt = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".py": true, ".rb": true,
	".java": true, ".kt": true, ".scala": true, ".cs": true, ".rs": true, ".swift": true, ".php": true,
	".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true, ".ex": true, ".proto": true,
	".graphql": true, ".sql": true, ".md": true,
}

// overviewNames are the files that introduce a whole repository.
var overviewNames = []string{"README.md", "ARCHITECTURE.md", "CONTRIBUTING.md", "docs/README.md", "archyl.yaml"}

// Repo is what the file picker knows of the working tree.
type Repo struct {
	// Files are the tracked files, relative to the root.
	Files []string
	// Churn counts recent commits per file.
	Churn map[string]int
}

// StartHere picks the files to read first for an element: the paths the
// model records for it and what it holds, then the most telling files of
// the folder named after it.
func StartHere(n *graph.Node, r Repo, limit int) []File {
	exists := make(map[string]bool, len(r.Files))
	for _, f := range r.Files {
		exists[f] = true
	}
	var out []File
	seen := map[string]bool{}
	add := func(p, reason string) {
		p = strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, `\`, "/")), "./")
		if p == "" || p == "." || seen[p] || len(out) >= limit {
			return
		}
		if len(exists) > 0 && !exists[p] {
			return
		}
		seen[p] = true
		out = append(out, File{Path: p, Reason: reason})
	}

	var dirs []string
	var documented func(m *graph.Node)
	documented = func(m *graph.Node) {
		if m.FilePath != "" {
			dirs = append(dirs, path.Dir(strings.ReplaceAll(m.FilePath, `\`, "/")))
			reason := "where the model places " + m.Name
			if m == n {
				reason = "where the model places it"
			}
			add(m.FilePath, reason)
		}
		for _, c := range m.Children {
			documented(c)
		}
	}
	documented(n)

	dir := pathmatch.Folder(n.Name, r.Files)
	if dir == "" {
		// No folder carries its name: the one its documented files share.
		dir = commonDir(dirs)
	}
	if dir == "" || dir == "." {
		return out
	}
	type scored struct {
		path   string
		score  int
		reason string
	}
	var ranked []scored
	for _, f := range r.Files {
		if !strings.HasPrefix(f, dir+"/") || noise(f) {
			continue
		}
		base := strings.ToLower(path.Base(f))
		depth := strings.Count(strings.TrimPrefix(f, dir+"/"), "/")
		churn := r.Churn[f]
		s := scored{path: f, score: churn*3 - depth*2}
		if !sourceExt[strings.ToLower(path.Ext(base))] && !entryNames[base] {
			s.score -= 6
		}
		switch {
		case entryNames[base]:
			s.score += 12
			s.reason = ReasonEntry
			if base == "readme.md" {
				s.reason = "what the folder is for"
			}
		case churn > 0:
			s.reason = "changed in " + strconv.Itoa(churn) + " recent " + plural("commit", churn)
		default:
			s.reason = "in " + dir + "/"
		}
		if minor[base] || strings.HasSuffix(base, "_string.go") || strings.HasPrefix(base, "mock") {
			s.score -= 8
		}
		ranked = append(ranked, s)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].path < ranked[j].path
	})
	for _, s := range ranked {
		add(s.path, s.reason)
	}
	return out
}

// Overview picks the files that introduce the repository.
func Overview(r Repo) []File {
	exists := make(map[string]bool, len(r.Files))
	for _, f := range r.Files {
		exists[f] = true
	}
	reasons := map[string]string{
		"README.md":       "what the repository is",
		"ARCHITECTURE.md": "the architecture in the team's words",
		"CONTRIBUTING.md": "how the team works",
		"docs/README.md":  "the documentation's front page",
		"archyl.yaml":     "the architecture as code",
	}
	var out []File
	for _, name := range overviewNames {
		if exists[name] {
			out = append(out, File{Path: name, Reason: reasons[name]})
		}
	}
	return out
}

// noise is what a newcomer should not start with: tests, generated and
// vendored code, lock files and assets.
func noise(f string) bool {
	lower := strings.ToLower(f)
	for _, part := range []string{"/vendor/", "/node_modules/", "/testdata/", "/__tests__/", "/mocks/", "/dist/", "/build/", "/generated/"} {
		if strings.Contains("/"+lower, part) {
			return true
		}
	}
	for _, part := range []string{"/k8s/", "/helm/", "/charts/", "/deploy/", "/deployments/", "/.github/", "/kustomize/"} {
		if strings.Contains("/"+lower, part) {
			return true
		}
	}
	base := path.Base(lower)
	switch base {
	case "build", "build.bazel", "workspace", "workspace.bazel", "module.bazel", "makefile", "go.work",
		"tsconfig.json", ".gitignore", ".dockerignore", "kustomization.yaml":
		return true
	}
	if strings.HasPrefix(base, "values") && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
		return true
	}
	switch {
	case strings.HasSuffix(base, ".swagger.json"), strings.HasSuffix(base, ".bzl"), strings.HasSuffix(base, "_pb2.py"),
		strings.HasSuffix(base, ".pb.gw.go"), strings.HasSuffix(base, "_grpc.pb.go"):
		return true
	}
	switch {
	case strings.HasSuffix(base, "_test.go"), strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.HasSuffix(base, ".lock"), strings.HasSuffix(base, ".sum"), base == "package-lock.json",
		strings.Contains(base, ".pb."), strings.HasSuffix(base, ".gen.go"), strings.HasSuffix(base, "_gen.go"):
		return true
	}
	switch path.Ext(base) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".mp4", ".snap":
		return true
	}
	return false
}

// commonDir is the deepest folder holding every one of dirs.
func commonDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	common := strings.Split(strings.TrimPrefix(dirs[0], "./"), "/")
	for _, d := range dirs[1:] {
		segs := strings.Split(strings.TrimPrefix(d, "./"), "/")
		i := 0
		for i < len(common) && i < len(segs) && common[i] == segs[i] {
			i++
		}
		common = common[:i]
	}
	return strings.Join(common, "/")
}
