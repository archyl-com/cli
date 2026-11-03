package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/history"
	"github.com/archyl-com/cli/internal/ui"
)

// replayFrames are three versions of a small shop: the API alone, then a
// worker added, then now with the worker renamed.
func replayFrames(t *testing.T) []history.Frame {
	t.Helper()
	model := func(worker string) *api.Model {
		m := &api.Model{Systems: []api.System{{ID: "s", Name: "Shop", Containers: []api.Container{
			{ID: "api", Name: "Orders API", ContainerType: "api"},
		}}}}
		if worker != "" {
			m.Systems[0].Containers = append(m.Systems[0].Containers, api.Container{ID: "w", Name: worker, ContainerType: "worker"})
			m.Relationships = []api.Relationship{{ID: "r", SourceID: "api", TargetID: "w", Type: "calls", Description: "queues jobs"}}
		}
		return m
	}
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local)
	states := []history.State{
		{Version: &api.Version{Number: 4, CreatedAt: at, Author: "Ada"}, Model: model("")},
		{Version: &api.Version{Number: 5, CreatedAt: at.Add(time.Hour), Author: "Grace", Description: "Add the worker"}, Model: model("Worker")},
		{Model: model("Fulfilment Worker")},
	}
	frames := history.Replay(states, history.Scope{ElementID: "s", Inside: true})
	require.Len(t, frames, 3)
	return frames
}

func space() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
}

func TestHistoryReplay(
	t *testing.T,
) {
	h := NewHistory(HistoryOptions{Project: "Shop", Frames: replayFrames(t), Theme: ui.NewTheme(true), Note: "capped"})
	require.NotNil(t, h.Init(), "it starts playing")
	var m tea.Model = h
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})

	first := screen(t, m)
	assert.Contains(t, first, "REV 4")
	assert.Contains(t, first, "2026-09-01 10:00 · Ada")
	assert.Contains(t, first, "Where the replay starts.")
	assert.Contains(t, first, "capped")
	assert.Contains(t, first, "1/3")
	assert.Contains(t, first, "▶ 1.5s")
	assert.Contains(t, first, "Orders API")

	m, cmd := m.Update(historyTick{seq: h.seq})
	require.NotNil(t, cmd, "the next tick is scheduled")
	second := screen(t, m)
	assert.Contains(t, second, "REV 5")
	assert.Contains(t, second, "Add the worker · elements: 1 added; relationships: 1 added")
	assert.Contains(t, second, "━ + ━", "the new worker is marked")

	m, cmd = m.Update(historyTick{seq: h.seq - 1})
	assert.Nil(t, cmd, "a stale tick does nothing")
	assert.Contains(t, screen(t, m), "REV 5")

	m, cmd = m.Update(historyTick{seq: h.seq})
	assert.Nil(t, cmd, "the replay stops on its last frame")
	last := screen(t, m)
	assert.Contains(t, last, "now")
	assert.Contains(t, last, "elements: 1 modified")
	assert.Contains(t, last, "❚❚ paused")
	assert.Contains(t, last, "3/3")
}

func TestHistoryKeys(
	t *testing.T,
) {
	testCases := []struct {
		name  string
		keys  []tea.KeyPressMsg
		want  []string
		check func(t *testing.T, h *History)
	}{
		{
			name: "space pauses",
			keys: []tea.KeyPressMsg{space()},
			want: []string{"❚❚ paused", "space play"},
		},
		{
			name: "right steps forward and pauses",
			keys: []tea.KeyPressMsg{key("right")},
			want: []string{"REV 5", "2/3", "❚❚ paused"},
		},
		{
			name: "left steps back, never before the first",
			keys: []tea.KeyPressMsg{key("right"), key("left"), key("left")},
			want: []string{"REV 4", "1/3"},
		},
		{
			name: "G jumps to the end, space replays from the start",
			keys: []tea.KeyPressMsg{key("G"), space()},
			want: []string{"REV 4", "▶"},
		},
		{
			name: "plus speeds up",
			keys: []tea.KeyPressMsg{key("+")},
			want: []string{"▶ 1s"},
			check: func(t *testing.T, h *History) {
				assert.Equal(t, time.Second, h.interval)
			},
		},
		{
			name: "minus slows down, up to a limit",
			keys: []tea.KeyPressMsg{key("-"), key("-"), key("-"), key("-"), key("-"), key("-"), key("-")},
			check: func(t *testing.T, h *History) {
				assert.Equal(t, maxFrameInterval, h.interval)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			h := NewHistory(HistoryOptions{Project: "Shop", Frames: replayFrames(t), Theme: ui.NewTheme(false)})
			var m tea.Model = h
			m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
			for _, k := range testCase.keys {
				m, _ = m.Update(k)
			}
			out := screen(t, m)
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			if testCase.check != nil {
				testCase.check(t, h)
			}
		})
	}
}

func TestHistoryQuits(
	t *testing.T,
) {
	h := NewHistory(HistoryOptions{Frames: replayFrames(t), Theme: ui.NewTheme(true)})
	_, cmd := h.Update(key("q"))
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestHistorySingleFrame(
	t *testing.T,
) {
	frames := history.Replay([]history.State{{Model: &api.Model{}}}, history.Scope{})
	h := NewHistory(HistoryOptions{Frames: frames, Theme: ui.NewTheme(true)})
	assert.Nil(t, h.Init(), "nothing to play")
	var m tea.Model = h
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	out := screen(t, m)
	assert.Contains(t, out, "The model is empty at this version.")
	assert.Contains(t, out, "❚❚ paused")
}
