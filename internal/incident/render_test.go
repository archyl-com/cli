package incident

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/ui"
)

// fullReport is a report with something in every section.
func fullReport() *Report {
	g := shop()
	released := hoursAgo(2)
	return Build(Input{
		Plan: NewPlan(g, g.Node("api"), 2),
		ADRs: []api.ADR{{ID: "a2", Number: 2, Title: "Orders are idempotent", Status: "accepted", Links: []api.ADRLink{{ElementID: "api"}}}},
		Facts: Facts{
			Owners: map[string]Owners{"api": checkout, "web": web, "bo": platform},
			Changes: []api.HistoryEntry{
				{ID: "h1", Action: "update", EntityName: "Orders DB", EntityType: "container", UserName: "Bob", CreatedAt: hoursAgo(3)},
			},
			Releases: map[string][]api.ReleaseDetail{"api": {{
				Version: "v1.2", Status: "deployed", SourceURL: "https://ci/1", ReleasedAt: &released,
				Environment: &api.ReleaseEnvironment{Name: "production", IsProduction: true},
			}}},
			Docs:     map[string][]api.Doc{"s": {{ID: "d1", Title: "Shop <runbook> & [ops]"}}},
			Memories: []api.Memory{{Kind: "pitfall", Content: "Retries must reuse the order key."}},
		},
		Links: testLinks,
		Now:   now,
	})
}

// downReport is a report where every API call failed.
func downReport() *Report {
	g := shop()
	plan := NewPlan(g, g.Node("handlers"), 1)
	failed := map[string]bool{}
	for _, n := range plan.OwnerTargets() {
		failed[n.ID] = true
	}
	return Build(Input{
		Plan: plan,
		Facts: Facts{
			OwnersFailed: failed,
			OwnersErr:    errDown, ChangesErr: errDown, ReleasesErr: errDown, DocsErr: errDown, MemoriesErr: errDown,
		},
		Now: now,
	})
}

func TestMarkdown(
	t *testing.T,
) {
	testCases := []struct {
		name        string
		report      *Report
		want        []string
		wantMissing []string
	}{
		{
			name:   "every section",
			report: fullReport(),
			want: []string{
				"# Incident: Orders API\n\napi · Shop / Orders API · Go, gRPC\n",
				"- **Owners:** Checkout team, Jane Doe (jane@shop.test)\n",
				"- **Open:** https://app/e/api\n",
				"**4 elements depend on it within 2 hops.**",
				"1 hop away:\n\n- Admin (container), reads from · Platform team (from Back Office)\n",
				"- Storefront (web app), calls · Web team, Jane Doe (JANE@shop.test)\n",
				"2 hops away:\n\n- Customer (person), uses · none set\n",
				"**It depends on:**\n\n- Stripe (external system), charges · none set\n- Orders DB (database), writes to · none set\n",
				"- 3 h ago · Bob updated Orders DB (container)\n",
				"**Releases of Orders API:**\n\n- v1.2 deployed in production · 2 h ago · https://ci/1\n",
				`- [Shop <runbook> & \[ops\]](https://app/d/d1) (on Shop)`,
				"- [ADR-2 Orders are idempotent](https://app/a/a2) (accepted)\n",
				"- pitfall: Retries must reuse the order key.\n",
				"## Who to tell\n\n- Checkout team\n- Platform team\n- Web team\n- Jane Doe (jane@shop.test)\n",
			},
			wantMissing: []string{"navailable"},
		},
		{
			name:   "every call failed",
			report: downReport(),
			want: []string{
				"- **Owners:** _Unavailable._\n",
				"**1 element depends on it within 1 hop.**",
				"- Admin (container), reads from · _Unavailable._\n",
				"## Recent changes\n\n_Unavailable._\n",
				"**Latest releases in the project:**\n\n_Unavailable._\n",
				"No documentation, decision or note is linked to it. Some sources were unavailable.\n",
				"## Who to tell\n\n_Incomplete: some owners could not be fetched._\n",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			out := Markdown(testCase.report)
			for _, w := range testCase.want {
				assert.Contains(t, out, w)
			}
			for _, w := range testCase.wantMissing {
				assert.NotContains(t, out, w)
			}
		})
	}
}

func TestMarkdownNothingAround(
	t *testing.T,
) {
	g := shop()
	out := Markdown(Build(Input{Plan: NewPlan(g, g.Node("place"), 1), Now: now}))

	assert.Contains(t, out, "Nothing depends on it in the model.\n")
	assert.Contains(t, out, "**It depends on:** nothing in the model.\n")
	assert.Contains(t, out, "No recent change to it or its dependencies in the model.\n")
	assert.Contains(t, out, "No release recorded.\n")
	assert.Contains(t, out, "No owner is set on these elements.\n")
}

