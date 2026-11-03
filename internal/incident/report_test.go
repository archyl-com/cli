package incident

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/graph"
	"github.com/archyl-com/cli/internal/ui"
)

var (
	checkout = Owners{Teams: []Team{{Name: "Checkout", Slug: "checkout"}}, People: []Person{{Name: "Jane Doe", Email: "jane@shop.test"}}}
	web      = Owners{Teams: []Team{{Name: "Web", Slug: "web"}}, People: []Person{{Name: "Jane Doe", Email: "JANE@shop.test"}}}
	platform = Owners{Teams: []Team{{Name: "Platform team", Slug: "platform"}}, People: []Person{}}
	nobody   = Owners{Teams: []Team{}, People: []Person{}}
	errDown  = errors.New("connection refused")
)

func hoursAgo(h int) time.Time {
	return now.Add(-time.Duration(h) * time.Hour)
}

func TestBuildOwners(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		element         string
		owners          map[string]Owners
		failed          map[string]bool
		ownersErr       error
		wantOwners      Owners
		wantFrom        string
		wantUnknown     bool
		wantNotify      []string
		wantUnavailable []string
	}{
		{
			name:            "own owners, then the dependents', each once",
			element:         "api",
			owners:          map[string]Owners{"api": checkout, "web": web, "mobile": checkout},
			wantOwners:      checkout,
			wantNotify:      []string{"Checkout team", "Web team", "Jane Doe (jane@shop.test)"},
			wantUnavailable: []string{},
		},
		{
			name:            "no owners of its own: the closest owned ancestor's",
			element:         "handlers",
			owners:          map[string]Owners{"handlers": nobody, "api": nobody, "s": platform},
			wantOwners:      platform,
			wantFrom:        "Shop",
			wantNotify:      []string{"Platform team"},
			wantUnavailable: []string{},
		},
		{
			name:            "nobody anywhere",
			element:         "handlers",
			owners:          map[string]Owners{},
			wantOwners:      nobody,
			wantNotify:      []string{},
			wantUnavailable: []string{},
		},
		{
			name:            "the lookup failed",
			element:         "handlers",
			owners:          map[string]Owners{},
			failed:          map[string]bool{"handlers": true},
			ownersErr:       errDown,
			wantOwners:      nobody,
			wantUnknown:     true,
			wantNotify:      []string{},
			wantUnavailable: []string{SectionOwners},
		},
		{
			name:            "its own lookup failed: never the ancestor's owners instead",
			element:         "handlers",
			owners:          map[string]Owners{"s": platform},
			failed:          map[string]bool{"handlers": true},
			ownersErr:       errDown,
			wantOwners:      nobody,
			wantUnknown:     true,
			wantNotify:      []string{},
			wantUnavailable: []string{SectionOwners},
		},
		{
			name:            "a failed ancestor between it and an owned one stops the walk",
			element:         "handlers",
			owners:          map[string]Owners{"handlers": nobody, "s": platform},
			failed:          map[string]bool{"api": true},
			ownersErr:       errDown,
			wantOwners:      nobody,
			wantUnknown:     true,
			wantNotify:      []string{},
			wantUnavailable: []string{SectionOwners},
		},
		{
			name:            "a failure higher up keeps the owners found but flags the section",
			element:         "handlers",
			owners:          map[string]Owners{"handlers": checkout},
			failed:          map[string]bool{"s": true},
			ownersErr:       errDown,
			wantOwners:      checkout,
			wantNotify:      []string{"Checkout team", "Jane Doe (jane@shop.test)"},
			wantUnavailable: []string{SectionOwners},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			r := Build(Input{
				Plan:  NewPlan(g, g.Node(testCase.element), 1),
				Facts: Facts{Owners: testCase.owners, OwnersFailed: testCase.failed, OwnersErr: testCase.ownersErr},
				Now:   now,
			})
			assert.Equal(t, testCase.wantOwners, r.Owners)
			assert.Equal(t, testCase.wantFrom, r.OwnersFrom)
			assert.Equal(t, testCase.wantUnknown, r.OwnersUnknown)
			assert.Equal(t, testCase.wantNotify, r.Notify.Labels())
			assert.Equal(t, testCase.wantUnavailable, r.Unavailable)
		})
	}
}

