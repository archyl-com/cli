package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/cache"
	"github.com/archyl-com/cli/internal/update"
)

func newUpgradeCmd(app *App) *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade archyl to the latest release",
		Long: `Upgrade archyl the way it was installed: brew upgrade for Homebrew, the go
install command for a build from source, and otherwise in place: the release
archive for this platform is downloaded, checked against the release's
checksums.txt (and its cosign signature when cosign is on your PATH), then
swapped in for the running binary.`,
		Example: `  archyl upgrade
  archyl upgrade --check`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			p := app.Printer

			exe, err := executable()
			if err != nil {
				return err
			}
			method := update.DetectMethod(exe, os.Getenv)
			current := currentVersion()

			var latest string
			err = p.Spin("Looking for the latest release…", func() error {
				ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				latest, err = update.Releases{}.Latest(ctx)
				return err
			})
			if err != nil {
				return fmt.Errorf("find the latest release: %w", err)
			}
			_ = cache.Save(update.CacheKey, latest)
			available := update.Newer(current, latest)

			if p.JSON {
				return p.WriteJSON(map[string]any{
					"current": current, "latest": latest, "updateAvailable": available, "method": method,
				})
			}
			if latest == "" {
				p.Println(p.Muted("No release published yet."))
				return nil
			}
			if current == "dev" {
				p.Println(p.Bang() + " This is a development build; the latest release is " + p.Bold(latest) + ".")
				p.Println(p.Hint("Install it:", upgradeCommand(method)))
				return nil
			}
			if !available {
				p.Println(p.Check() + " archyl " + p.Bold(current) + " is the latest release.")
				return nil
			}
			if check {
				p.Println(p.Accent("◆") + " archyl " + p.Bold(latest) + p.Muted(" is out (you have "+current+")."))
				p.Println(p.Hint("Upgrade:", upgradeCommand(method)))
				return nil
			}

			switch method {
			case update.MethodHomebrew:
				brew := exec.CommandContext(ctx, update.BrewCommand[0], update.BrewCommand[1:]...)
				brew.Stdin, brew.Stdout, brew.Stderr = os.Stdin, p.Out, p.Err
				return brew.Run()
			case update.MethodGoInstall:
				p.Println("archyl was installed with go install. Upgrade it with:")
				p.Println("  " + p.Code(update.GoInstallCommand))
				return nil
			}

			u := update.Upgrade{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Exe: exe}
			if cosign, err := exec.LookPath("cosign"); err == nil {
				u.VerifySignature = update.Cosign(cosign)
			}
			label := fmt.Sprintf("Downloading archyl %s for %s/%s…", latest, runtime.GOOS, runtime.GOARCH)
			if err := p.Spin(label, func() error { return u.Run(ctx, latest) }); err != nil {
				if errors.Is(err, os.ErrPermission) {
					return fmt.Errorf("%w\n\n%s cannot be replaced by your user; rerun with sudo, or reinstall to a directory you own", err, exe)
				}
				return err
			}
			verified := "checksum verified; install cosign to check the signature too"
			if u.VerifySignature != nil {
				verified = "checksum and signature verified"
			}
			p.Println(p.Check() + " archyl " + p.Bold(latest) + p.Muted(" installed to "+exe+" ("+verified+")."))
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release is out")
	return cmd
}

// upgradeCommand is what upgrades an install made with method.
func upgradeCommand(method update.Method) string {
	switch method {
	case update.MethodHomebrew:
		return strings.Join(update.BrewCommand, " ")
	case update.MethodGoInstall:
		return update.GoInstallCommand
	}
	return "archyl upgrade"
}

// executable is the running binary with symlinks resolved, so an upgrade
// replaces the file and not a link to it.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// currentVersion is the release this binary was built from: stamped by
// GoReleaser, or recorded by go install. A build from a checkout carries a
// pseudo-version and stays "dev".
func currentVersion() string {
	if buildinfo.Version != "dev" {
		return buildinfo.Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; strings.HasPrefix(v, "v") && !strings.ContainsAny(v, "-+") {
			return v
		}
	}
	return buildinfo.Version
}