func TestSlack(
	t *testing.T,
) {
	testCases := []struct {
		name   string
		report *Report
		want   string
	}{
		{
			name:   "every section",
			report: fullReport(),
			want: ":rotating_light: *Incident on <https://app/e/api|Orders API>* (api · Shop / Orders API · Go, gRPC)\n" +
				"*Owned by:* Checkout team, Jane Doe (jane@shop.test)\n" +
				"*Impacted (4 within 2 hops):* <https://app/e/admin|Admin>, <https://app/e/mobile|Mobile App>, " +
				"<https://app/e/web|Storefront>, <https://app/e/u|Customer>\n" +
				"*Depends on:* <https://app/e/pay|Stripe>, <https://app/e/db|Orders DB>\n" +
				"*Latest:* <https://ci/1|v1.2 deployed in production> 2 h ago; Bob updated Orders DB (container) 3 h ago\n" +
				"*Runbooks:* <https://app/d/d1|Shop &lt;runbook&gt; &amp; [ops]>, <https://app/a/a2|ADR-2 Orders are idempotent>\n" +
				"*To notify:* Checkout team, Platform team, Web team, Jane Doe (jane@shop.test)\n",
		},
		{
			name:   "every call failed: only what the model knows",
			report: downReport(),
			want: ":rotating_light: *Incident on Order Handlers* (component · Shop / Orders API / Order Handlers)\n" +
				"*Owned by:* unknown, the lookup failed\n" +
				"*Impacted (1 within 1 hop):* Admin\n" +
				"*Depends on:* Stripe\n" +
				"_Incomplete: could not fetch owners, changes, releases, docs, notes._\n",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, Slack(testCase.report))
		})
	}
}

func TestSlackShortensLongLists(
	t *testing.T,
) {
	g := fanIn(MaxDependents + 2)
	r := Build(Input{Plan: NewPlan(g, g.Node("api"), 1), Facts: Facts{Owners: map[string]Owners{"s": platform}}, Now: now})

	out := Slack(r)

	assert.Contains(t, out, "*Impacted (14 within 1 hop):* Client c00, Client c01, Client c02, Client c03, Client c04, Client c05 and 8 more\n")
	assert.Contains(t, out, "*Owned by:* Platform team (from S)\n")
}

func TestSlackLinkNeverBreaks(
	t *testing.T,
) {
	testCases := []struct {
		name string
		text string
		url  string
		want string
	}{
		{name: "plain", text: "Orders", url: "https://x", want: "<https://x|Orders>"},
		{name: "no url", text: "<!channel>", want: "&lt;!channel&gt;"},
		{name: "a pipe in the text", text: "a|b", url: "https://x", want: "<https://x|a/b>"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, slackLink(testCase.text, testCase.url))
		})
	}
}

func TestRender(
	t *testing.T,
) {
	testCases := []struct {
		name    string
		report  *Report
		diagram string
		want    []string
	}{
		{
			name:    "every section",
			report:  fullReport(),
			diagram: "[diagram]",
			want: []string{
				"Orders API  api  Shop / Orders API",
				"tech    Go · gRPC",
				"owners  Checkout team, Jane Doe",
				"open    https://app/e/api",
				"Blast radius  4 elements depend on it within 2 hops",
				"Admin  reads from  Platform team  from Back Office",
				"Customer  uses  none set",
				"[diagram]",
				"Stripe  charges  none set",
				"3 h ago      Bob updated Orders DB (container)",
				"Releases of Orders API",
				"2 h ago      v1.2 deployed in production",
				"doc  Shop <runbook> & [ops]  on Shop",
				"https://app/d/d1",
				"ADR-2  Orders are idempotent  accepted",
				"pitfall  Retries must reuse the order key.",
				"Who to tell",
				"Jane Doe  jane@shop.test",
			},
		},
		{
			name:   "every call failed",
			report: downReport(),
			want: []string{
				"owners  unavailable",
				"Admin  reads from  unavailable",
				"Recent changes\n  Unavailable.",
				"Latest releases in the project\n  Unavailable.",
				"documentation unavailable",
				"notes unavailable",
				"Who to tell\n  Incomplete: some owners could not be fetched.",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var out bytes.Buffer
			Render(&ui.Printer{Out: &out, Err: io.Discard}, testCase.report, testCase.diagram)
			for _, w := range testCase.want {
				assert.Contains(t, out.String(), w)
			}
		})
	}
}
