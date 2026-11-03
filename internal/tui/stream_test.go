package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/ui"
)

func TestStream(
	t *testing.T,
) {
	chunks := make(chan StreamChunk)
	var m tea.Model = NewStream(ui.NewTheme(true), true, "ask", chunks)
	assert.Contains(t, screen(t, m), "thinking")

	m, _ = m.Update(StreamChunk{Text: "The **Harness** "})
	m, _ = m.Update(StreamChunk{Text: "opens sessions."})
	assert.Contains(t, screen(t, m), "writing")
	assert.Contains(t, screen(t, m), "Harness opens sessions.", "markdown is rendered, not shown raw")

	m, cmd := m.Update(StreamChunk{Done: true})
	assert.NotNil(t, cmd)
	assert.NotContains(t, screen(t, m), "writing")
	assert.Equal(t, "The **Harness** opens sessions.", m.(*Stream).Text())
}
