package tui

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/tour"
	"github.com/archyl-com/cli/internal/ui"
)

func TestTour(
	t *testing.T,
) {
	g := sampleGraph()
	tr, _ := tour.Plan(g, tour.Options{Project: "Demo"})
	require.GreaterOrEqual(t, len(tr.Stops), 3)
	tr.Stops[1].Files = []tour.File{{Path: "billing/api/main.go", Reason: "entry point of billing/api/"}}

	var opened string
	view := NewTour(TourOptions{
		Tour:  tr,
		Graph: g,
		Theme: ui.NewTheme(true),
		Link:  func(id string) string { return "https://app/" + id },
		Open:  func(url string) error { opened = url; return nil },
	})
	var m tea.Model = view
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	first := screen(t, m)
	assert.Contains(t, first, "The big picture")
	assert.Contains(t, first, "1/"+strconv.Itoa(len(tr.Stops)))
	assert.Contains(t, first, "next")

	m, _ = m.Update(key("n"))
	second := screen(t, m)
	assert.Contains(t, second, tr.Stops[1].Name)
	assert.Contains(t, second, "Start here")
	assert.Contains(t, second, "billing/api/main.go")

	m, _ = m.Update(key("o"))
	assert.Equal(t, "https://app/"+tr.Stops[1].ElementID, opened)

	m, _ = m.Update(key("p"))
	assert.Contains(t, screen(t, m), "The big picture", "p goes back")

	m, _ = m.Update(key("G"))
	last := screen(t, m)
	assert.Contains(t, last, "Recap")
	assert.Contains(t, last, "archyl brief <files>")
	assert.Contains(t, last, "done")

	_, cmd := m.Update(key("q"))
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}
