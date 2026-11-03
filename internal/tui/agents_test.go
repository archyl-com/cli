package tui

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func TestAgents(
	t *testing.T,
) {
	now := time.Now()
	sessions := []api.Session{
		{ID: "a", AgentName: "claude-code", TaskText: "Add refunds", Status: "active", Gate: "allow", StartedAt: now.Add(-12 * time.Minute),
			Leases: []api.Lease{{ElementName: "Payments", ElementType: "component", Source: "declared"}}},
		{ID: "b", AgentName: "codex", TaskText: "Fix flaky checkout test", Status: "active", Gate: "warn", StartedAt: now.Add(-3 * time.Minute),
			Leases: []api.Lease{{ElementName: "Payments", ElementType: "component", Source: "observed"}}},
	}
	m := NewAgents(AgentsOptions{
		Theme: ui.NewTheme(true), Project: "Shop",
		Sessions: func(context.Context, bool) ([]api.Session, error) { return sessions, nil },
		Files: func(context.Context, string) ([]api.SessionFile, error) {
			return []api.SessionFile{{Path: "internal/payments/refund.go", Status: "added"}}, nil
		},
	})
	var model tea.Model = m
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	model, _ = model.Update(agentsLoaded{seq: 0, sessions: sessions})
	model, _ = model.Update(filesLoaded{id: "b", files: []api.SessionFile{{Path: "internal/payments/refund.go", Status: "added"}}})

	view := screen(t, model)
	assert.Contains(t, view, "2 at work")
	assert.Contains(t, view, "leased by several agents: Payments", "two agents on one element is called out")
	assert.Equal(t, "codex", m.current().AgentName, "the most recent session comes first")
	assert.Contains(t, view, "also claude-code")
	assert.Contains(t, view, "+ internal/payments/refund.go")

	_, _ = model.Update(key("down"))
	assert.Equal(t, "claude-code", m.current().AgentName)
}
