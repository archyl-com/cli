package commands

import (
	"fmt"
	"os"

	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

// printMarkdown renders markdown in a terminal and prints it raw otherwise.
// Raw is what an agent running the command from its shell wants: compact,
// structured, no escape codes.
func (a *App) printMarkdown(md string) {
	p := a.Printer
	if p.Interactive() || p.Embedded {
		p.Println(tui.Markdown(md, min(p.Width(), 110), p.Dark()))
		return
	}
	_, _ = fmt.Fprintln(p.Out, ui.Sanitize(md))
}

// agentName names who is acting, for memories and sessions: an explicit
// ARCHYL_AGENT, then the coding agent the CLI runs inside, else "cli".
func agentName() string {
	switch {
	case os.Getenv("ARCHYL_AGENT") != "":
		return os.Getenv("ARCHYL_AGENT")
	case os.Getenv("CLAUDECODE") != "":
		return "claude-code"
	case os.Getenv("CODEX_SANDBOX") != "" || os.Getenv("CODEX_HOME") != "":
		return "codex"
	case os.Getenv("CURSOR_TRACE_ID") != "":
		return "cursor"
	}
	return "cli"
}
