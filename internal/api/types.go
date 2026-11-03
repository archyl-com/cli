package api

import (
	"strings"
	"time"
)

// User is the signed-in account.
type User struct {
	ID                    string `json:"id"`
	Email                 string `json:"email"`
	FirstName             string `json:"firstName"`
	LastName              string `json:"lastName"`
	DefaultOrganizationID string `json:"defaultOrganizationId"`
}

// DisplayName is the user's name, or their email when they have none.
func (u *User) DisplayName() string {
	name := u.FirstName
	if u.LastName != "" {
		name += " " + u.LastName
	}
	return name
}

// Repository is a git repository connected to a project.
type Repository struct {
	Name     string `json:"name"`
	FullName string `json:"fullName"`
	URL      string `json:"url"`
	CloneURL string `json:"cloneUrl"`
}

// Project is an Archyl project.
type Project struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Slug         string       `json:"slug"`
	Description  string       `json:"description"`
	Repositories []Repository `json:"repositories"`
}

// Owner is a user who owns an element.
type Owner struct {
	Email     string `json:"email"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

// TeamOwner is a team that owns an element.
type TeamOwner struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Element is a C4 element reference.
type Element struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
}

// LocatedElement is an element a file was attributed to.
type LocatedElement struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Match string `json:"match"`
}

// FileLocation is where one file sits in the architecture.
type FileLocation struct {
	Path     string           `json:"path"`
	Elements []LocatedElement `json:"elements"`
}

// ImpactedElement is an element reached by following relationships.
type ImpactedElement struct {
	Element
	Distance         int         `json:"distance"`
	Via              string      `json:"via"`
	RelationshipType string      `json:"relationshipType"`
	Owners           []Owner     `json:"owners"`
	TeamOwners       []TeamOwner `json:"teamOwners"`
}

// TouchedElement is an element a change lands in.
type TouchedElement struct {
	Element
	Owners     []Owner           `json:"owners"`
	TeamOwners []TeamOwner       `json:"teamOwners"`
	Dependents []ImpactedElement `json:"dependents"`
}

// NotifyList is everyone who should hear about a change.
type NotifyList struct {
	Users []Owner     `json:"users"`
	Teams []TeamOwner `json:"teams"`
}

// BlastRadius is the combined impact of a change.
type BlastRadius struct {
	Elements []TouchedElement `json:"elements"`
	Notify   NotifyList       `json:"notify"`
}

// LocateResult places files in the architecture.
type LocateResult struct {
	Files     []FileLocation `json:"files"`
	Unmatched []string       `json:"unmatched"`
	Impact    *BlastRadius   `json:"impact"`
}

// Lease is an advisory claim a session holds on an element.
type Lease struct {
	ElementID   string `json:"elementId"`
	ElementName string `json:"elementName"`
	ElementType string `json:"elementType"`
	Source      string `json:"source"`
}

// Session is a harness work session.
type Session struct {
	ID              string     `json:"id"`
	ProjectID       string     `json:"projectId"`
	AgentName       string     `json:"agentName"`
	TaskText        string     `json:"taskText"`
	Status          string     `json:"status"`
	Gate            string     `json:"gate"`
	GateReasons     []string   `json:"gateReasons"`
	StartedAt       time.Time  `json:"startedAt"`
	LastHeartbeatAt time.Time  `json:"lastHeartbeatAt"`
	EndedAt         *time.Time `json:"endedAt"`
	Leases          []Lease    `json:"leases"`
}

// Conflict is another session holding a lease on a target element.
type Conflict struct {
	ElementName string `json:"elementName"`
	SessionID   string `json:"sessionId"`
	AgentName   string `json:"agentName"`
	Task        string `json:"task"`
}

// ADRRef is a decision relevant to a task.
type ADRRef struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// Guardrail is a conformance rule relevant to a task.
type Guardrail struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
}

// RelevantContext is the architecture slice relevant to a task.
type RelevantContext struct {
	Primary    []Element   `json:"primary"`
	ADRs       []ADRRef    `json:"adrs"`
	Guardrails []Guardrail `json:"guardrails"`
}

// StartSessionRequest opens a work session.
type StartSessionRequest struct {
	AgentName string `json:"agentName"`
	Task      string `json:"task"`
	Exclusive bool   `json:"exclusive,omitempty"`
}

// StartSessionResult is the briefing for a new session.
type StartSessionResult struct {
	Session   *Session         `json:"session"`
	Context   *RelevantContext `json:"context"`
	Conflicts []Conflict       `json:"conflicts"`
	Markdown  string           `json:"markdown"`
}

// FinishSessionRequest closes a session.
type FinishSessionRequest struct {
	Summary             string   `json:"summary"`
	Decisions           []string `json:"decisions,omitempty"`
	FollowUps           []string `json:"followUps,omitempty"`
	CreateChangeRequest bool     `json:"createChangeRequest,omitempty"`
	ChangeRequestTitle  string   `json:"changeRequestTitle,omitempty"`
}

// FinishSessionResult reports the closed session.
type FinishSessionResult struct {
	Session             *Session `json:"session"`
	ChangeRequestNumber *int     `json:"changeRequestNumber"`
	Note                string   `json:"note"`
}

// ChangedFile is a file in a change set.
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// ConformanceCheckRequest runs the project's rules on file contents.
type ConformanceCheckRequest struct {
	CommitSHA    string            `json:"commitSha,omitempty"`
	BaseSHA      string            `json:"baseSha,omitempty"`
	Branch       string            `json:"branch"`
	ChangedFiles []ChangedFile     `json:"changedFiles"`
	FileContents map[string]string `json:"fileContents"`
}

// Violation is a broken conformance rule.
type Violation struct {
	ID          string `json:"id"`
	RuleID      string `json:"ruleId"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion"`
	FilePath    string `json:"filePath"`
	LineStart   int    `json:"lineStart"`
	LineEnd     int    `json:"lineEnd"`
	ElementType string `json:"elementType"`
}