func TestBuildDependentsCarryTheirOwners(
	t *testing.T,
) {
	g := shop()
	r := Build(Input{
		Plan:  NewPlan(g, g.Node("api"), 2),
		Facts: Facts{Owners: map[string]Owners{"web": web, "bo": platform}},
		Links: testLinks,
		Now:   now,
	})

	assert.Equal(t, 4, r.DependentCount())
	admin := r.Dependents[0]
	assert.Equal(t, "Admin", admin.Name)
	assert.Equal(t, "reads_from", admin.Via)
	assert.Equal(t, platform, admin.Owners)
	assert.Equal(t, "Back Office", admin.OwnersFrom)
	assert.Equal(t, "https://app/e/admin", admin.URL)
	assert.Equal(t, web, r.Dependents[2].Owners)
	assert.Equal(t, []string{"Stripe", "Orders DB"}, []string{r.Dependencies[0].Name, r.Dependencies[1].Name})
	assert.Equal(t, "https://app/e/api", r.Element.URL)
	assert.Equal(t, "Shop / Orders API", r.Element.Path)
	assert.Equal(t, []string{"Go", "gRPC"}, r.Element.Technologies)
}

func TestBuildChanges(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		history         []api.HistoryEntry
		err             error
		want            []Change
		wantUnavailable bool
	}{
		{
			name: "merged, each once, latest first",
			history: []api.HistoryEntry{
				{ID: "h1", Action: "update", EntityName: "Orders API", EntityType: "container", UserName: "Jane", CreatedAt: hoursAgo(5)},
				{ID: "h2", Action: "create", EntityName: "Orders DB", EntityType: "container", UserName: "Bob", CreatedAt: hoursAgo(1)},
				{ID: "h1", Action: "update", EntityName: "Orders API", EntityType: "container", UserName: "Jane", CreatedAt: hoursAgo(5)},
				{ID: "h3", Action: "rename", EntityName: "Stripe", EntityType: "software_system", CreatedAt: hoursAgo(3)},
			},
			want: []Change{
				{At: hoursAgo(1), Who: "Bob", Action: "created", Element: "Orders DB", Type: "container"},
				{At: hoursAgo(3), Action: "rename", Element: "Stripe", Type: "software system"},
				{At: hoursAgo(5), Who: "Jane", Action: "updated", Element: "Orders API", Type: "container"},
			},
		},
		{
			name:            "the history failed",
			err:             errDown,
			want:            []Change{},
			wantUnavailable: true,
		},
		{
			name: "part of the history arrived",
			history: []api.HistoryEntry{
				{ID: "h1", Action: "delete", EntityName: "Cache", CreatedAt: hoursAgo(2)},
			},
			err:  errDown,
			want: []Change{{At: hoursAgo(2), Action: "deleted", Element: "Cache"}},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			r := Build(Input{
				Plan:  NewPlan(g, g.Node("api"), 1),
				Facts: Facts{Changes: testCase.history, ChangesErr: testCase.err},
				Now:   now,
			})
			assert.Equal(t, testCase.want, r.Changes)
			assert.Equal(t, testCase.wantUnavailable, r.IsUnavailable(SectionChanges))
		})
	}
}

func TestBuildCapsChanges(
	t *testing.T,
) {
	var history []api.HistoryEntry
	for i := range MaxChanges + 4 {
		history = append(history, api.HistoryEntry{ID: string(rune('a' + i)), CreatedAt: hoursAgo(i)})
	}
	g := shop()
	r := Build(Input{Plan: NewPlan(g, g.Node("api"), 1), Facts: Facts{Changes: history}, Now: now})

	assert.Len(t, r.Changes, MaxChanges)
	assert.Equal(t, now, r.Changes[0].At)
}

func TestBuildReleases(
	t *testing.T,
) {
	prod := &api.ReleaseEnvironment{Name: "production", IsProduction: true}
	released := hoursAgo(2)
	testCases := []struct {
		name            string
		element         string
		byScope         map[string][]api.ReleaseDetail
		project         []api.ReleaseDetail
		err             error
		wantOf          string
		wantVersions    []string
		wantUnavailable bool
	}{
		{
			name:    "the container's own releases win",
			element: "handlers",
			byScope: map[string][]api.ReleaseDetail{
				"api": {
					{Version: "v1.1", CreatedAt: hoursAgo(30)},
					{Version: "v1.2", CreatedAt: hoursAgo(10), ReleasedAt: &released, Environment: prod},
				},
				"s": {{Version: "shop-9", CreatedAt: hoursAgo(1)}},
			},
			wantOf:       "Orders API",
			wantVersions: []string{"v1.2", "v1.1"},
		},
		{
			name:         "then the system's",
			element:      "api",
			byScope:      map[string][]api.ReleaseDetail{"s": {{Version: "shop-9", CreatedAt: hoursAgo(1)}}},
			project:      []api.ReleaseDetail{{Version: "other", CreatedAt: hoursAgo(1)}},
			wantOf:       "Shop",
			wantVersions: []string{"shop-9"},
		},
		{
			name:    "else the project's latest, capped",
			element: "api",
			project: []api.ReleaseDetail{
				{Version: "a", CreatedAt: hoursAgo(1)}, {Version: "b", CreatedAt: hoursAgo(2)},
				{Version: "c", CreatedAt: hoursAgo(3)}, {Version: "d", CreatedAt: hoursAgo(4)},
			},
			wantVersions: []string{"a", "b", "c"},
		},
		{
			name:            "the listing failed",
			element:         "api",
			err:             errDown,
			wantVersions:    []string{},
			wantUnavailable: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			r := Build(Input{
				Plan:  NewPlan(g, g.Node(testCase.element), 1),
				Facts: Facts{Releases: testCase.byScope, ProjectReleases: testCase.project, ReleasesErr: testCase.err},
				Now:   now,
			})
			versions := []string{}
			for _, rel := range r.Releases {
				versions = append(versions, rel.Version)
			}
			assert.Equal(t, testCase.wantVersions, versions)
			assert.Equal(t, testCase.wantOf, r.ReleasesOf)
			assert.Equal(t, testCase.wantUnavailable, r.IsUnavailable(SectionReleases))
		})
	}
}

