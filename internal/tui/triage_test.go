package tui

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func pending(id, elementType string, data map[string]string) api.PendingDiscovery {
	raw, _ := json.Marshal(data)
	return api.PendingDiscovery{ID: id, ElementType: elementType, ElementData: raw, Status: "pending"}
}

func samplePending() []api.PendingDiscovery {
	return []api.PendingDiscovery{
		pending("rel", "relationship", map[string]string{"sourceName": "Checkout API", "targetName": "Orders DB", "description": "writes orders"}),
		pending("api", "container", map[string]string{"name": "Checkout API", "type": "api", "systemName": "Shop", "technology": "Go", "description": "Takes payments and creates orders."}),
		pending("shop", "system", map[string]string{"name": "Shop", "description": "The online shop."}),
		pending("db", "container", map[string]string{"name": "Orders DB", "type": "database", "systemName": "Shop"}),
	}
}

// press drives a key through Update and feeds back the message its command
// produces, the way the program loop would.
func press(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	m, cmd := m.Update(msg)
	if cmd != nil {
		if out := cmd(); out != nil {
			if _, ok := out.(triageDone); ok {
				m, _ = m.Update(out)
			}
		}
	}
	return m
}

func TestTriage(
	t *testing.T,
) {
	var approved, rejected []string
	m := NewTriage(TriageOptions{
		Theme: ui.NewTheme(true), Project: "Shop", Pending: samplePending(),
		Approve: func(_ context.Context, ids []string) error { approved = append(approved, ids...); return nil },
		Reject:  func(_ context.Context, ids []string) error { rejected = append(rejected, ids...); return nil },
	})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	view := screen(t, model)
	assert.Contains(t, view, "4 pending")
	assert.Contains(t, view, "SYSTEMS")
	assert.Contains(t, view, "CONTAINERS")
	assert.Contains(t, view, "RELATIONSHIPS")
	assert.Equal(t, "shop", m.current().ID, "systems come first")
	assert.Contains(t, view, "The online shop.", "the detail pane shows the current element")

	model = press(t, model, key("j"))
	assert.Equal(t, "api", m.current().ID)
	assert.Contains(t, screen(t, model), "Takes payments", "the detail follows the cursor")

	model = press(t, model, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	model = press(t, model, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	assert.Contains(t, screen(t, model), "2 selected")

	model = press(t, model, key("a"))
	assert.ElementsMatch(t, []string{"api", "db"}, approved, "a acts on the selection")
	assert.Equal(t, 2, m.Remaining())
	assert.Contains(t, screen(t, model), "Approved 2 elements")

	model = press(t, model, key("r"))
	require.Len(t, rejected, 1, "with nothing selected, r acts on the current element")
	assert.Equal(t, 1, m.Remaining())

	model = press(t, model, key("A"))
	assert.Equal(t, 0, m.Remaining())
	assert.Equal(t, 3, m.Approved)
	assert.Equal(t, 1, m.Rejected)
	assert.Contains(t, screen(t, model), "All reviewed.")
}

func TestTriageFilter(
	t *testing.T,
) {
	m := NewTriage(TriageOptions{Theme: ui.NewTheme(true), Project: "Shop", Pending: samplePending()})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	model = press(t, model, key("/"))
	for _, r := range "ordb" {
		model = press(t, model, key(string(r)))
	}
	assert.Equal(t, "db", m.current().ID, "fuzzy filter")
	assert.NotContains(t, screen(t, model), "SYSTEMS")

	model = press(t, model, key("esc"))
	assert.False(t, m.filterOn)
	assert.Contains(t, screen(t, model), "SYSTEMS")
}

func TestTriageShowsTheBackendError(
	t *testing.T,
) {
	m := NewTriage(TriageOptions{
		Theme: ui.NewTheme(true), Project: "Shop", Pending: samplePending(),
		Approve: func(context.Context, []string) error { return errors.New("you need edit access") },
	})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	model = press(t, model, key("a"))
	assert.Contains(t, screen(t, model), "you need edit access")
	assert.Equal(t, 4, m.Remaining(), "nothing leaves the list when the call fails")
}
