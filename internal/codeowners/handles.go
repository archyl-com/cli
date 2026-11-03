// Package codeowners turns the ownership recorded in Archyl into a CODEOWNERS
// file: it places each owned element on the folders of the repository that
// belong to it, writes the owners as git host handles, and keeps the
// generated lines in a marked block so hand-written ones survive.
package codeowners

import (
	"regexp"
	"strings"
	"unicode"
)

// Person is someone who owns an element.
type Person struct {
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// Team is a team that owns an element.
type Team struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

// Owners are the people and teams that own one element.
type Owners struct {
	People []Person
	Teams  []Team
}

// Empty reports whether nobody owns the element.
func (o Owners) Empty() bool {
	return len(o.People) == 0 && len(o.Teams) == 0
}

// Handles turns owners into CODEOWNERS handles.
type Handles struct {
	// Org prefixes team handles: @<org>/<team-slug>. Without it, a team
	// needs an entry in Teams.
	Org string
	// Teams maps a team's slug or name to its handle.
	Teams map[string]string
	// Users maps an email, compared without case, to a handle.
	Users map[string]string
}

// handlePattern is what CODEOWNERS accepts as an owner. Handles are built from
// names in the model: anything else could add a line, and with it an owner.
var handlePattern = regexp.MustCompile(`^(@[\w.-]+(/[\w.-]+)*|[^@\s]+@[^@\s]+\.[^@\s]+)$`)

func validHandle(handle string) bool {
	return handlePattern.MatchString(handle)
}

// Team returns the team's handle, false when there is no way to name it.
func (h Handles) Team(t Team) (string, bool) {
	slug := t.Slug
	if slug == "" {
		slug = Slug(t.Name)
	}
	for _, key := range []string{t.Slug, t.Name, slug} {
		if handle, ok := h.Teams[key]; ok && key != "" {
			return asHandle(handle), true
		}
	}
	org := strings.Trim(strings.TrimPrefix(h.Org, "@"), "/")
	if org == "" || slug == "" {
		return "", false
	}
	return "@" + org + "/" + slug, true
}

// Person returns the person's handle: the override for their email, else
// the email itself, which CODEOWNERS accepts. Someone without an email has
// no handle.
func (h Handles) Person(p Person) (string, bool) {
	email := strings.TrimSpace(p.Email)
	for key, handle := range h.Users {
		if email != "" && strings.EqualFold(key, email) {
			return asHandle(handle), true
		}
	}
	if email == "" {
		return "", false
	}
	return email, true
}

// asHandle adds the @ a team or user handle needs; an email keeps its form.
func asHandle(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "@") {
		return s
	}
	return "@" + s
}

// Slug lowercases a team name and joins its words with dashes, the way git
// hosts name teams: "Video Platform" is video-platform.
func Slug(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(words, "-")
}
