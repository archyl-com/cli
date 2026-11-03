// Package incident gathers, for one element of the architecture, what an
// on-call engineer needs in the first minutes of an incident: who owns it,
// what depends on it and what it depends on, what changed lately, where the
// runbooks and decisions are, and who to tell.
//
// Nothing here talks to the network. The command plans the report from the
// model (NewPlan), fetches what the plan asks for, and Build turns it into a
// Report, which renders for a terminal, as Markdown or as a Slack message.
package incident

import (
	"strings"
	"time"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

// Report sizes. An incident screen is read in seconds, so every list is
// short and says how much it left out.
const (
	DefaultDepth    = 2
	MaxDepth        = 4
	MaxDependents   = 12
	MaxDependencies = 8
	MaxChanges      = 6
	MaxReleases     = 3
	MaxDocs         = 5
	MaxDecisions    = 4
	MaxNotes        = 4
	// MaxNotified is how many dependents have their owners looked up for
	// "Who to tell"; the report says how many it left out.
	MaxNotified = 100
)

// Sections that depend on the API, as listed in Report.Unavailable when
// their call failed.
const (
	SectionOwners   = "owners"
	SectionChanges  = "changes"
	SectionReleases = "releases"
	SectionDocs     = "docs"
	SectionNotes    = "notes"
)

// Team is a team that owns an element.
type Team struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

// Label names the team for a person to read: "Checkout team".
func (t Team) Label() string {
	name := firstNonEmpty(t.Name, t.Slug)
	if strings.HasSuffix(strings.ToLower(name), "team") {
		return name
	}
	return name + " team"
}

// Person is someone who owns an element.
type Person struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Label names the person with their email, so they can be found in any
// chat tool: "Jane Doe (jane@example.com)".
func (p Person) Label() string {
	switch {
	case p.Name == "":
		return p.Email
	case p.Email == "":
		return p.Name
	}
	return p.Name + " (" + p.Email + ")"
}

// key identifies the person across lookups.
func (p Person) key() string {
	return strings.ToLower(firstNonEmpty(p.Email, p.Name))
}

// Owners are the teams and people who own an element.
type Owners struct {
	Teams  []Team   `json:"teams"`
	People []Person `json:"people"`
}

// NewOwners converts the API's owners, stripped of control characters and
// without the entries that have no name at all.
func NewOwners(people []api.Owner, teams []api.TeamOwner) Owners {
	o := Owners{Teams: []Team{}, People: []Person{}}
	for _, t := range teams {
		team := Team{Name: clean(t.Name), Slug: clean(t.Slug)}
		if team.Name != "" || team.Slug != "" {
			o.Teams = append(o.Teams, team)
		}
	}
	for _, p := range people {
		person := Person{Name: clean(p.FirstName + " " + p.LastName), Email: clean(p.Email)}
		if person.Name != "" || person.Email != "" {
			o.People = append(o.People, person)
		}
	}
	return o
}

// Empty reports whether nobody owns the element.
func (o Owners) Empty() bool {
	return len(o.Teams) == 0 && len(o.People) == 0
}

// Labels names every owner, teams first.
func (o Owners) Labels() []string {
	out := make([]string, 0, len(o.Teams)+len(o.People))
	for _, t := range o.Teams {
		out = append(out, t.Label())
	}
	for _, p := range o.People {
		out = append(out, p.Label())
	}
	return out
}

// Element is an element of the model as a report shows it.
type Element struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Level        string   `json:"level"`
	Kind         string   `json:"kind,omitempty"`
	Path         string   `json:"path"`
	Technologies []string `json:"technologies,omitempty"`
	External     bool     `json:"external,omitempty"`
	URL          string   `json:"url,omitempty"`
}

// Type is the element's own type in words ("web app"), or its level, said
// to be external when it is: the cause may then be out of reach.
func (e Element) Type() string {
	kind := e.Level
	if e.Kind != "" {
		kind = strings.ReplaceAll(e.Kind, "_", " ")
	}
	if e.External {
		return "external " + kind
	}
	return kind
}

