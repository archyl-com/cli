package incident

import (
	"fmt"
	"strings"
)

// slackListMax keeps each line of the Slack message short enough to read
// in a channel.
const slackListMax = 6

// Slack renders a ready-to-paste Slack message in mrkdwn: what is affected,
// what it impacts, the latest change, the runbooks and who to ping. Owners
// are plain names and emails: Archyl does not know their Slack handles.
func Slack(r *Report) string {
	e := r.Element
	var lines []string
	lines = append(lines, ":rotating_light: *Incident on "+slackLink(e.Name, e.URL)+"* ("+slackEscape(elementLine(e))+")")
	switch {
	case r.OwnersUnknown:
		lines = append(lines, "*Owned by:* unknown, the lookup failed")
	case !r.Owners.Empty():
		owners := "*Owned by:* " + slackEscape(strings.Join(r.Owners.Labels(), ", "))
		if r.OwnersFrom != "" {
			owners += " (from " + slackEscape(r.OwnersFrom) + ")"
		}
		lines = append(lines, owners)
	}
	if n := r.DependentCount(); n > 0 {
		names := make([]string, 0, len(r.Dependents))
		for _, d := range r.Dependents {
			names = append(names, slackLink(d.Name, d.URL))
		}
		lines = append(lines, fmt.Sprintf("*Impacted (%d within %s):* %s", n, hops(r.Depth), slackList(names, n)))
	}
	if len(r.Dependencies) > 0 {
		names := make([]string, 0, len(r.Dependencies))
		for _, d := range r.Dependencies {
			names = append(names, slackLink(d.Name, d.URL))
		}
		lines = append(lines, "*Depends on:* "+slackList(names, len(r.Dependencies)+r.MoreDependencies))
	}
	if latest := slackLatest(r); latest != "" {
		lines = append(lines, "*Latest:* "+latest)
	}
	if refs := slackRefs(r); len(refs) > 0 {
		lines = append(lines, "*Runbooks:* "+slackList(refs, len(refs)))
	}
	if !r.Notify.Empty() {
		notify := "*To notify:* " + slackEscape(strings.Join(r.Notify.Labels(), ", "))
		if r.NotifyUnlisted > 0 {
			notify += fmt.Sprintf(", plus the owners of %s", moreDependents(r.NotifyUnlisted))
		}
		lines = append(lines, notify)
	}
	if len(r.Unavailable) > 0 {
		// A pasted message must not look complete when it is not.
		lines = append(lines, "_Incomplete: could not fetch "+strings.Join(r.Unavailable, ", ")+"._")
	}
	return strings.Join(lines, "\n") + "\n"
}

// slackLatest is the latest release and the latest model change.
func slackLatest(r *Report) string {
	var parts []string
	if len(r.Releases) > 0 {
		rel := r.Releases[0]
		parts = append(parts, slackLink(releaseText(rel), rel.URL)+" "+Ago(rel.At, r.GeneratedAt))
	}
	if len(r.Changes) > 0 {
		c := r.Changes[0]
		parts = append(parts, slackEscape(changeText(c))+" "+Ago(c.At, r.GeneratedAt))
	}
	return strings.Join(parts, "; ")
}

// slackRefs are the documentation pages, then the decisions.
func slackRefs(r *Report) []string {
	var out []string
	for _, d := range r.Docs {
		out = append(out, slackLink(d.Title, d.URL))
	}
	for _, d := range r.Decisions {
		out = append(out, slackLink(fmt.Sprintf("ADR-%d %s", d.Number, d.Title), d.URL))
	}
	return out
}

// slackList joins items already escaped, saying how many of total it left
// out.
func slackList(items []string, total int) string {
	shown := items[:min(len(items), slackListMax)]
	text := strings.Join(shown, ", ")
	if more := total - len(shown); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text
}

func slackLink(text, url string) string {
	// A URL with a link delimiter in it could carry its own <!channel>.
	if url == "" || strings.ContainsAny(url, "<>| \n") {
		return slackEscape(text)
	}
	// A "|" would end the URL part of the link early.
	return "<" + url + "|" + slackEscape(strings.ReplaceAll(text, "|", "/")) + ">"
}

// slackEscape escapes the three characters mrkdwn reserves for links and
// mentions, so a name can never ping a channel.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
