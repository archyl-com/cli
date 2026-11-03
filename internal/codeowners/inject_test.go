package codeowners

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidHandle(t *testing.T) {
	for _, ok := range []string{"@acme/platform", "@ada", "ada@acme.example"} {
		assert.True(t, validHandle(ok), ok)
	}
	for _, bad := range []string{"@acme/t\n* @attacker", "@a b", "* @x"} {
		assert.False(t, validHandle(bad), bad)
	}
}

func TestDescribeStaysOnOneLine(t *testing.T) {
	text := describe(Entry{Elements: []ElementRef{{Name: "Pay\n* @attacker\n#", Level: "component"}}})
	assert.NotContains(t, text, "\n")
}
