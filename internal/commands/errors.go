package commands

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/session"
	"github.com/archyl-com/cli/internal/ui"
)

// ExitError ends the program with a code and no message: the command already
// printed what the user needs (a failed check, a blocked gate).
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return "exit" }

// hint pairs an error with the command that fixes it.
type hint struct {
	text    string
	command string
}

func hintFor(err error) *hint {
	switch {
	case errors.Is(err, session.ErrNotLoggedIn), api.IsStatus(err, http.StatusUnauthorized):
		return &hint{"Sign in with", "archyl login"}
	case errors.Is(err, errNoProject), errors.Is(err, repo.ErrNotLinked):
		return &hint{"Link this repository with", "archyl init"}
	case errors.Is(err, repo.ErrNotARepository):
		return &hint{"Run it inside a git repository, or pass", "--project <id>"}
	case api.IsStatus(err, http.StatusForbidden) && strings.Contains(strings.ToLower(err.Error()), "plan"):
		return &hint{"Compare plans in the web app:", "archyl open"}
	case api.IsStatus(err, http.StatusForbidden):
		return &hint{"Check the profile and organization with", "archyl whoami"}
	}
	return nil
}

// ErrorHandler renders errors in the CLI's palette, with the fix when known.
func ErrorHandler(p *ui.Printer) fang.ErrorHandler {
	return func(w io.Writer, _ fang.Styles, err error) {
		var exit *ExitError
		if errors.As(err, &exit) {
			return
		}
		t := p.Theme()
		badge := lipgloss.NewStyle().
			Background(t.Danger).
			Foreground(lipgloss.Color("#ffffff")).
			Bold(true).
			Padding(0, 1).
			Render("ERROR")
		_, _ = lipgloss.Fprintln(w)
		_, _ = lipgloss.Fprintln(w, "  "+badge+" "+capitalize(err.Error()))
		if h := hintFor(err); h != nil {
			_, _ = lipgloss.Fprintln(w)
			_, _ = lipgloss.Fprintln(w, "  "+p.Hint(h.text, h.command))
		}
		_, _ = lipgloss.Fprintln(w)
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}