// Impacted is an element tied to the one in trouble, with its owners.
type Impacted struct {
	Element
	Distance int    `json:"distance"`
	Via      string `json:"via,omitempty"`
	Owners   Owners `json:"owners"`
	// OwnersFrom names the ancestor the owners come from, when the element
	// has none of its own.
	OwnersFrom string `json:"ownersFrom,omitempty"`
	// OwnersUnknown is set when a lookup failed before any owner was found:
	// the owners are neither "none" nor an ancestor's.
	OwnersUnknown bool `json:"ownersUnknown,omitempty"`
}

// Change is a recent change to the model.
type Change struct {
	At      time.Time `json:"at"`
	Who     string    `json:"who,omitempty"`
	Action  string    `json:"action"`
	Element string    `json:"element"`
	Type    string    `json:"type,omitempty"`
}

// Release is a recent release.
type Release struct {
	Version     string    `json:"version"`
	Status      string    `json:"status,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Production  bool      `json:"production,omitempty"`
	At          time.Time `json:"at"`
	URL         string    `json:"url,omitempty"`
}

// Doc is documentation linked to the element or one of its ancestors.
type Doc struct {
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	// Via names the ancestor the page is linked to, when it is not linked to
	// the element itself.
	Via string `json:"via,omitempty"`
}

// Decision is an architecture decision that applies to the element.
type Decision struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Status string `json:"status,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Note is something people or agents remembered about the element.
type Note struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Report is everything an incident screen shows, most urgent first.
type Report struct {
	Element Element `json:"element"`
	Owners  Owners  `json:"owners"`
	// OwnersFrom names the closest ancestor with owners, when the element
	// has none of its own.
	OwnersFrom string `json:"ownersFrom,omitempty"`
	// OwnersUnknown is set when the lookup failed before any owner was
	// found.
	OwnersUnknown bool `json:"ownersUnknown,omitempty"`

	Depth            int        `json:"depth"`
	Dependents       []Impacted `json:"dependents"`
	MoreDependents   int        `json:"moreDependents"`
	Dependencies     []Impacted `json:"dependencies"`
	MoreDependencies int        `json:"moreDependencies"`

	Changes  []Change  `json:"changes"`
	Releases []Release `json:"releases"`
	// ReleasesOf names the element the releases belong to; empty when they
	// are the project's latest.
	ReleasesOf string `json:"releasesOf,omitempty"`

	Docs      []Doc      `json:"docs"`
	Decisions []Decision `json:"decisions"`
	Notes     []Note     `json:"notes"`

	// Notify is every owner of the element and of what depends on it.
	Notify Owners `json:"notify"`
	// NotifyUnlisted counts the dependents whose owners were not looked up,
	// beyond MaxNotified.
	NotifyUnlisted int `json:"notifyUnlisted"`

	// Unavailable lists the sections whose data could not be fetched.
	Unavailable []string  `json:"unavailable"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// IsUnavailable reports whether a section could not be fetched.
func (r *Report) IsUnavailable(section string) bool {
	for _, s := range r.Unavailable {
		if s == section {
			return true
		}
	}
	return false
}

// DependentCount is how many elements depend on the element, shown or not.
func (r *Report) DependentCount() int {
	return len(r.Dependents) + r.MoreDependents
}

func newElement(n *graph.Node, link func(*graph.Node) string) Element {
	tech := make([]string, 0, len(n.Tech))
	for _, t := range n.Tech {
		tech = append(tech, clean(t))
	}
	e := Element{
		ID: n.ID, Name: clean(n.Name), Level: n.Level, Kind: clean(n.Kind), Path: clean(n.PathString()),
		Technologies: tech, External: n.External,
	}
	if link != nil {
		e.URL = link(n)
	}
	return e
}

// clean strips control characters from text written outside the CLI (names,
// versions written by CI, notes written by agents) so it cannot rewrite the
// terminal, and trims it.
func clean(s string) string {
	return strings.TrimSpace(ui.StripControl(s))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
