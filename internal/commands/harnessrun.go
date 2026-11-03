package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/repo"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/wrap"
)

// harnessRun is a coding agent run inside an Archyl work session.
type harnessRun struct {
	Task      string
	AgentName string
	// Args is the agent's command line.
	Args      []string
	Exclusive bool
	// ChangeRequest opens a draft Architecture Change Request on finish.
	ChangeRequest bool
	// AskFinish lets the user edit the summary and choose the change request.
	AskFinish bool
	// Env is added to the agent's environment.
	Env []string
}

// runHarnessed opens a work session for the run, briefs and runs the agent,
// then attributes the files it changed and closes the session. It returns
// the agent's exit code. When Archyl cannot be reached the agent still runs,
// without a session.
func runHarnessed(
	ctx context.Context,
	app *App,
	git *repo.Git,
	project ProjectRef,
	run harnessRun,
) (int, error) {
	p := app.Printer
	client, err := app.Client()
	if err != nil {
		return 0, err
	}

	var started *api.StartSessionResult
	err = p.Spin("Opening a work session…", func() error {
		started, err = client.StartSession(ctx, project.ID, api.StartSessionRequest{
			AgentName: run.AgentName, Task: run.Task, Exclusive: run.Exclusive,
		})
		return err
	})
	var apiErr *api.Error
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == 409:
		p.Eprintln(p.Danger("✗ ") + p.Bold("Gate denied: ") + apiErr.Message)
		return 0, &ExitError{Code: 2}
	case errors.As(err, &apiErr):
		return 0, err
	case err != nil:
		// Archyl unreachable: never hold the developer's agent hostage.
		p.Eprintln(p.Bang() + " " + p.Warning("Archyl is unreachable, running without a session: ") + p.Muted(err.Error()))
		return wrap.Exec(ctx, run.Args, append(os.Environ(), run.Env...), wrap.HeartbeatEvery, func(context.Context) {})
	}

	renderBriefing(p, started)

	briefing := wrap.Preamble(started.Session.ID) + started.Markdown
	briefingFile, err := writeBriefing(briefing)
	if err != nil {
		_ = client.CancelSession(context.WithoutCancel(ctx), started.Session.ID)
		return 0, err
	}
	defer func() { _ = os.Remove(briefingFile) }()

	env := append(os.Environ(),
		"ARCHYL_SESSION_ID="+started.Session.ID,
		"ARCHYL_PROJECT_ID="+project.ID,
		"ARCHYL_BRIEFING_FILE="+briefingFile,
	)
	env = append(env, run.Env...)
	startHead := git.Head(ctx)
	startedAt := time.Now()

	code, runErr := wrap.Exec(ctx, wrap.AgentArgs(run.Args, briefing, briefingFile), env, wrap.HeartbeatEvery, func(ctx context.Context) {
		_ = client.Heartbeat(ctx, started.Session.ID)
	})
	if runErr != nil {
		_ = client.CancelSession(context.WithoutCancel(ctx), started.Session.ID)
		return 0, runErr
	}

	// The agent may have been interrupted with the context; closing the
	// session must still go through.
	ctx = context.WithoutCancel(ctx)
	changes, _ := git.Changes(ctx, startHead)
	if len(changes) > 0 {
		files := make([]api.ChangedFile, 0, len(changes))
		for _, c := range changes {
			files = append(files, api.ChangedFile{Path: c.Path, Status: c.Status})
		}
		for start := 0; start < len(files); start += 200 {
			_ = client.ReportFiles(ctx, started.Session.ID, files[start:min(start+200, len(files))])
		}
	}

	finish := api.FinishSessionRequest{
		Summary:             defaultSummary(run.AgentName, run.Task, changes, code),
		CreateChangeRequest: run.ChangeRequest,
	}
	if run.AskFinish && p.Interactive() {
		if err := askFinish(p, &finish, changes, time.Since(startedAt)); err != nil {
			finish.CreateChangeRequest = false
		}
	}

	var finished *api.FinishSessionResult
	if err := p.Spin("Closing the session…", func() error {
		finished, err = client.FinishSession(ctx, started.Session.ID, finish)
		return err
	}); err != nil {
		return code, err
	}

	line := p.Check() + " Session closed" + p.Muted(" · "+ui.Plural(len(changes), "file", "files")+" attributed")
	if finished.ChangeRequestNumber != nil {
		line += p.Muted(" · ") + p.Accent(fmt.Sprintf("change request #%d", *finished.ChangeRequestNumber)) + p.Muted(" opened for review")
	}
	p.Eprintln(line)
	if finished.Note != "" {
		p.Eprintln("  " + p.Muted(finished.Note))
	}
	return code, nil
}

// writeBriefing puts the briefing in a new private file. The name is random and
// holds nothing from the server, so an ID cannot steer it out of the temp dir.
func writeBriefing(briefing string) (string, error) {
	f, err := os.CreateTemp("", "archyl-session-*.md")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(briefing); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
