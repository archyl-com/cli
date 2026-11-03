package activity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
)

func TestFeed(
	t *testing.T,
) {
	t0 := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	at := func(m int) *time.Time { v := t0.Add(time.Duration(m) * time.Minute); return &v }

	history := FromHistory([]api.HistoryEntry{
		{ID: "h1", UserName: "Ada", Action: "update", EntityType: "component", EntityName: "Payments",
			Changes: `{"fields":[{"field":"description"},{"field":"technologies"}]}`, CreatedAt: *at(5)},
		{ID: "h2", UserName: "Ada", Action: "merge", EntityType: "arch_pr", EntityName: "Split billing", CreatedAt: *at(1)},
	})
	require.Len(t, history, 2)
	assert.Equal(t, "changed", history[0].Verb)
	assert.Equal(t, "component", history[0].Level)
	assert.Equal(t, "(description, technologies)", history[0].Detail)
	assert.Equal(t, KindRequest, history[1].Kind)

	runs := FromRuns([]api.Run{
		{ID: "r1", TaskText: "Add refunds", Status: "waiting_for_input", CreatedAt: *at(2), StartedAt: at(3), WaitingSince: at(4)},
	})
	require.Len(t, runs, 3)
	assert.True(t, runs[2].NeedsUser)

	feed := NewFeed()
	first := feed.Fresh(append(history, runs...))
	require.Len(t, first, 5)
	assert.Equal(t, "h2", first[0].Key[2:], "oldest first")

	again := feed.Fresh(FromRuns([]api.Run{
		{ID: "r1", TaskText: "Add refunds", Status: "succeeded", CreatedAt: *at(2), StartedAt: at(3), EndedAt: at(9)},
	}))
	require.Len(t, again, 1, "only the new state of a known run is new")
	assert.Equal(t, "finished", again[0].Verb)
}
