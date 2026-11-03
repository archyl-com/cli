package commands

import (
	"image/color"
	"os"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/charmbracelet/x/term"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/ci"
	"github.com/archyl-com/cli/internal/ui"
	"github.com/archyl-com/cli/internal/update"
)

// NewRoot builds the command tree.
func NewRoot(app *App) *cobra.Command {
	var resume bool
	root := &cobra.Command{
		Use:   "archyl",
		Short: "Your architecture, in the terminal and in your agents",
		Long: `archyl connects your repository to its architecture in Archyl.

Find where a file sits in the C4 model, see what a branch affects and who to
tell, gate pull requests on your architecture rules, brief coding agents
before they touch code and guard what they write.

Run archyl on its own for a session, archyl -c to pick up the last one.`,
		Example: `  # Sign in, link this repository, connect your agents
  archyl login && archyl init && archyl setup

  # Explore and ask
  archyl map
  archyl ask "Which services write to the orders database?"

  # Before the pull request
  archyl review

  # Run Claude Code under the harness
  archyl wrap -- claude`,
		Args: cobra.NoArgs,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			app.Printer.JSON = app.json
		},
		PersistentPostRun: func(cmd *cobra.Command, _ []string) {
			if cmd.Name() != "upgrade" {
				notifyUpdate(app)
			}
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !app.Printer.Interactive() {
				return cmd.Help()
			}
			return runShell(cmd, app, resume)
		},
	}
	root.Flags().BoolVarP(&resume, "continue", "c", false, "reopen this repository's last session")

	root.PersistentFlags().StringVar(&app.profile, "profile", "", "profile to use (see `archyl profile`)")
	root.PersistentFlags().StringVarP(&app.project, "project", "p", "", "project ID (defaults to the repository's .archyl.json)")
	root.PersistentFlags().BoolVar(&app.json, "json", false, "print machine-readable JSON")
	root.PersistentFlags().BoolVar(&app.noCache, "no-cache", false, "fetch the model from the API instead of the local cache (or ARCHYL_NO_CACHE=1)")

	groups := []struct {
		id, title string
		cmds      []*cobra.Command
	}{
		{"start", "Getting started", []*cobra.Command{newLoginCmd(app), newInitCmd(app), newSetupCmd(app), newDoctorCmd(app), newProjectsCmd(app)}},
		{"explore", "Explore", []*cobra.Command{newActivityCmd(app), newMapCmd(app), newDiagramCmd(app), newTourCmd(app), newHistoryCmd(app), newSearchCmd(app), newShowCmd(app), newWhereCmd(app), newWhyCmd(app), newImpactCmd(app), newIncidentCmd(app), newAskCmd(app), newADRCmd(app), newDiscoverCmd(app), newStatusCmd(app), newOpenCmd(app), newContextCmd(app)}},
		{"agents", "Coding agents", []*cobra.Command{newPlanCmd(app), newBriefCmd(app), newRememberCmd(app), newRecallCmd(app), newDecideCmd(app), newWrapCmd(app), newFixCmd(app), newLSPCmd(app), newRunsCmd(app), newAgentsCmd(app), newMCPCmd(app), newGuardCmd(app), newSessionCmd(app), newStatuslineCmd(app)}},
		{"ci", "Checks and CI", []*cobra.Command{newCheckCmd(app), newRulesCmd(app), newReviewCmd(app), newCRCmd(app), newRadarCmd(app), newHooksCmd(app), newDriftCmd(app), newCodeownersCmd(app), newDSLCmd(app), newSyncCmd(app), newReleaseCmd(app)}},
		{"account", "Account and shell", []*cobra.Command{newInboxCmd(app), newWhoamiCmd(app), newProfileCmd(app), newLogoutCmd(app), newPromptCmd(app), newCacheCmd(app), newUpgradeCmd(app)}},
	}
	for _, g := range groups {
		root.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
		for _, c := range g.cmds {
			c.GroupID = g.id
			root.AddCommand(c)
		}
	}
	root.SetHelpCommandGroupID("account")
	root.SetCompletionCommandGroupID("account")
	addCompletionInstall(root, app)
	root.AddCommand(newUpdateCheckCmd())
	root.AddCommand(newRefreshModelCmd(app))
	return root
}

// notifyUpdate mentions a newer release once the command is done, and starts
// a background check when the last one is a day old. It stays silent in
// scripts, in CI, and for development builds.
func notifyUpdate(app *App) {
	if buildinfo.Version == "dev" || os.Getenv("ARCHYL_NO_UPDATE_NOTIFIER") != "" || ci.Detect(os.Getenv).IsCI() ||
		!term.IsTerminal(os.Stderr.Fd()) || app.Printer.JSON {
		return
	}
	entry, _ := cache.Load[string](update.CacheKey)
	if !entry.Fresh(update.CheckEvery) {
		spawnSelf("__update-check")
	}
	if entry != nil && update.Newer(buildinfo.Version, entry.Value) {
		p := app.Printer
		p.Eprintln()
		p.Eprintln(p.Accent("◆") + " archyl " + p.Bold(entry.Value) + p.Muted(" is out (you have "+buildinfo.Version+"). ") +
			p.Hint("Upgrade:", "archyl upgrade"))
	}
}

// ColorScheme maps the CLI palette onto fang's help and error styling.
func ColorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	t := ui.NewThemeFunc(c)
	return fang.ColorScheme{
		Base:           t.Text,
		Title:          t.Accent,
		Description:    t.Text,
		Codeblock:      c(lipgloss.Color("#f4f4f5"), lipgloss.Color("#18181b")),
		Program:        t.Accent,
		DimmedArgument: t.Muted,
		Comment:        t.Muted,
		Flag:           t.Component,
		FlagDefault:    t.Muted,
		Command:        t.Container,
		QuotedString:   t.Code,
		Argument:       t.Text,
		Help:           t.Muted,
		Dash:           t.Muted,
		ErrorHeader:    [2]color.Color{lipgloss.Color("#ffffff"), t.Danger},
		ErrorDetails:   t.Danger,
	}
}
