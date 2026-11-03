// Package activity merges what happens on a project — model changes, change
// requests, agent runs, work sessions — into one time-ordered feed.
package activity

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/api"
)

// Kind groups events for coloring and filtering.
type Kind string

const (
	KindModel   Kind = "model"
	KindRequest Kind = "request"
	KindRun     Kind = "run"
	KindSession Kind = "session"
)

// Event is one line of the feed.
type Event struct {
	Key    string    `json:"key"`
	At     time.Time `json:"at"`
	Kind   Kind      `json:"kind"`
	Actor  string    `json:"actor"`
	Verb   string    `json:"verb"`
	Object string    `json:"object"`
	// Level is the C4 level of a model element, for its glyph.
	Level  string `json:"level,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Ref is what to act on: a run or session ID, an element ID.
	Ref       string `json:"ref,omitempty"`
	NeedsUser bool   `json:"needsUser,omitempty"`
}

var verbs = map[string]string{
	"create": "created", "update": "changed", "delete": "deleted",
	"open": "opened", "close": "closed", "reopen": "reopened", "merge": "merged",
}

var levels = map[string]string{
	"system": "system", "container": "container", "component": "component",
	"code_element": "code", "code": "code",
}

// FromHistory turns history entries into events.
func FromHistory(entries []api.HistoryEntry) []Event {
	out := make([]Event, 0, len(entries))
	for _, h := range entries {
		kind := KindModel
		if strings.Contains(h.EntityType, "request") || h.EntityType == "arch_pr" {
			kind = KindRequest
		}
		e := Event{
			Key: "h:" + h.ID, At: h.CreatedAt, Kind: kind, Actor: h.UserName,
			Verb: firstNonEmpty(verbs[h.Action], h.Action), Object: h.EntityName,
			Level: levels[h.EntityType], Ref: h.EntityID,
		}
		if e.Level == "" {
			e.Detail = strings.ReplaceAll(h.EntityType, "_", " ")
		}
		if fields := changedFields(h.Changes); fields != "" {
			e.Detail = strings.TrimSpace(e.Detail + " " + fields)
		}
		out = append(out, e)
	}
	return out
}

// changedFields lists the fields of an update: "(description, technologies)".
func changedFields(changes string) string {
	var diff struct {
		Fields []struct {
			Field string `json:"field"`
		} `json:"fields"`
	}
	if json.Unmarshal([]byte(changes), &diff) != nil || len(diff.Fields) == 0 {
		return ""
	}
	names := make([]string, 0, len(diff.Fields))
	for _, f := range diff.Fields {
		names = append(names, f.Field)
	}
	return "(" + strings.Join(names, ", ") + ")"
}

// FromRuns turns agent runs into events, one per state they reached.
func FromRuns(runs []api.Run) []Event {
	var out []Event
	for _, r := range runs {
		task := truncate(r.TaskText, 70)
		add := func(state string, at *time.Time, verb string, needsUser bool) {
			if at == nil || at.IsZero() {
				return
			}
			out = append(out, Event{Key: "r:" + r.ID + ":" + state, At: *at, Kind: KindRun, Actor: "agent run",
				Verb: verb, Object: task, Ref: r.ID, NeedsUser: needsUser})
		}
		created := r.CreatedAt
		add("created", &created, "queued", false)
		add("started", r.StartedAt, "started", false)
		if r.Status == "waiting_for_input" || r.Status == "awaiting_approval" {
			add("waiting", firstTime(r.WaitingSince, &created), "is waiting for you on", true)
		}
		if r.Ended() {
			verb := map[string]string{"succeeded": "finished", "failed": "failed", "cancelled": "was cancelled"}[r.Status]
			add(r.Status, r.EndedAt, verb, false)
		}
	}
	return out
}

// FromSessions turns work sessions into events: started, and how they ended.
func FromSessions(sessions []api.Session) []Event {
	var out []Event
	for _, s := range sessions {
		started := s.StartedAt
		out = append(out, Event{Key: "s:" + s.ID + ":start", At: started, Kind: KindSession, Actor: s.AgentName,
			Verb: "started working on", Object: truncate(s.TaskText, 70), Ref: s.ID})
		if s.EndedAt != nil {
			verb := map[string]string{"finished": "finished", "cancelled": "dropped", "expired": "went silent on"}[s.Status]
			out = append(out, Event{Key: "s:" + s.ID + ":" + s.Status, At: *s.EndedAt, Kind: KindSession, Actor: s.AgentName,
				Verb: firstNonEmpty(verb, s.Status), Object: truncate(s.TaskText, 70), Ref: s.ID})
		}
	}
	return out
}

// Feed accumulates events and hands out the ones not seen yet, oldest first.
type Feed struct {
	seen map[string]bool
}

// NewFeed returns an empty feed.
func NewFeed() *Feed {
	return &Feed{seen: map[string]bool{}}
}

// Fresh returns the events not returned before, sorted by time.
func (f *Feed) Fresh(events []Event) []Event {
	var out []Event
	for _, e := range events {
		if f.seen[e.Key] {
			continue
		}
		f.seen[e.Key] = true
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func firstTime(values ...*time.Time) *time.Time {
	for _, v := range values {
		if v != nil && !v.IsZero() {
			return v
		}
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
