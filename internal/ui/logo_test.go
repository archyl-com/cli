package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func TestLogo(
	t *testing.T,
) {
	logo := ansi.Strip(LogoWith(NewTheme(true), true, "archyl", "", "v1.0.0"))
	lines := strings.Split(logo, "\n")
	assert.Len(t, lines, len(logoRows))
	assert.Equal(t, "   ▄██▄", lines[0], "the dome stands alone")
	assert.Equal(t, "  ▄▄▄▄▄▄     archyl", lines[1], "text starts beside the second row, aligned")
	assert.Equal(t, "  ▀▀  ▀▀", lines[2], "a row without text has no trailing space")
	assert.True(t, strings.HasSuffix(lines[3], "   v1.0.0"))
	assert.Len(t, logoLayer, len(logoRows))
	for _, row := range logoRows {
		assert.LessOrEqual(t, len([]rune(row)), logoWidth)
	}
}
