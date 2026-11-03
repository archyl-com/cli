// Package pathmatch matches repository folders to C4 elements by name: a
// folder belongs to an element when their names, lowercased and stripped of
// punctuation, are the same once a generic trailing word such as Service or
// API is dropped from either. It accepts what the Archyl server accepts when
// it attributes files to elements, and also folders that carry the generic
// word the element's name lacks (billing-api for "Billing") and two-letter
// names (ui, db).
package pathmatch

import (
	"strings"
	"unicode"
)

// genericSuffixes are words that say what an element is rather than what it
// is called. The server strips the same ones: "Orders Service" matches
// services/orders.
var genericSuffixes = []string{
	"service", "component", "handler", "controller", "manager", "repository",
	"adapter", "provider", "middleware", "worker", "client", "server", "store",
	"panel", "console", "view", "page", "canvas", "api", "module",
}

// Normalize lowercases s and keeps only its letters and digits.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// minLength is the shortest name compared at all: one letter matches too
// much by accident.
const minLength = 2

// Tokens are the comparable forms of a name: the whole name and, when it
// ends with a generic word, the name without it.
func Tokens(name string) map[string]bool {
	full := Normalize(name)
	if len(full) < minLength {
		return nil
	}
	tokens := map[string]bool{full: true}
	for _, suffix := range genericSuffixes {
		if len(full) > len(suffix) && strings.HasSuffix(full, suffix) {
			if trimmed := full[:len(full)-len(suffix)]; len(trimmed) >= 3 {
				tokens[trimmed] = true
			}
			break
		}
	}
	return tokens
}

// Matches reports whether a folder or file name belongs to the element name.
func Matches(segment, name string) bool {
	return overlap(Tokens(segment), Tokens(name))
}

func overlap(a, b map[string]bool) bool {
	for t := range a {
		if b[t] {
			return true
		}
	}
	return false
}

// words splits a name into its lowercased words: "transcoding-api" is
// transcoding and api.
func words(name string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		out = append(out, strings.ToLower(w))
	}
	return out
}

// Folder is the folder of files that best belongs to the element named
// name, "" when none does. A folder named after the whole name beats one
// named without its generic word, and a path holding every word of the name
// (api/transcoding for transcoding-api) beats both; then the shallowest
// wins, then the first in order.
func Folder(name string, files []string) string {
	tokens := Tokens(name)
	if len(tokens) == 0 {
		return ""
	}
	full := Normalize(name)
	nameWords := words(name)

	best, bestScore := "", 0
	consider := func(dir string, score int) {
		depth, bestDepth := strings.Count(dir, "/"), strings.Count(best, "/")
		switch {
		case score > bestScore,
			score == bestScore && depth < bestDepth,
			score == bestScore && depth == bestDepth && dir < best:
			best, bestScore = dir, score
		}
	}
	for _, f := range files {
		segs := strings.Split(f, "/")
		for i := 0; i < len(segs)-1; i++ {
			seg := Normalize(segs[i])
			if !overlap(Tokens(segs[i]), tokens) {
				continue
			}
			score := 1
			if seg == full {
				score = 2
			}
			if len(nameWords) > 1 && holdsAll(segs[:i+1], nameWords) {
				score = 3
			}
			consider(strings.Join(segs[:i+1], "/"), score)
			break
		}
	}
	return best
}

// holdsAll reports whether every word appears as one of the segments.
func holdsAll(segs, ws []string) bool {
	have := make(map[string]bool, len(segs))
	for _, s := range segs {
		have[Normalize(s)] = true
	}
	for _, w := range ws {
		if !have[w] {
			return false
		}
	}
	return true
}
