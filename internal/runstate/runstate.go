// Package runstate folds a managed agent run's event stream into what a
// person needs to follow and act on it: the agent's words, its tool calls,
// its plan, the question it is waiting on, the files it changed.
package runstate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/archyl-com/cli/internal/api"
)

// ItemKind is the kind of a feed item.
type ItemKind string

const (
	ItemText     ItemKind = "text"
	ItemTool     ItemKind = "tool"
	ItemFile     ItemKind = "file"
	ItemPlan     ItemKind = "plan"
	ItemQuestion ItemKind = "question"
	ItemAnswer   ItemKind = "answer"
	ItemSteering ItemKind = "steering"
	ItemStatus   ItemKind = "status"
	ItemResult   ItemKind = "result"
	ItemError    ItemKind = "error"
)

// Item is one entry of the run's feed.
type Item struct {
	Kind  ItemKind
	Title string
	Body  string
	Error bool
}

// Step is a plan step and its progress.
type Step struct {
	Title  string
	Detail string
	Status string // pending, in_progress, completed, skipped, failed
}

// Plan is the agent's current plan.
type Plan struct {
	ID               string
	Summary          string
	Steps            []Step
	AwaitingApproval bool
}

// Question is a question the agent asked.
type Question struct {
	ID       string
	Text     string
	Options  []string
	Answered bool
}

// File is a file the run changed.
type File struct {
	Path      string
	Status    string
	Additions int
	Deletions int
}

// State is the folded run.
type State struct {
	Items    []Item
	Plan     *Plan
	Question *Question // the question awaiting an answer, if any
	Files    []File
	PRURL    string
	Branch   string
	LastSeq  int64
	text     *strings.Builder
}

// New returns an empty state.
func New() *State {
	return &State{LastSeq: -1}
}

// Pending reports what the agent waits for from a human: "plan", "question" or "".
func (s *State) Pending() string {
	switch {
	case s.Question != nil && !s.Question.Answered:
		return "question"
	case s.Plan != nil && s.Plan.AwaitingApproval:
		return "plan"
	}
	return ""
}

// Apply folds events in, ignoring ones already seen.
func (s *State) Apply(events []api.RunEvent) {
	for _, ev := range events {
		if ev.Seq <= s.LastSeq {
			continue
		}
		s.LastSeq = ev.Seq
		s.apply(ev)
	}
}

func (s *State) flushText() {
	s.text = nil
}

