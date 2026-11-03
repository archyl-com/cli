package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMdTextDefusesMarkdown(t *testing.T) {
	out := mdText("Api **b** [click](http://evil.example) @acme/sec <img src=x>\n# Forged")
	assert.NotContains(t, out, "\n")
	assert.NotRegexp(t, `(^|[^\\])[*\[<@#]`, out)
}

func TestInsecureHost(t *testing.T) {
	assert.True(t, insecureHost("http://archyl.corp.example"))
	assert.False(t, insecureHost("http://localhost:8081"))
	assert.False(t, insecureHost("https://archyl.corp.example"))
}
