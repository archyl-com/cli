package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/report"
)

// hookMarker identifies a hook archyl wrote, so it is never mistaken for, or
// overwrites, one the user wrote.
const hookMarker = "# archyl-managed pre-push hook"

func prePushHook(failOn string) string {
	return fmt.Sprintf(`#!/bin/sh
%s — remove it with: archyl hooks uninstall
# Blocks a push that brings architecture violations at or above %q.
# Bypass once with: git push --no-verify
command -v archyl >/dev/null 2>&1 || exit 0
exec archyl check --fail-on %s --fail-open
`, hookMarker, failOn, failOn)
}

func newHooksCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Check architecture rules before every git push",
	}

	var failOn string
	var force bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the pre-push hook",
		Long: `Install a git pre-push hook that runs ` + "`archyl check`" + ` and blocks the push when a
violation reaches --fail-on. Without archyl on the PATH, or offline, the push
goes through. Skip it once with git push --no-verify.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := report.ParseFailOn(failOn); err != nil {
				return err
			}
			path, err := prePushPath(cmd, app)
			if err != nil {
				return err
			}
			if existing, err := os.ReadFile(path); err == nil && !strings.Contains(string(existing), hookMarker) && !force {
				return fmt.Errorf("%s already exists and was not written by archyl; pass --force to replace it", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(prePushHook(failOn)), 0o755); err != nil {
				return err
			}
			p := app.Printer
			p.Println(p.Check() + " Pre-push hook installed" + p.Muted(" · blocks "+failOn+" and above"))
			p.Println(p.Muted("  bypass once with ") + p.Code("git push --no-verify"))
			return nil
		},
	}
	install.Flags().StringVar(&failOn, "fail-on", "critical", "lowest severity that blocks a push")
	install.Flags().BoolVar(&force, "force", false, "replace a pre-push hook archyl did not write")

	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the pre-push hook",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := prePushPath(cmd, app)
			if err != nil {
				return err
			}
			existing, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				app.Printer.Println(app.Printer.Muted("No pre-push hook installed."))
				return nil
			}
			if err != nil {
				return err
			}
			if !strings.Contains(string(existing), hookMarker) {
				return fmt.Errorf("%s was not written by archyl; leaving it alone", path)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			app.Printer.Println(app.Printer.Check() + " Pre-push hook removed")
			return nil
		},
	}

	cmd.AddCommand(install, uninstall)
	return cmd
}

func prePushPath(cmd *cobra.Command, app *App) (string, error) {
	git, err := app.Repo(cmd.Context())
	if err != nil {
		return "", err
	}
	dir, err := git.HooksDir(cmd.Context())
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pre-push"), nil
}
