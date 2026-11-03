package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/api"
)

var memoryKinds = map[string]bool{"note": true, "convention": true, "pitfall": true}

func newRememberCmd(app *App) *cobra.Command {
	var req api.RememberRequest
	cmd := &cobra.Command{
		Use:   "remember <what the code cannot show>",
		Short: "Teach Archyl something about the architecture",
		Long: `Record a fact the code cannot show — a convention, a pitfall, a reason — on the
element it is about. Every future work session on that element, human or
agent, is briefed with it.

  --kind convention   how things are done here
  --kind pitfall      what bit someone, so it does not bite the next one
  --kind note         anything else worth knowing`,
		Example: `  archyl remember "Refund amounts are in minor units; never use floats" \
    --element "Payments" --kind pitfall`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			if !memoryKinds[req.Kind] {
				return fmt.Errorf("--kind must be note, convention or pitfall")
			}
			req.Content = strings.Join(args, " ")
			req.AgentName = agentName()
			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			result, err := client.Remember(ctx, project.ID, req)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(result)
			}
			if result.Deduplicated {
				p.Println(p.Muted("Already known — confirmed the existing memory."))
				return nil
			}
			line := p.Check() + " Remembered"
			if result.Memory != nil && result.Memory.ElementName != "" {
				line += " on " + p.Bold(result.Memory.ElementName)
			}
			p.Println(line + p.Muted(" · "+req.Kind))
			for _, s := range result.SimilarTo {
				p.Println("  " + p.Muted("similar to: "+s.Title))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Element, "element", "", "element it is about (name or ID)")
	cmd.Flags().StringVar(&req.Kind, "kind", "note", "note, convention or pitfall")
	cmd.Flags().StringVar(&req.Title, "title", "", "short title (derived from the text when omitted)")
	cmd.Flags().StringVar(&req.Supersedes, "supersedes", "", "ID or title of a memory this one replaces")
	_ = cmd.RegisterFlagCompletionFunc("element", completeElements(app))
	_ = cmd.RegisterFlagCompletionFunc("kind", cobra.FixedCompletions([]string{"note", "convention", "pitfall"}, cobra.ShellCompDirectiveNoFileComp))
	return cmd
}

func newRecallCmd(app *App) *cobra.Command {
	var opts api.RecallOptions
	cmd := &cobra.Command{
		Use:   "recall [topic]",
		Short: "Recall what Archyl remembers about a topic or element",
		Example: `  archyl recall refunds
  archyl recall --element "Payments" --kind pitfall`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p := app.Printer
			opts.Query = strings.Join(args, " ")
			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			var memories []api.Memory
			if err := p.Spin("Recalling…", func() error {
				memories, err = client.Recall(ctx, project.ID, opts)
				return err
			}); err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(memories)
			}
			if len(memories) == 0 {
				if p.Interactive() {
					p.Println(p.Muted("Nothing remembered about that yet."))
				} else {
					_, _ = fmt.Fprintln(p.Out, "No memory matches.")
				}
				return nil
			}
			app.printMarkdown(memoriesMarkdown(memories))
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Element, "element", "", "only memories about this element")
	cmd.Flags().StringVar(&opts.Kind, "kind", "", "note, convention, pitfall or session_outcome")
	cmd.Flags().IntVar(&opts.Limit, "limit", 10, "maximum memories")
	_ = cmd.RegisterFlagCompletionFunc("element", completeElements(app))
	return cmd
}

func memoriesMarkdown(memories []api.Memory) string {
	var b strings.Builder
	for _, m := range memories {
		content := strings.TrimSpace(m.Content)
		// A title the server derived from the text says nothing the text
		// does not; show the text alone then.
		derived := m.Title == "" || strings.HasPrefix(content, strings.TrimSuffix(strings.TrimSpace(m.Title), "…"))
		if derived {
			fmt.Fprintf(&b, "- **[%s]** %s", m.Kind, strings.ReplaceAll(content, "\n", "\n  "))
		} else {
			fmt.Fprintf(&b, "- **[%s] %s**", m.Kind, m.Title)
		}
		if m.ElementName != "" {
			fmt.Fprintf(&b, " — on `%s`", m.ElementName)
		}
		b.WriteString("\n")
		if !derived && content != "" {
			fmt.Fprintf(&b, "  %s\n", strings.ReplaceAll(content, "\n", "\n  "))
		}
	}
	return b.String()
}