func TestBuildReleaseFields(
	t *testing.T,
) {
	released := hoursAgo(2)
	g := shop()
	r := Build(Input{
		Plan: NewPlan(g, g.Node("api"), 1),
		Facts: Facts{Releases: map[string][]api.ReleaseDetail{"api": {{
			Version: "v1.2", Status: "deployed", SourceURL: "https://ci/1", CreatedAt: hoursAgo(9), ReleasedAt: &released,
			Environment: &api.ReleaseEnvironment{Name: "production", IsProduction: true},
		}}}},
		Now: now,
	})

	assert.Equal(t, []Release{{
		Version: "v1.2", Status: "deployed", Environment: "production", Production: true, At: released, URL: "https://ci/1",
	}}, r.Releases)
}

func TestBuildDocs(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		docs            map[string][]api.Doc
		err             error
		want            []Doc
		wantUnavailable bool
	}{
		{
			name: "the element's first, then its ancestors', each once",
			docs: map[string][]api.Doc{
				"s":        {{ID: "d3", Title: "Shop runbook"}, {ID: "d1", Title: "Handlers runbook"}},
				"handlers": {{ID: "d1", Title: "Handlers runbook"}},
				"api":      {{ID: "d2", Title: "Orders API on-call"}},
			},
			want: []Doc{
				{Title: "Handlers runbook", URL: "https://app/d/d1"},
				{Title: "Orders API on-call", URL: "https://app/d/d2", Via: "Orders API"},
				{Title: "Shop runbook", URL: "https://app/d/d3", Via: "Shop"},
			},
		},
		{
			name: "capped",
			docs: map[string][]api.Doc{"handlers": {
				{ID: "1", Title: "1"}, {ID: "2", Title: "2"}, {ID: "3", Title: "3"},
				{ID: "4", Title: "4"}, {ID: "5", Title: "5"}, {ID: "6", Title: "6"},
			}},
			want: []Doc{
				{Title: "1", URL: "https://app/d/1"}, {Title: "2", URL: "https://app/d/2"}, {Title: "3", URL: "https://app/d/3"},
				{Title: "4", URL: "https://app/d/4"}, {Title: "5", URL: "https://app/d/5"},
			},
		},
		{
			name:            "the listing failed",
			err:             errDown,
			want:            []Doc{},
			wantUnavailable: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			r := Build(Input{
				Plan:  NewPlan(g, g.Node("handlers"), 1),
				Facts: Facts{Docs: testCase.docs, DocsErr: testCase.err},
				Links: testLinks,
				Now:   now,
			})
			assert.Equal(t, testCase.want, r.Docs)
			assert.Equal(t, testCase.wantUnavailable, r.IsUnavailable(SectionDocs))
		})
	}
}

