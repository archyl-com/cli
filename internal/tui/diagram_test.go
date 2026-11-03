package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/ui"
)

func TestExplorerDiagram(
	t *testing.T,
) {
	e := NewExplorer(ExplorerOptions{Project: "Demo", Graph: sampleGraph(), Theme: ui.NewTheme(true), Focus: "inv"})
	var m tea.Model = e
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	m, _ = m.Update(key("d"))
	require.True(t, e.diag.on)
	view := screen(t, m)
	assert.Contains(t, view, "Components of API")
	assert.Contains(t, view, "┃ ◉ Invoicing Service", "the current element is selected")
	assert.Contains(t, view, "▣ Checkout Web", "neighbours outside the container show")
	assert.Contains(t, view, "drill down")

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	b := e.selectedBox()
	require.NotNil(t, b)
	assert.NotEqual(t, "inv", b.ID, "tab selects the next box")

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Contains(t, screen(t, m), "Containers of Billing", "backspace goes up a level")
	assert.Equal(t, "api", e.selectedBox().ID, "the container left is selected")

	m, _ = m.Update(key("enter"))
	assert.Contains(t, screen(t, m), "Components of API", "enter drills down")

	m, _ = m.Update(key("u"))
	m, _ = m.Update(key("u"))
	assert.Contains(t, screen(t, m), "System context")
	m, _ = m.Update(key("u"))
	assert.Contains(t, screen(t, m), "this is the system context")

	m, _ = m.Update(key("esc"))
	assert.False(t, e.diag.on)
	assert.Equal(t, "Billing", e.current().Name, "the tree follows the diagram's selection")
	assert.Contains(t, screen(t, m), "contains", "back to the tree and details")
	_ = m
}

func TestExplorerDiagramScroll(
	t *testing.T,
) {
	e := NewExplorer(ExplorerOptions{Project: "Demo", Graph: sampleGraph(), Theme: ui.NewTheme(true), Focus: "inv"})
	var m tea.Model = e
	m, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 14})
	m, _ = m.Update(key("d"))
	assert.Contains(t, screen(t, m), "scroll", "a diagram bigger than the screen says it scrolls")

	start := e.diag.y
	m, _ = m.Update(key("j"))
	m, _ = m.Update(key("l"))
	assert.Equal(t, start+1, e.diag.y)
	assert.Positive(t, e.diag.x)
	for range 50 {
		m, _ = m.Update(key("j"))
	}
	assert.LessOrEqual(t, e.diag.y, max(len(e.diag.lines)-e.diag.viewH, 0), "scrolling stops at the end")
	_ = m
}

func TestExplorerFocusDiagram(
	t *testing.T,
) {
	e := NewExplorer(ExplorerOptions{Project: "Demo", Graph: sampleGraph(), Theme: ui.NewTheme(true), Focus: "inv"})
	var m tea.Model = e
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	m, _ = m.Update(key("f"))
	require.True(t, e.diag.on)
	assert.Contains(t, screen(t, m), "Around Invoicing Service", "f centres the diagram on the element")
	assert.Equal(t, "inv", e.selectedBox().ID)

	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := e.selectedBox().Name
	m, _ = m.Update(key("f"))
	assert.Contains(t, screen(t, m), "Around "+next, "f on a box re-centres on it")

	m, _ = m.Update(key("u"))
	assert.NotContains(t, screen(t, m), "Around ", "u leaves the focus for the view the element sits in")
	_ = m
}