func (s *State) apply(ev api.RunEvent) {
	p := ev.Payload
	if ev.Type != "message_delta" {
		s.flushText()
	}
	switch ev.Type {
	case "message_delta":
		text := str(p["text"])
		if text == "" {
			return
		}
		if s.text == nil {
			s.text = &strings.Builder{}
			s.Items = append(s.Items, Item{Kind: ItemText})
		}
		s.text.WriteString(text)
		s.Items[len(s.Items)-1].Body = s.text.String()
	case "tool_use":
		name := str(p["name"])
		if isLoopTool(name) {
			return
		}
		s.Items = append(s.Items, Item{Kind: ItemTool, Title: shortTool(name), Body: toolSummary(p["input"])})
	case "tool_result":
		if p["is_error"] == true && len(s.Items) > 0 && s.Items[len(s.Items)-1].Kind == ItemTool {
			last := &s.Items[len(s.Items)-1]
			last.Error = true
			last.Body = firstLine(str(p["output"]))
		}
	case "file_change":
		f := File{Path: str(p["path"]), Status: firstNonEmpty(str(p["status"]), "modified"),
			Additions: num(p["additions"]), Deletions: num(p["deletions"])}
		if f.Path == "" {
			return
		}
		replaced := false
		for i := range s.Files {
			if s.Files[i].Path == f.Path {
				s.Files[i] = f
				replaced = true
			}
		}
		if !replaced {
			s.Files = append(s.Files, f)
		}
		s.Items = append(s.Items, Item{Kind: ItemFile, Title: f.Path, Body: fmt.Sprintf("+%d −%d", f.Additions, f.Deletions)})
	case "plan":
		plan := &Plan{ID: str(p["planId"]), Summary: str(p["summary"]), AwaitingApproval: p["awaitingApproval"] == true}
		for _, raw := range list(p["steps"]) {
			m, _ := raw.(map[string]any)
			plan.Steps = append(plan.Steps, Step{Title: str(m["title"]), Detail: str(m["detail"]), Status: "pending"})
		}
		s.Plan = plan
		s.Items = append(s.Items, Item{Kind: ItemPlan, Title: fmt.Sprintf("Plan · %d steps", len(plan.Steps)), Body: plan.Summary})
	case "plan_step":
		if s.Plan == nil || str(p["planId"]) != s.Plan.ID {
			return
		}
		if i := num(p["step"]) - 1; i >= 0 && i < len(s.Plan.Steps) {
			s.Plan.Steps[i].Status = firstNonEmpty(str(p["status"]), s.Plan.Steps[i].Status)
		}
	case "question":
		q := &Question{ID: str(p["questionId"]), Text: str(p["question"])}
		for _, o := range list(p["options"]) {
			if text := strings.TrimSpace(fmt.Sprint(o)); text != "" {
				q.Options = append(q.Options, text)
			}
		}
		s.Question = q
		s.Items = append(s.Items, Item{Kind: ItemQuestion, Title: "The agent asks", Body: q.Text})
	case "human_response":
		requestID := str(p["requestId"])
		if str(p["kind"]) == "plan" {
			if s.Plan != nil && s.Plan.ID == requestID {
				s.Plan.AwaitingApproval = false
				if p["approved"] == true {
					s.Items = append(s.Items, Item{Kind: ItemAnswer, Title: "Plan approved"})
				} else {
					s.Items = append(s.Items, Item{Kind: ItemAnswer, Title: "Changes requested", Body: str(p["feedback"])})
				}
			}
			return
		}
		if s.Question != nil && s.Question.ID == requestID {
			s.Question.Answered = true
		}
		s.Items = append(s.Items, Item{Kind: ItemAnswer, Title: "Answered", Body: str(p["answer"])})
	case "human_timeout":
		if s.Question != nil {
			s.Question.Answered = true
		}
		if s.Plan != nil {
			s.Plan.AwaitingApproval = false
		}
		s.Items = append(s.Items, Item{Kind: ItemError, Title: "Nobody answered in time"})
	case "steering":
		s.Items = append(s.Items, Item{Kind: ItemSteering, Title: "You said", Body: firstNonEmpty(str(p["message"]), str(p["text"]))})
	case "status":
		if status := str(p["status"]); status != "" {
			s.Items = append(s.Items, Item{Kind: ItemStatus, Title: strings.ReplaceAll(status, "_", " "), Body: firstNonEmpty(str(p["phase"]), str(p["reason"]))})
		}
	case "result":
		s.PRURL = firstNonEmpty(str(p["pr_url"]), s.PRURL)
		s.Branch = firstNonEmpty(str(p["branch"]), s.Branch)
		s.Items = append(s.Items, Item{Kind: ItemResult, Title: firstNonEmpty(str(p["status"]), "finished"), Body: firstNonEmpty(str(p["error"]), s.PRURL)})
	case "error":
		s.Items = append(s.Items, Item{Kind: ItemError, Title: "Error", Body: firstNonEmpty(str(p["message"]), str(p["error"])), Error: true})
	}
}

// The agent's human-loop calls show as their own items, not as raw tools.
var loopTools = map[string]bool{"propose_plan": true, "update_plan": true, "ask_human": true, "report_outcome": true}

func isLoopTool(name string) bool {
	return loopTools[shortTool(name)]
}

// shortTool drops an MCP server prefix: "archyl__who_owns" → "who_owns".
func shortTool(name string) string {
	if i := strings.Index(name, "__"); i > 0 {
		return name[i+2:]
	}
	return name
}

// toolSummary picks the argument that says what a call is about.
func toolSummary(input any) string {
	m, ok := input.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"path", "file_path", "command", "pattern", "query", "url", "element", "task", "name"} {
		if v := str(m[key]); v != "" {
			return firstLine(v)
		}
	}
	raw, _ := json.Marshal(m)
	return truncate(string(raw), 80)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	return truncate(s, 120)
}

func truncate(s string, n int) string {
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
