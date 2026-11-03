package codeowners

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines surround a change.
const diffContext = 3

// Op is what a diff line does.
type Op byte

// The diff operations, as unified diffs mark them.
const (
	Same   Op = ' '
	Delete Op = '-'
	Insert Op = '+'
)

// DiffLine is one line of a diff.
type DiffLine struct {
	Op   Op
	Text string
}

// Diff compares two texts line by line, ignoring line endings, and returns
// the changes with diffContext lines around them, hunks headed by @@ lines.
// Equal texts have no diff.
func Diff(before, after string) []DiffLine {
	a, b := lines(before), lines(after)
	ops := align(a, b)
	return hunks(ops)
}

// Unified renders a diff as text under ---/+++ headers.
func Unified(
	from string,
	to string,
	diff []DiffLine,
) string {
	if len(diff) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- " + from + "\n+++ " + to + "\n")
	for _, l := range diff {
		if l.Op == 0 {
			b.WriteString(l.Text + "\n")
			continue
		}
		b.WriteString(string(l.Op) + l.Text + "\n")
	}
	return b.String()
}

func lines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// edit is an aligned line with its position in each text.
type edit struct {
	op     Op
	text   string
	ai, bi int
}

// align finds a longest common subsequence; CODEOWNERS files are small
// enough for the quadratic table.
func align(a, b []string) []edit {
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []edit
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, edit{Same, a[i], i, j})
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] > lcs[i+1][j]):
			out = append(out, edit{Insert, b[j], i, j})
			j++
		default:
			out = append(out, edit{Delete, a[i], i, j})
			i++
		}
	}
	return out
}

// hunks keeps the changes with their context, under @@ headers.
func hunks(ops []edit) []DiffLine {
	var out []DiffLine
	for start := 0; start < len(ops); {
		first := nextChange(ops, start)
		if first < 0 {
			break
		}
		from := max(first-diffContext, start)
		to := first
		// Extend the hunk while the next change is close enough to share
		// its context.
		for to < len(ops) {
			next := nextChange(ops, to+1)
			if next < 0 || next-to > 2*diffContext {
				break
			}
			to = next
		}
		end := min(to+diffContext+1, len(ops))
		out = append(out, DiffLine{Text: header(ops[from:end])})
		for _, e := range ops[from:end] {
			out = append(out, DiffLine{Op: e.op, Text: e.text})
		}
		start = end
	}
	return out
}

func nextChange(ops []edit, from int) int {
	for i := from; i < len(ops); i++ {
		if ops[i].op != Same {
			return i
		}
	}
	return -1
}

// header is the hunk's @@ -a,n +b,m @@ line, with 1-based line numbers.
func header(hunk []edit) string {
	var aLen, bLen int
	for _, e := range hunk {
		if e.op != Insert {
			aLen++
		}
		if e.op != Delete {
			bLen++
		}
	}
	return fmt.Sprintf("@@ -%s +%s @@", span(hunk[0].ai, aLen), span(hunk[0].bi, bLen))
}

func span(start, length int) string {
	if length == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	return fmt.Sprintf("%d,%d", start+1, length)
}
