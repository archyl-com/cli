package history

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
)

// numbered are the versions 1 to last, a day apart.
func numbered(last int) []api.Version {
	var out []api.Version
	for n := 1; n <= last; n++ {
		out = append(out, api.Version{Number: n, CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)})
	}
	return out
}

func TestPick(
	t *testing.T,
) {
	at := func(n int) Resolved {
		return Resolved{Version: &api.Version{Number: n}, Label: "REV"}
	}
	now := Resolved{Label: "now"}
	testCases := []struct {
		name      string
		versions  []api.Version
		from, to  Resolved
		wantFirst int
		wantLast  int
		wantCount int
		wantTotal int
	}{
		{name: "between two versions", versions: numbered(20), from: at(4), to: at(9), wantFirst: 4, wantLast: 9, wantCount: 6, wantTotal: 6},
		{name: "up to now", versions: numbered(20), from: at(15), to: now, wantFirst: 15, wantLast: 20, wantCount: 6, wantTotal: 6},
		{name: "capped, keeping the start and the latest", versions: numbered(100), from: at(1), to: at(100), wantFirst: 1, wantLast: 100, wantCount: MaxFrames, wantTotal: 100},
		{name: "capped up to now leaves room for it", versions: numbered(100), from: at(1), to: now, wantFirst: 1, wantLast: 100, wantCount: MaxFrames - 1, wantTotal: 100},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, total := Pick(testCase.versions, testCase.from, testCase.to)
			require.Len(t, got, testCase.wantCount)
			assert.Equal(t, testCase.wantFirst, got[0].Number)
			assert.Equal(t, testCase.wantLast, got[len(got)-1].Number)
			assert.Equal(t, testCase.wantTotal, total)
		})
	}
}

func TestReplay(
	t *testing.T,
) {
	v1, v2 := &api.Version{Number: 1}, &api.Version{Number: 2}
	middle := shop()
	middle.Systems = append(middle.Systems, api.System{ID: "wh", Name: "Warehouse", Type: "software_system"})
	testCases := []struct {
		name        string
		states      []State
		wantFrames  int
		wantSummary []Summary
	}{
		{
			name:        "each frame marks what changed since the one before",
			states:      []State{{Version: v1, Model: shop()}, {Version: v2, Model: middle}, {Model: shopAfter()}},
			wantFrames:  3,
			wantSummary: []Summary{{}, {Added: 1}, {Removed: 1, Modified: 2, RelationshipsAdded: 1, RelationshipsRemoved: 2}},
		},
		{
			name:        "now without news is left out",
			states:      []State{{Version: v1, Model: shop()}, {Version: v2, Model: middle}, {Model: middle}},
			wantFrames:  2,
			wantSummary: []Summary{{}, {Added: 1}},
		},
		{
			name:        "a single state",
			states:      []State{{Model: shop()}},
			wantFrames:  1,
			wantSummary: []Summary{{}},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			frames := Replay(testCase.states, Scope{ElementID: "shop", Inside: true})
			require.Len(t, frames, testCase.wantFrames)
			for i, f := range frames {
				assert.Equal(t, testCase.wantSummary[i], f.Summary, "frame %d", i)
				assert.Equal(t, "Containers of Shop", f.View.Title)
			}
			assert.False(t, frames[0].View.Marked(), "the first frame is the start as it was")
		})
	}
}

func TestReplayTrimsKeepingChanges(
	t *testing.T,
) {
	before := &api.Model{Systems: []api.System{{ID: "s", Name: "Big"}}}
	after := &api.Model{Systems: []api.System{{ID: "s", Name: "Big"}}}
	for i := range MaxBoxes + 10 {
		c := api.Container{ID: "c" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Name: "C"}
		before.Systems[0].Containers = append(before.Systems[0].Containers, c)
		after.Systems[0].Containers = append(after.Systems[0].Containers, c)
	}
	after.Systems[0].Containers = append(after.Systems[0].Containers, api.Container{ID: "new", Name: "New"})

	frames := Replay([]State{{Model: before}, {Model: after}}, Scope{ElementID: "s", Inside: true})

	require.Len(t, frames, 2)
	assert.Len(t, frames[1].View.Nodes, MaxBoxes)
	n := frames[1].View.Node("new")
	require.NotNil(t, n, "the added box survives the trim")
	assert.Equal(t, diagram.Added, n.Change)
}