// Rule is a conformance rule.
type Rule struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RuleType    string `json:"ruleType"`
	Severity    string `json:"severity"`
}

// ConformanceSummary counts violations by severity.
type ConformanceSummary struct {
	Total    int `json:"total"`
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

// ConformanceCheckResult is the outcome of a check.
type ConformanceCheckResult struct {
	Check struct {
		ID string `json:"id"`
	} `json:"check"`
	Summary    ConformanceSummary `json:"summary"`
	Violations []Violation        `json:"violations"`
	Rules      []Rule             `json:"rules"`
}

// Merge adds a batch of the same check. A rule on the whole model fires on
// every batch, with a new ID each time, so repeated violations count once.
func (r *ConformanceCheckResult) Merge(other *ConformanceCheckResult) {
	seen := make(map[Violation]bool, len(r.Violations)+len(other.Violations))
	key := func(v Violation) Violation {
		v.ID = ""
		return v
	}
	for _, v := range r.Violations {
		seen[key(v)] = true
	}
	for _, v := range other.Violations {
		if seen[key(v)] {
			continue
		}
		seen[key(v)] = true
		r.Violations = append(r.Violations, v)
		r.Summary.add(v.Severity)
	}
	known := make(map[string]bool, len(r.Rules))
	for _, rule := range r.Rules {
		known[rule.ID] = true
	}
	for _, rule := range other.Rules {
		if !known[rule.ID] {
			known[rule.ID] = true
			r.Rules = append(r.Rules, rule)
		}
	}
	if r.Check.ID == "" {
		r.Check = other.Check
	}
}

func (s *ConformanceSummary) add(severity string) {
	s.Total++
	switch strings.ToLower(severity) {
	case "critical":
		s.Critical++
	case "high":
		s.High++
	case "medium":
		s.Medium++
	case "low":
		s.Low++
	}
}

// RuleNames maps rule IDs to their names.
func (r *ConformanceCheckResult) RuleNames() map[string]string {
	names := make(map[string]string, len(r.Rules))
	for _, rule := range r.Rules {
		names[rule.ID] = rule.Name
	}
	return names
}

// DSLImport counts what a DSL sync created.
type DSLImport struct {
	SystemsCreated       int      `json:"systemsCreated"`
	ContainersCreated    int      `json:"containersCreated"`
	ComponentsCreated    int      `json:"componentsCreated"`
	CodeElementsCreated  int      `json:"codeElementsCreated"`
	RelationshipsCreated int      `json:"relationshipsCreated"`
	TechnologiesCreated  int      `json:"technologiesCreated"`
	ADRsCreated          int      `json:"adrsCreated"`
	DocsCreated          int      `json:"docsCreated"`
	APIContractsCreated  int      `json:"apiContractsCreated"`
	EventsCreated        int      `json:"eventsCreated"`
	EnvironmentsCreated  int      `json:"environmentsCreated"`
	ReleasesCreated      int      `json:"releasesCreated"`
	Warnings             []string `json:"warnings"`
}

// ReleaseRequest records a release.
type ReleaseRequest struct {
	Version     string `json:"version"`
	Status      string `json:"status,omitempty"`
	Changelog   string `json:"changelog,omitempty"`
	Environment string `json:"environment,omitempty"`
	SystemID    string `json:"systemId,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
	Source      string `json:"source,omitempty"`
	SourceURL   string `json:"sourceUrl,omitempty"`
}

// Release is a recorded release.
type Release struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// DriftScore is a model-to-code alignment score (higher is better).
type DriftScore struct {
	ID             string    `json:"id"`
	ErrorMessage   string    `json:"errorMessage"`
	Score          int       `json:"score"`
	TotalElements  int       `json:"totalElements"`
	MatchedCount   int       `json:"matchedCount"`
	DriftedCount   int       `json:"driftedCount"`
	MissingInCode  int       `json:"missingInCode"`
	MissingInModel int       `json:"missingInModel"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"createdAt"`
}
