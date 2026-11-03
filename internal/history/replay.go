package history

import (
	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/diagram"
)

// MaxFrames caps how many versions a replay plays.
const MaxFrames = 60

// MaxBoxes is the most boxes a history diagram draws; the changed ones are
// kept first.
const MaxBoxes = 30

// State is the model at a version, or now when Version is nil.
type State struct {
	Version *api.Version
	Model   *api.Model
}

// Frame is one step of a replay: the model at a version with what changed
// since the step before marked.
type Frame struct {
	// Version is nil for now.
	Version *api.Version
	Summary Summary
	View    *diagram.View
}

// Pick lists the versions a replay from..to plays, oldest first: every
// version between them, both included. Past MaxFrames it keeps the first
// and the latest ones; total is how many the range holds.
func Pick(
	versions []api.Version,
	from Resolved,
	to Resolved,
) (picked []api.Version, total int) {
	var out []api.Version
	for _, v := range versions {
		if v.Number >= from.Number() && (to.IsNow() || v.Number <= to.Number()) {
			out = append(out, v)
		}
	}
	limit := MaxFrames
	if to.IsNow() {
		// Now takes a frame of its own.
		limit--
	}
	if len(out) <= limit {
		return out, len(out)
	}
	return append(out[:1:1], out[len(out)-limit+1:]...), len(out)
}

// Replay turns consecutive states into frames, all drawn at one scope. The
// first frame shows the first state as it was; each next one marks what
// changed since the one before. A last state of now with nothing new is
// left out.
func Replay(states []State, scope Scope) []Frame {
	frames := make([]Frame, 0, len(states))
	for i, s := range states {
		prev := s.Model
		if i > 0 {
			prev = states[i-1].Model
		}
		d := Compare(prev, s.Model, Options{})
		if i > 0 && i == len(states)-1 && s.Version == nil && d.Empty() {
			break
		}
		v := diagram.Trim(d.Scene().View(scope), MaxBoxes)
		frames = append(frames, Frame{Version: s.Version, Summary: d.Summary(), View: v})
	}
	return frames
}
