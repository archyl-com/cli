package tui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

func newTestDiscovery() *Discovery {
	m := NewDiscovery(DiscoveryOptions{
		Theme:      ui.NewTheme(true),
		Repository: "acme/shop",
		Job:        &api.DiscoveryJob{ID: "job", Status: "pending"},
		Status:     func(context.Context, string) (*api.DiscoveryJob, error) { return nil, errors.New("unused") },
		Pending:    func(context.Context) ([]api.PendingDiscovery, error) { return nil, nil },
	})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return m
}

func discovered() []api.PendingDiscovery {
	items := samplePending()
	for i := range items {
		items[i].DiscoveryJobID = "job"
	}
	items = append(items,
		pending("pay", "component", map[string]string{"name": "Payments", "containerName": "Checkout API"}),
		api.PendingDiscovery{ID: "old", DiscoveryJobID: "another", ElementType: "system", ElementData: []byte(`{"name":"Legacy"}`)},
	)
	items[len(items)-2].DiscoveryJobID = "job"
	return items
}

func TestDiscoveryStages(
	t *testing.T,
) {
	testCases := []struct {
		name     string
		job      api.DiscoveryJob
		want     []string
		wantNone []string
	}{
		{
			name: "early: reading",
			job:  api.DiscoveryJob{ID: "job", Status: "running", Progress: 5, CurrentStep: "Fetching repository structure..."},
			want: []string{"Discovering acme/shop", "Reading the repository", "Fetching repository structure", "5%", "q stop following"},
		},
		{
			name:     "AI analysis shows the chunk",
			job:      api.DiscoveryJob{ID: "job", Status: "running", Progress: 66, CurrentStep: "Phase 2: Analyzing chunk 3/7..."},
			want:     []string{"✓ Reading the repository", "✓ Picking and fetching the source files", "Phase 2: Analyzing chunk 3/7", "66%"},
			wantNone: []string{"Fetching repository structure"},
		},
		{
			name: "failure names the stage and the reason",
			job:  api.DiscoveryJob{ID: "job", Status: "failed", Progress: 5, Error: "failed to get tree: 404 Not Found"},
			want: []string{"✗ Reading the repository", "404 Not Found"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			m := newTestDiscovery()
			job := testCase.job
			m.Update(discoveryPolled{job: &job})
			view := screen(t, m)
			for _, s := range testCase.want {
				assert.Contains(t, view, s)
			}
			for _, s := range testCase.wantNone {
				assert.NotContains(t, view, s)
			}
		})
	}
}

func TestDiscoveryStageNeverGoesBack(
	t *testing.T,
) {
	m := newTestDiscovery()
	m.Update(discoveryPolled{job: &api.DiscoveryJob{ID: "job", Status: "running", Progress: 80}})
	m.Update(discoveryPolled{job: &api.DiscoveryJob{ID: "job", Status: "running", Progress: 78}})
	assert.Contains(t, screen(t, m), "✓ Mapping the architecture with AI", "the server reports 80 then 78")
}

func TestDiscoveryRevealsTheTree(
	t *testing.T,
) {
	m := newTestDiscovery()
	var model tea.Model = m
	model, cmd := model.Update(discoveryPolled{job: &api.DiscoveryJob{ID: "job", Status: "completed", Progress: 100}, pending: discovered()})
	assert.Nil(t, cmd, "a finished job is not polled again")
	assert.NotContains(t, screen(t, model), "Shop", "the tree cascades in, it does not appear at once")

	model, _ = model.Update(discoveryTick{})
	view := screen(t, model)
	assert.Contains(t, view, "◆ Shop")
	assert.NotContains(t, view, "Checkout API")
	assert.Contains(t, view, "1 system")

	var quit tea.Cmd
	for range 10 {
		model, quit = model.Update(discoveryTick{})
	}
	view = screen(t, model)
	assert.Contains(t, view, "├─ ▣ Checkout API")
	assert.Contains(t, view, "│  └─ ◉ Payments")
	assert.Contains(t, view, "└─ ▣ Orders DB")
	assert.NotContains(t, view, "Legacy", "another job's elements are not this discovery's")
	assert.Contains(t, view, "1 system · 2 containers · 1 component · 1 relationship")
	assert.Contains(t, view, "✓ Preparing the review")
	assert.NotContains(t, view, "q stop following", "the last frame stays in the scrollback without hints")
	require.NotNil(t, quit)
	assert.IsType(t, tea.QuitMsg{}, quit())
	assert.Len(t, m.Pending(), 5)
}

func TestDiscoveryDetach(
	t *testing.T,
) {
	m := newTestDiscovery()
	m.Update(discoveryPolled{job: &api.DiscoveryJob{ID: "job", Status: "running", Progress: 40}})
	_, cmd := m.Update(key("q"))
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.True(t, m.Detached, "q leaves the discovery running")
}

func TestDiscoveryPollFailures(
	t *testing.T,
) {
	m := newTestDiscovery()
	for i := range 2 {
		_, cmd := m.Update(discoveryPolled{err: errors.New("timeout")})
		require.NotNil(t, cmd, "poll %d is retried", i)
		assert.NoError(t, m.Err)
	}
	m.Update(discoveryPolled{err: errors.New("timeout")})
	assert.EqualError(t, m.Err, "timeout")
}

func TestDiscoveryTreeCapsComponents(
	t *testing.T,
) {
	var items []api.PendingDiscovery
	items = append(items, pending("api", "container", map[string]string{"name": "API"}))
	for _, name := range []string{"A", "B", "C", "D", "E", "F"} {
		items = append(items, pending(name, "component", map[string]string{"name": name, "containerName": "api"}))
	}
	lines := DiscoveryTree(TriageItems(items), 4)
	require.Len(t, lines, 5)
	assert.Equal(t, TreeLine{Prefix: "", Level: "container", Name: "API"}, lines[0], "an orphan container is a root")
	assert.Equal(t, "├─ ", lines[1].Prefix)
	assert.Equal(t, TreeLine{Prefix: "└─ ", Level: "component", More: 3}, lines[4])
}
