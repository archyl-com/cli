package tour

import "github.com/archyl-com/cli/internal/ui"

// Sanitize strips control characters from every text of the tour. Names,
// descriptions, notes and the AI's story all come from outside the CLI, and
// an escape sequence in them could rewrite the reader's terminal.
func (t *Tour) Sanitize() {
	clean := ui.StripControl
	t.Project, t.TraceTask = clean(t.Project), clean(t.TraceTask)
	cleanAll(t.Trace)
	for i := range t.Stops {
		s := &t.Stops[i]
		for _, f := range []*string{&s.Name, &s.Type, &s.Path, &s.Description, &s.Story, &s.Why} {
			*f = clean(*f)
		}
		for _, list := range [][]string{s.Tech, s.Infrastructure, s.Owners, s.Systems, s.People, s.Externals, s.Flows} {
			cleanAll(list)
		}
		for j := range s.Uses {
			s.Uses[j].Name, s.Uses[j].Label = clean(s.Uses[j].Name), clean(s.Uses[j].Label)
		}
		for j := range s.UsedBy {
			s.UsedBy[j].Name, s.UsedBy[j].Label = clean(s.UsedBy[j].Name), clean(s.UsedBy[j].Label)
		}
		for j := range s.KeyParts {
			s.KeyParts[j].Name, s.KeyParts[j].Description = clean(s.KeyParts[j].Name), clean(s.KeyParts[j].Description)
		}
		for j := range s.Files {
			s.Files[j].Path, s.Files[j].Reason = clean(s.Files[j].Path), clean(s.Files[j].Reason)
		}
		for j := range s.Decisions {
			s.Decisions[j].Title, s.Decisions[j].Status = clean(s.Decisions[j].Title), clean(s.Decisions[j].Status)
		}
		for j := range s.Notes {
			s.Notes[j].Kind, s.Notes[j].Text = clean(s.Notes[j].Kind), clean(s.Notes[j].Text)
		}
		for j := range s.Rules {
			s.Rules[j].Name, s.Rules[j].Severity = clean(s.Rules[j].Name), clean(s.Rules[j].Severity)
		}
	}
}

func cleanAll(list []string) {
	for i := range list {
		list[i] = ui.StripControl(list[i])
	}
}
