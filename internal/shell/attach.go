package shell

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// What an @file mention attaches. The chat API takes 32 KB per message, so
// the files of one question share a little less than that with it.
const (
	maxAttachLines = 400
	maxAttachBytes = 24 << 10
	maxAttachTotal = 28 << 10
	// maxMessage is the chat API's limit on one message.
	maxMessage = 32 << 10
)

// Attachment is a mentioned file as the assistant receives it.
type Attachment struct {
	Path string
	// Content is what is attached; empty when Skipped says why not.
	Content string
	Lines   int // lines attached
	Total   int // lines in the file
	Skipped string
}

// Note is the line the session prints about the attachment.
func (a Attachment) Note() string {
	switch {
	case a.Skipped != "":
		return fmt.Sprintf("skipped %s (%s)", a.Path, a.Skipped)
	case a.Lines < a.Total:
		return fmt.Sprintf("attached %s (first %d of %d lines)", a.Path, a.Lines, a.Total)
	default:
		return fmt.Sprintf("attached %s (%d %s)", a.Path, a.Total, plural(a.Total, "line", "lines"))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Attach reads the mentioned files, within the repository, up to budget
// bytes in all.
func Attach(root string, paths []string, budget int) []Attachment {
	if root == "" {
		root, _ = os.Getwd()
	}
	budget = min(budget, maxAttachTotal)
	seen := map[string]bool{}
	var out []Attachment
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		a := Attachment{Path: p}
		full, why := resolveInRepo(root, p)
		switch {
		case why != "":
			a.Skipped = why
		case budget < 512:
			a.Skipped = "attachment limit reached"
		default:
			a = readAttachment(full, p, min(maxAttachBytes, budget))
		}
		budget -= len(a.Content)
		out = append(out, a)
	}
	return out
}

// resolveInRepo finds a mentioned path, relative to the repository root or
// the working directory, and refuses anything outside the repository.
func resolveInRepo(root, p string) (string, string) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	candidates := []string{p}
	if !filepath.IsAbs(p) {
		candidates = []string{filepath.Join(root, p)}
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates, filepath.Join(cwd, p))
		}
	}
	for _, c := range candidates {
		real, err := filepath.EvalSymlinks(c)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(realRoot, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", "outside the repository"
		}
		info, err := os.Stat(real)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return "", "a directory"
		}
		return real, ""
	}
	return "", "not found"
}

func readAttachment(full, display string, maxBytes int) Attachment {
	a := Attachment{Path: display}
	f, err := os.Open(full)
	if err != nil {
		a.Skipped = "unreadable"
		return a
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReader(f)
	head, _ := r.Peek(8 << 10)
	if bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(trimPartialRune(head)) {
		a.Skipped = "binary"
		return a
	}

	var content strings.Builder
	taking := true
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			a.Total++
			if taking && a.Lines < maxAttachLines && content.Len()+len(line) <= maxBytes {
				content.WriteString(line)
				a.Lines++
			} else {
				taking = false
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			a.Skipped = "unreadable"
			return a
		}
	}
	if a.Total == 0 {
		a.Skipped = "empty"
		return a
	}
	if a.Lines == 0 {
		a.Skipped = "its first line is too long"
		return a
	}
	a.Content = content.String()
	return a
}

// trimPartialRune drops an incomplete UTF-8 sequence cut at the end of b.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return b
}

// attachmentsBlock is the attached files as the question carries them.
func attachmentsBlock(atts []Attachment) string {
	var b strings.Builder
	for _, a := range atts {
		if a.Skipped != "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n\nAttached files:\n")
		}
		lines := fmt.Sprintf("%d lines", a.Total)
		if a.Lines < a.Total {
			lines = fmt.Sprintf("lines 1-%d of %d", a.Lines, a.Total)
		}
		fmt.Fprintf(&b, "\n<file path=%q lines=%q>\n%s", a.Path, lines, a.Content)
		if !strings.HasSuffix(a.Content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("</file>\n")
	}
	return b.String()
}
