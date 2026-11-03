package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
	"github.com/archyl-com/cli/internal/tui"
	"github.com/archyl-com/cli/internal/ui"
)

// maxStdin bounds what `ask` reads from a pipe.
const maxStdin = 200 << 10

func newAskCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "ask <question>",
		Short: "Ask the AI assistant about your architecture",
		Long: `Ask a question about the project's architecture. The assistant answers from the
C4 model, decisions, docs and contracts, and the answer is rendered as it is
written. Pipe anything in — a diff, a stack trace, a file — and it is added to
the question.`,
		Example: `  archyl ask "Which services write to the orders database?"
  git diff main | archyl ask "Does this break any of our decisions?"
  archyl ask "What would break if we removed the Notifications service?"`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			question := strings.Join(args, " ")

			if !term.IsTerminal(os.Stdin.Fd()) {
				piped, err := io.ReadAll(io.LimitReader(os.Stdin, maxStdin))
				if err != nil {
					return err
				}
				if s := strings.TrimSpace(string(piped)); s != "" {
					question += "\n\nContext I am looking at:\n\n```\n" + s + "\n```"
				}
			}

			projectID := ""
			if ref, err := app.Project(ctx); err == nil {
				projectID = ref.ID
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			messages := []api.ChatMessage{{Role: "user", Content: question}}

			if p.JSON || !term.IsTerminal(os.Stdout.Fd()) {
				var answer strings.Builder
				err := client.Chat(ctx, projectID, messages, func(chunk string) {
					if p.JSON {
						answer.WriteString(chunk)
					} else {
						_, _ = fmt.Fprint(p.Out, ui.Sanitize(chunk))
					}
				})
				if err != nil {
					return err
				}
				if p.JSON {
					return p.WriteJSON(map[string]string{"answer": answer.String()})
				}
				_, _ = fmt.Fprintln(p.Out)
				return nil
			}

			chunks := make(chan tui.StreamChunk, 64)
			go func() {
				err := client.Chat(ctx, projectID, messages, func(chunk string) {
					chunks <- tui.StreamChunk{Text: chunk}
				})
				chunks <- tui.StreamChunk{Err: err, Done: err == nil}
			}()
			stream := tui.NewStream(p.Theme(), p.Dark(), "ask", chunks)
			if _, err := tea.NewProgram(stream, tea.WithContext(ctx)).Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) {
				return err
			}
			return stream.Err
		},
	}
}
