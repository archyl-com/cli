package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitize(t *testing.T) {
	testCases := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "hello\n\tworld", want: "hello\n\tworld"},
		{name: "sgr stays", in: "\x1b[1;38;5;99mbold\x1b[0m", want: "\x1b[1;38;5;99mbold\x1b[0m"},
		{name: "clipboard write", in: "a\x1b]52;c;cHduZWQ=\x07b", want: "ab"},
		{name: "title with st", in: "a\x1b]0;PWNED\x1b\\b", want: "ab"},
		{name: "hyperlink", in: "\x1b]8;;http://evil.example/\x07click\x1b]8;;\x07", want: "click"},
		{name: "cursor up and erase", in: "ok\x1b[2A\x1b[2K", want: "ok"},
		{name: "dcs", in: "a\x1bPpayload\x1b\\b", want: "ab"},
		{name: "unterminated osc", in: "a\x1b]52;c;secret", want: "a"},
		{name: "lone escape at end", in: "a\x1b", want: "a"},
		{name: "bell and carriage return", in: "a\x07\rb", want: "ab"},
		{name: "c1 csi", in: "a\u009b2Ab", want: "a2Ab"},
		{name: "bidi override", in: "a\u202eb", want: "ab"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Sanitize(testCase.in))
		})
	}
}
