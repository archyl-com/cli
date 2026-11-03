package commands

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/report"
)

func newProjectsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "projects",
		Short: "List the projects you can reach",
		Long: `List the Archyl projects of your organization. The one this repository is
linked to is marked. Use an ID with --project on any command, or link the
repository to another project with archyl init --force.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			client, err := app.Client()
			if err != nil {
				return err
			}
			projects, err := client.Projects(ctx)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(projects)
			}
			if len(projects) == 0 {
				p.Println(p.Muted("No project yet."))
				p.Println(p.Hint("Create one from a repository:", "archyl init --new"))
				return nil
			}
			current, _ := app.Project(ctx)
			for _, project := range projects {
				marker := "  "
				if project.ID == current.ID {
					marker = p.Check() + " "
				}
				p.Println(marker + p.Bold(project.Name) + "  " + p.Muted(project.ID))
			}
			return nil
		},
	}
}

func newRulesCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "rules",
		Short: "List the architecture rules that apply to this project",
		Long: `List the enabled conformance rules of the project and its organization, most
severe first: the rules archyl check, archyl review and the Guard hook apply.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer
			project, err := app.Project(ctx)
			if err != nil {
				return err
			}
			client, err := app.Client()
			if err != nil {
				return err
			}
			rules, err := client.Rules(ctx, project.ID)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.WriteJSON(rules)
			}
			if len(rules) == 0 {
				p.Println(p.Muted("No rule applies to this project yet."))
				return nil
			}
			sort.SliceStable(rules, func(i, j int) bool {
				return report.Severity(rules[i].Severity) > report.Severity(rules[j].Severity)
			})
			for _, rule := range rules {
				p.Println(severityBadge(p, rule.Severity) + " " + p.Bold(rule.Name) + "  " + p.Muted(rule.RuleType))
				if text := strings.TrimSpace(rule.Description); text != "" {
					p.Println("         " + p.Muted(truncate(text, 100)))
				}
			}
			return nil
		},
	}
}
