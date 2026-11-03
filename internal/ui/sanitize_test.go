package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStripControl(
	t *testing.T,
) {
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text is untouched", in: "Orders API · ✓", want: "Orders API · ✓"},
		{name: "newlines and tabs stay", in: "a\n\tb", want: "a\n\tb"},
		{name: "an escape sequence loses its escape", in: "x\x1b]0;pwned\x07y", want: "x]0;pwnedy"},
		{name: "cursor movement", in: "\x1b[2Jcleared", want: "[2Jcleared"},
		{name: "carriage return and C1 controls", in: "a\rb\u009bc", want: "abc"},
		{name: "line separators", in: "a b", want: "ab"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, StripControl(testCase.in))
		})
	}
}
