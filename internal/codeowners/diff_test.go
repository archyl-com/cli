package codeowners

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiff(
	t *testing.T,
) {
	numbered := func(from, to int) string {
		var b strings.Builder
		for i := from; i <= to; i++ {
			b.WriteString("line" + string(rune('a'+i)) + "\n")
		}
		return b.String()
	}
	testCases := []struct {
		name   string
		before string
		after  string
		want   string
	}{
		{
			name:   "equal texts",
			before: "a\nb\n",
			after:  "a\nb\n",
		},
		{
			name:   "line endings are ignored",
			before: "a\r\nb\r\n",
			after:  "a\nb\n",
		},
		{
			name:   "a new file",
			before: "",
			after:  "a\nb\n",
			want:   "--- a/F\n+++ b/F\n@@ -0,0 +1,2 @@\n+a\n+b\n",
		},
		{
			name:   "a changed line with context",
			before: "a\nb\nc\nd\ne\n",
			after:  "a\nb\nC\nd\ne\n",
			want:   "--- a/F\n+++ b/F\n@@ -1,5 +1,5 @@\n a\n b\n-c\n+C\n d\n e\n",
		},
		{
			name:   "distant changes make two hunks",
			before: numbered(0, 11),
			after:  strings.Replace(strings.Replace(numbered(0, 11), "linea\n", "lineA\n", 1), "linel\n", "lineL\n", 1),
			want: "--- a/F\n+++ b/F\n" +
				"@@ -1,4 +1,4 @@\n-linea\n+lineA\n lineb\n linec\n lined\n" +
				"@@ -9,4 +9,4 @@\n linei\n linej\n linek\n-linel\n+lineL\n",
		},
		{
			name:   "close changes share a hunk",
			before: "a\nb\nc\nd\n",
			after:  "A\nb\nc\nD\n",
			want:   "--- a/F\n+++ b/F\n@@ -1,4 +1,4 @@\n-a\n+A\n b\n c\n-d\n+D\n",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Unified("a/F", "b/F", Diff(testCase.before, testCase.after)))
		})
	}
}
