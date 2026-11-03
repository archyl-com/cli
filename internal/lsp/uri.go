package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// uriToPath turns a file:// URI into a local path; "" for other schemes.
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	path := u.Path
	// file:///C:/repo → C:/repo
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	if u.Host != "" && u.Host != "localhost" {
		path = "//" + u.Host + path
	}
	return filepath.FromSlash(path)
}

// pathToURI turns a local path into a file:// URI.
func pathToURI(path string) string {
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" || (len(path) >= 2 && path[1] == ':') {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// relPath places a file in the repository, as a slash path; "" outside it.
// Symlinks are resolved when the plain paths disagree: git reports the real
// root (/private/var on macOS) while editors keep the path they opened.
func relPath(root, path string) string {
	if root == "" || path == "" {
		return ""
	}
	if rel := lexicalRel(root, path); rel != "" {
		return rel
	}
	realRoot, err1 := filepath.EvalSymlinks(root)
	realPath, err2 := filepath.EvalSymlinks(path)
	if err1 != nil || err2 != nil {
		return ""
	}
	return lexicalRel(realRoot, realPath)
}

func lexicalRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}
