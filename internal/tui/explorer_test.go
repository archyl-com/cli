package tui

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

func sampleGraph() *graph.Graph {
	return graph.New(&api.Model{
		Systems: []api.System{{
			ID: "s", Name: "Billing", Type: "software_system", Description: "Bills customers every month and collects payments.",
			Containers: []api.Container{
				{ID: "api", Name: "API", ContainerType: "api", Technologies: []api.Technology{{Name: "Go"}}, Components: []api.Component{
					{ID: "inv", Name: "Invoicing Service", ComponentType: "service", FilePath: "internal/invoicing"},
					{ID: "pay", Name: "Payments", ComponentType: "service"},
				}},
				{ID: "web", Name: "Checkout Web", ContainerType: "web_app"},
			},
		}},
		Relationships: []api.Relationship{
			{ID: "r1", SourceID: "web", TargetID: "inv", Type: "calls", Description: "creates invoices"},
			{ID: "r2", SourceID: "inv", TargetID: "pay", Type: "uses"},
		},
	})
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func screen(t *testing.T, m tea.Model) string {
	t.Helper()
	return ansi.Strip(m.View().Content)
}

func TestExplorerNavigation(
	t *testing.T,
) {
	e := NewExplorer(ExplorerOptions{Project: "Demo", Graph: sampleGraph(), Theme: ui.NewTheme(true)})
	var m tea.Model = e
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	assert.Equal(t, "Billing", e.current().Name, "opens on the system")
	assert.Contains(t, screen(t, m), "API", "a lone system opens expanded")

	for _, k := range []string{"down", "right", "right"} {
		m, _ = m.Update(key(k))
	}
	assert.Equal(t, "Invoicing Service", e.current().Name)
	view := screen(t, m)
	assert.Contains(t, view, "internal/invoicing")
	assert.Contains(t, view, "USED BY  1")
	assert.Contains(t, view, "creates invoices")

	m, _ = m.Update(key("u"))
	assert.Equal(t, "Payments", e.current().Name, "u follows the first relationship out")
	m, _ = m.Update(key("left"))
	assert.Equal(t, "API", e.current().Name, "left goes to the parent")
	m, _ = m.Update(key("left"))
	assert.NotContains(t, screen(t, m), "Payments", "left again collapses")

	m, _ = m.Update(key("/"))
	for _, r := range "chk" {
		m, _ = m.Update(key(string(r)))
	}
	assert.Equal(t, "Checkout Web", e.current().Name, "fuzzy filter")
	m, _ = m.Update(key("enter"))
	assert.Equal(t, "Checkout Web", e.current().Name)
	assert.False(t, e.filterOn)

	if os.Getenv("ARCHYL_PRINT_VIEW") != "" {
		m, _ = m.Update(key("up"))
		t.Log("\n" + m.View().Content)
	}
}

func TestExplorerFocus(
	t *testing.T,
) {
	e := NewExplorer(ExplorerOptions{Project: "Demo", Graph: sampleGraph(), Theme: ui.NewTheme(true), Focus: "pay"})
	require.NotNil(t, e.current())
	assert.Equal(t, "Payments", e.current().Name)
}

func TestExplorerGraphUpdate(
	t *testing.T,
) {
	e := NewExplorer(ExplorerOptions{Project: "Demo", Graph: sampleGraph(), Theme: ui.NewTheme(true), Focus: "pay"})
	var m tea.Model = e
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	updated := sampleGraph()
	updated.Roots[0].Children[0].Children[1].Name = "Payments Gateway"
	m, _ = m.Update(GraphUpdatedMsg{Graph: updated})

	require.NotNil(t, e.current())
	assert.Equal(t, "Payments Gateway", e.current().Name, "the selection follows the element")
	view := screen(t, m)
	assert.Contains(t, view, "model updated")
	assert.Contains(t, view, "Invoicing Service", "expanded elements stay expanded")

	m, _ = m.Update(key("down"))
	assert.NotContains(t, screen(t, m), "model updated", "the hint goes with the next key")
}