func TestBuildDecisions(
	t *testing.T,
) {
	adrs := []api.ADR{
		{ID: "a1", Number: 1, Title: "Shop runs on Kubernetes", Status: "accepted", Links: []api.ADRLink{{ElementID: "s"}}},
		{ID: "a2", Number: 2, Title: "Orders are idempotent", Status: "accepted", Links: []api.ADRLink{{ElementID: "api"}}},
		{ID: "a3", Number: 3, Title: "PlaceOrder retries", Status: "proposed", Links: []api.ADRLink{{ElementID: "place"}, {ElementID: "api"}}},
		{ID: "a4", Number: 4, Title: "Storefront in React", Status: "accepted", Links: []api.ADRLink{{ElementID: "web"}}},
		{ID: "a5", Number: 5, Title: "Unlinked", Status: "accepted"},
	}
	testCases := []struct {
		name    string
		element string
		want    []int
	}{
		{name: "its own, inside it and above it; accepted first", element: "api", want: []int{2, 1, 3}},
		{name: "a leaf sees its ancestors'", element: "place", want: []int{2, 1, 3}},
		{name: "a system sees everything inside it", element: "s", want: []int{4, 2, 1, 3}},
		{name: "nothing applies", element: "pay", want: []int{}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			r := Build(Input{Plan: NewPlan(g, g.Node(testCase.element), 1), ADRs: adrs, Links: testLinks, Now: now})
			got := []int{}
			for _, d := range r.Decisions {
				got = append(got, d.Number)
			}
			assert.Equal(t, testCase.want, got)
		})
	}
}

func TestBuildNotes(
	t *testing.T,
) {
	testCases := []struct {
		name            string
		memories        []api.Memory
		err             error
		want            []Note
		wantUnavailable bool
	}{
		{
			name: "pitfalls first, blank ones dropped, capped",
			memories: []api.Memory{
				{Kind: "convention", Content: "Handlers return problem+json."},
				{Kind: "note", Title: "Owned by checkout since 2025"},
				{Kind: "pitfall", Content: "Retries must reuse\nthe order key."},
				{Kind: "note", Content: "  "},
				{Kind: "pitfall", Content: "The DB pool is 20."},
				{Kind: "other", Content: "Dropped by the cap."},
			},
			want: []Note{
				{Kind: "pitfall", Text: "Retries must reuse\nthe order key."},
				{Kind: "pitfall", Text: "The DB pool is 20."},
				{Kind: "note", Text: "Owned by checkout since 2025"},
				{Kind: "convention", Text: "Handlers return problem+json."},
			},
		},
		{
			name:            "recall failed",
			err:             errDown,
			want:            []Note{},
			wantUnavailable: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := shop()
			r := Build(Input{
				Plan:  NewPlan(g, g.Node("api"), 1),
				Facts: Facts{Memories: testCase.memories, MemoriesErr: testCase.err},
				Now:   now,
			})
			assert.Equal(t, testCase.want, r.Notes)
			assert.Equal(t, testCase.wantUnavailable, r.IsUnavailable(SectionNotes))
		})
	}
}

func TestAgo(
	t *testing.T,
) {
	testCases := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "unknown", at: time.Time{}, want: ""},
		{name: "in the future", at: now.Add(time.Minute), want: "just now"},
		{name: "seconds", at: now.Add(-30 * time.Second), want: "just now"},
		{name: "minutes", at: now.Add(-12 * time.Minute), want: "12 min ago"},
		{name: "hours", at: hoursAgo(2), want: "2 h ago"},
		{name: "yesterday in hours", at: hoursAgo(47), want: "47 h ago"},
		{name: "days", at: hoursAgo(24 * 4), want: "4 d ago"},
		{name: "a month old shows the date", at: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), want: "on 2 Aug 2026"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Ago(testCase.at, now))
		})
	}
}

func TestOwnerLabels(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		people []api.Owner
		teams  []api.TeamOwner
		want   []string
	}{
		{
			name:   "teams first, people with their email",
			people: []api.Owner{{FirstName: "Jane", LastName: "Doe", Email: "jane@shop.test"}, {Email: "ops@shop.test"}},
			teams:  []api.TeamOwner{{Name: "Checkout", Slug: "checkout"}, {Name: "SRE Team"}, {Slug: "payments"}},
			want:   []string{"Checkout team", "SRE Team", "payments team", "Jane Doe (jane@shop.test)", "ops@shop.test"},
		},
		{
			name:   "a name without email",
			people: []api.Owner{{FirstName: "Bob"}},
			want:   []string{"Bob"},
		},
		{
			name: "nobody",
			want: []string{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			o := NewOwners(testCase.people, testCase.teams)
			assert.Equal(t, testCase.want, o.Labels())
			assert.Equal(t, len(testCase.want) == 0, o.Empty())
		})
	}
}

