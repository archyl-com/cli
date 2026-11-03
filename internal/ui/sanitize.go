package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// StripControl removes control characters from text that comes from outside
// the CLI (AI output, notes written by agents, names in the model) before it
// is printed: an escape sequence in it could otherwise move the cursor, retitle
// the window or rewrite what is already on screen. Newlines and tabs stay.
func StripControl(s string) string {
	if strings.IndexFunc(s, isUnsafe) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isUnsafe(r) {
			return -1
		}
		return r
	}, s)
}

func isUnsafe(r rune) bool {
	return r != '\n' && r != '\t' && (unicode.IsControl(r) || r == '\u2028' || r == '\u2029' || isBidi(r))
}

// isBidi matches the overrides that make text read differently than it is.
func isBidi(r rune) bool {
	return (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')
}

// Sanitize is StripControl for rendered output: the colors and weights of SGR
// sequences stay, and every other escape goes, payload included. Whatever the
// CLI prints passes through here, because the text in it comes from the model,
// from files and from AI answers.
func Sanitize(s string) string {
	if strings.IndexFunc(s, isUnsafe) < 0 {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			end, keep := escapeEnd(s, i)
			if keep {
				out.WriteString(s[i:end])
			}
			i = end
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isUnsafe(r) {
			out.WriteString(s[i : i+size])
		}
		i += size
	}
	return out.String()
}

// escapeEnd returns where the escape sequence at s[i] ends, and whether it is
// SGR.
func escapeEnd(s string, i int) (int, bool) {
	if i+1 >= len(s) {
		return len(s), false
	}
	switch s[i+1] {
	case '[':
		j := i + 2
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
			j++
		}
		if j == len(s) || s[j] < 0x40 || s[j] > 0x7e {
			return j, false
		}
		sgr := s[j] == 'm' && strings.Trim(s[i+2:j], "0123456789;:") == ""
		return j + 1, sgr
	case ']', 'P', 'X', '^', '_':
		return stringEnd(s, i+2, s[i+1] == ']'), false
	}
	return i + 2, false
}

// stringEnd finds the terminator of an OSC, DCS, SOS, PM or APC string. Without
// one the rest of the text is the string, as a terminal would treat it.
func stringEnd(s string, from int, bel bool) int {
	for j := from; j < len(s); j++ {
		switch {
		case bel && s[j] == 0x07:
			return j + 1
		case s[j] == 0x1b:
			if j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
			return j
		}
	}
	return len(s)
}
