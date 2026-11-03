package codeowners

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHandlesTeam(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		handles Handles
		team    Team
		want    string
		wantOK  bool
	}{
		{
			name:    "org and slug",
			handles: Handles{Org: "acme"},
			team:    Team{Name: "Checkout", Slug: "checkout"},
			want:    "@acme/checkout",
			wantOK:  true,
		},
		{
			name:    "an org written as a handle",
			handles: Handles{Org: "@acme/"},
			team:    Team{Name: "Checkout", Slug: "checkout"},
			want:    "@acme/checkout",
			wantOK:  true,
		},
		{
			name:    "a GitLab group with subgroups",
			handles: Handles{Org: "acme/video"},
			team:    Team{Name: "Player", Slug: "player"},
			want:    "@acme/video/player",
			wantOK:  true,
		},
		{
			name:    "no slug: the name is slugged",
			handles: Handles{Org: "acme"},
			team:    Team{Name: "Video Platform"},
			want:    "@acme/video-platform",
			wantOK:  true,
		},
		{
			name:    "override by slug",
			handles: Handles{Org: "acme", Teams: map[string]string{"platform": "@acme/video-platform"}},
			team:    Team{Name: "Platform", Slug: "platform"},
			want:    "@acme/video-platform",
			wantOK:  true,
		},
		{
			name:    "override by name, without an org",
			handles: Handles{Teams: map[string]string{"Platform": "acme/infra"}},
			team:    Team{Name: "Platform", Slug: "platform-1"},
			want:    "@acme/infra",
			wantOK:  true,
		},
		{
			name:   "no org and no override",
			team:   Team{Name: "Checkout", Slug: "checkout"},
			wantOK: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := testCase.handles.Team(testCase.team)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestHandlesPerson(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		handles Handles
		person  Person
		want    string
		wantOK  bool
	}{
		{
			name:   "the email by default",
			person: Person{Email: "ada@example.com", Name: "Ada Lovelace"},
			want:   "ada@example.com",
			wantOK: true,
		},
		{
			name:    "an override, matched without case",
			handles: Handles{Users: map[string]string{"Ada@Example.com": "ada"}},
			person:  Person{Email: "ada@example.com"},
			want:    "@ada",
			wantOK:  true,
		},
		{
			name:    "an override to another email stays an email",
			handles: Handles{Users: map[string]string{"ada@example.com": "ada@corp.example"}},
			person:  Person{Email: "ada@example.com"},
			want:    "ada@corp.example",
			wantOK:  true,
		},
		{
			name:   "no email",
			person: Person{Name: "Grace Hopper"},
			wantOK: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := testCase.handles.Person(testCase.person)
			assert.Equal(t, testCase.wantOK, ok)
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestSlug(
	t *testing.T,
) {
	testCases := []struct {
		name string
		want string
	}{
		{name: "Checkout", want: "checkout"},
		{name: "Video Platform", want: "video-platform"},
		{name: "  SRE & Ops  ", want: "sre-ops"},
		{name: "", want: ""},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Slug(testCase.name))
		})
	}
}

func TestOwnersEmpty(
	t *testing.T,
) {
	assert.True(t, Owners{}.Empty())
	assert.False(t, Owners{Teams: []Team{{Name: "Checkout"}}}.Empty())
	assert.False(t, Owners{People: []Person{{Email: "ada@example.com"}}}.Empty())
}