func TestBuildDependentWithFailedLookup(
	t *testing.T,
) {
	g := shop()
	r := Build(Input{
		Plan: NewPlan(g, g.Node("api"), 1),
		Facts: Facts{
			Owners:       map[string]Owners{"api": checkout, "s": platform, "bo": platform},
			OwnersFailed: map[string]bool{"admin": true},
			OwnersErr:    errDown,
		},
		Now: now,
	})

	admin := r.Dependents[0]
	assert.Equal(t, "Admin", admin.Name)
	assert.True(t, admin.OwnersUnknown, "a failed lookup is unknown, not none")
	assert.Empty(t, admin.OwnersFrom, "a failed lookup is never inherited")
	assert.True(t, admin.Owners.Empty())
	assert.Contains(t, Markdown(r), "- Admin (container), reads from · _Unavailable._\n")
	assert.Equal(t, "Shop", r.Dependents[1].OwnersFrom, "a dependent looked up fine still inherits")
}

func TestBuildNotifiesBeyondTheShownDependents(
	t *testing.T,
) {
	testCases := []struct {
		name         string
		clients      int
		wantTeams    int
		wantUnlisted int
	}{
		{name: "every dependent's owners, not only the shown ones", clients: MaxDependents + 8, wantTeams: MaxDependents + 8},
		{name: "capped, with the rest counted", clients: MaxNotified + 7, wantTeams: MaxNotified, wantUnlisted: 7},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			g := fanIn(testCase.clients)
			plan := NewPlan(g, g.Node("api"), 1)
			owners := map[string]Owners{}
			for _, n := range plan.OwnerTargets() {
				owners[n.ID] = Owners{Teams: []Team{{Name: "Team " + n.ID}}}
			}
			delete(owners, "api")
			delete(owners, "s")
			r := Build(Input{Plan: plan, Facts: Facts{Owners: owners}, Now: now})

			assert.Len(t, r.Dependents, MaxDependents)
			assert.Len(t, r.Notify.Teams, testCase.wantTeams)
			assert.Equal(t, testCase.wantUnlisted, r.NotifyUnlisted)
			if testCase.wantUnlisted == 0 {
				assert.NotContains(t, Markdown(r), "whose owners are not listed")
				return
			}
			assert.Contains(t, Markdown(r), "_Plus 7 more dependents whose owners are not listed._")
			assert.Contains(t, Slack(r), ", plus the owners of 7 more dependents\n")
		})
	}
}

func TestBuildStripsControlCharacters(
	t *testing.T,
) {
	const esc = "\x1b]0;pwned\x07\x1b[2J"
	g := graph.New(&api.Model{
		Systems: []api.System{{ID: "s", Name: "Shop" + esc, Containers: []api.Container{
			{ID: "api", Name: "Orders" + esc, Technologies: []api.Technology{{Name: "Go" + esc}}},
			{ID: "web", Name: "Web" + esc},
		}}},
		Relationships: []api.Relationship{{ID: "r", SourceID: "web", TargetID: "api", Type: "calls" + esc}},
	})
	r := Build(Input{
		Plan: NewPlan(g, g.Node("api"), 1),
		ADRs: []api.ADR{{ID: "a", Number: 1, Title: "ADR" + esc, Status: "accepted" + esc, Links: []api.ADRLink{{ElementID: "api"}}}},
		Facts: Facts{
			Owners: map[string]Owners{"api": NewOwners(
				[]api.Owner{{FirstName: "Jane" + esc, Email: "j@x" + esc}},
				[]api.TeamOwner{{Name: "Checkout" + esc}},
			)},
			Changes:  []api.HistoryEntry{{ID: "h", Action: "update", UserName: "Bob" + esc, EntityName: "DB" + esc, CreatedAt: now}},
			Releases: map[string][]api.ReleaseDetail{"api": {{Version: "v1" + esc, Status: "ok" + esc, SourceURL: "https://ci" + esc, CreatedAt: now}}},
			Docs:     map[string][]api.Doc{"s": {{ID: "d", Title: "Runbook" + esc}}},
			Memories: []api.Memory{{Kind: "pitfall" + esc, Content: "Careful" + esc}},
		},
		Links: testLinks,
		Now:   now,
	})

	var terminal bytes.Buffer
	Render(&ui.Printer{Out: &terminal, Err: io.Discard}, r, "")
	for name, out := range map[string]string{"markdown": Markdown(r), "slack": Slack(r), "terminal": terminal.String()} {
		assert.NotContains(t, out, "\x1b", name)
		assert.NotContains(t, out, "\x07", name)
	}
	assert.Contains(t, Markdown(r), "# Incident: Orders]0;pwned[2J")
}

func TestNewOwnersDropsNamelessEntries(
	t *testing.T,
) {
	o := NewOwners(
		[]api.Owner{{FirstName: " ", Email: ""}, {Email: "ops@shop.test"}},
		[]api.TeamOwner{{}, {Name: "Checkout team"}},
	)

	assert.Equal(t, []string{"Checkout team", "ops@shop.test"}, o.Labels())
}
