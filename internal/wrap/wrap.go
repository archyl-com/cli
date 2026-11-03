// Package wrap runs a coding agent inside an Archyl work session: briefing in,
// heartbeats while it works, changed files out.
package wrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// HeartbeatEvery keeps a session well inside the server's 30-minute TTL.
const HeartbeatEvery = 10 * time.Minute

// Preamble tells the agent the session is handled for it, so it does not open
// a second one through the MCP tools.
func Preamble(sessionID string) string {
	return fmt.Sprintf(`You are working under the Archyl harness. Work session %s is already open for
this task and will be closed for you when you exit: do not call
start_work_session or finish_work_session. Respect the guardrails below, call
recall before touching unfamiliar ground and remember what the code cannot show.

`, sessionID)
}

// MaxInlineBriefing bounds the briefing passed on the command line: Windows
// caps a whole command line at 32K characters and Linux one argument at 128K.
const MaxInlineBriefing = 8 << 10

// AgentArgs hands the briefing to agents that take one on the command line.
// Others read it from $ARCHYL_BRIEFING_FILE, file; a briefing too long for
// the command line is cut there, with a pointer to the file for the rest.
func AgentArgs(args []string, briefing, file string) []string {
	if len(args) == 0 {
		return args
	}
	if filepath.Base(args[0]) != "claude" ||
		slices.Contains(args, "--append-system-prompt") || slices.Contains(args, "--system-prompt") {
		return args
	}
	out := []string{args[0], "--append-system-prompt", inlineBriefing(briefing, file)}
	return append(out, args[1:]...)
}

func inlineBriefing(briefing, file string) string {
	if len(briefing) <= MaxInlineBriefing {
		return briefing
	}
	more := fmt.Sprintf("\n\n[The briefing goes on: read the rest in %s ($ARCHYL_BRIEFING_FILE) before you start.]", file)
	cut := briefing[:MaxInlineBriefing-len(more)]
	if i := strings.LastIndex(cut, "\n"); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, "\n") + more
}

// Exec runs the agent attached to the terminal, calling beat every interval
// until it exits, and returns its exit code. Ctrl-C reaches the agent through
// the terminal; the wrapper ignores it so it can still close the session.
func Exec(
	ctx context.Context,
	args []string,
	env []string,
	every time.Duration,
	beat func(context.Context),
) (int, error) {
	if len(args) == 0 {
		return 0, errors.New("no command to run")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start %s: %w", args[0], err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return exitErr.ExitCode(), nil
			}
			return 0, err
		case sig := <-signals:
			if sig == syscall.SIGTERM && cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		case <-ticker.C:
			beat(ctx)
		}
	}
}
