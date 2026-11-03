// Package dslfile reads an archyl.yaml with the files its include
// directives reach, the way the server merges them.
package dslfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Limits the server enforces on a file set, checked here before anything is
// read into memory or sent.
const (
	// MaxFiles bounds how many files one root may include.
	MaxFiles = 200
	// MaxFileBytes caps one file.
	MaxFileBytes = 5 << 20
	// MaxTotalBytes caps the files together.
	MaxTotalBytes = 50 << 20
)

// ErrTooManyFiles is returned when the includes reach past MaxFiles.
var ErrTooManyFiles = fmt.Errorf("more than %d included files", MaxFiles)

// Source is a root file and the files it includes, keyed by their path
// relative to the base directory, with forward slashes.
type Source struct {
	Content string            `json:"content"`
	Path    string            `json:"path,omitempty"`
	Files   map[string]string `json:"files,omitempty"`
}

// Load reads the file at file and every file its include directives reach.
// Includes resolve against the directory of the file declaring them and may
// not leave base, usually the repository root. A file outside base is read
// alone, its includes resolved against its own directory.
func Load(base, file string) (*Source, error) {
	rel, err := filepath.Rel(base, file)
	inside := err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	if !inside {
		base, rel = filepath.Dir(file), filepath.Base(file)
	}
	// The repository names this file, so its content must really be in it. A
	// file outside it was named by hand.
	if info, err := os.Lstat(file); inside && err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symbolic link; it was not read", file)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file, err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, fmt.Errorf("%s is empty", file)
	}

	src := &Source{Content: string(raw), Path: filepath.ToSlash(rel)}

	total := len(raw)
	seen := map[string]bool{src.Path: true}
	queue := []string{src.Path}
	contents := map[string]string{src.Path: src.Content}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, include := range includes(contents[from]) {
			resolved, ok := resolve(from, include)
			if !ok {
				return nil, fmt.Errorf("%s includes %q, which is outside %s", from, include, base)
			}
			if seen[resolved] {
				continue
			}
			seen[resolved] = true
			if len(seen) > MaxFiles+1 {
				return nil, ErrTooManyFiles
			}
			body, err := readIncluded(filepath.Join(base, filepath.FromSlash(resolved)))
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%s includes %s, which does not exist", from, resolved)
			}
			if err != nil {
				return nil, fmt.Errorf("%s includes %s: %w", from, resolved, err)
			}
			if total += len(body); total > MaxTotalBytes {
				return nil, fmt.Errorf("the included files are larger than %d MiB together", MaxTotalBytes>>20)
			}
			if src.Files == nil {
				src.Files = map[string]string{}
			}
			src.Files[resolved] = string(body)
			contents[resolved] = string(body)
			queue = append(queue, resolved)
		}
	}
	return src, nil
}

// readIncluded reads an included file. Only a regular file is read: a
// symbolic link could point outside the repository (a key, a .env file) and
// a device or pipe could block, and the content is sent to the server.
func readIncluded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file: symbolic links and special files are not included")
	}
	if info.Size() > MaxFileBytes {
		return nil, fmt.Errorf("larger than %d MiB", MaxFileBytes>>20)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
}

// includes lists a file's include directives; a file that does not parse has
// none here, and the server reports it.
func includes(content string) []string {
	var head struct {
		Include []string `yaml:"include"`
	}
	if err := yaml.Unmarshal([]byte(content), &head); err != nil {
		return nil
	}
	return head.Include
}

// resolve joins an include to the directory of the file declaring it. ok is
// false for an absolute path or one that climbs out of the base.
func resolve(from, include string) (string, bool) {
	include = strings.ReplaceAll(strings.TrimSpace(include), `\`, "/")
	if include == "" || strings.HasPrefix(include, "/") || strings.Contains(include, ":") {
		return "", false
	}
	resolved := path.Clean(path.Join(path.Dir(from), include))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") {
		return "", false
	}
	return resolved, true
}
