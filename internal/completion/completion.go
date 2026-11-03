// Package completion installs archyl's shell completion script where each
// shell loads it from on its own, without touching rc files.
package completion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Shell is a shell archyl completes in.
type Shell string

// The supported shells.
const (
	Bash       Shell = "bash"
	Zsh        Shell = "zsh"
	Fish       Shell = "fish"
	PowerShell Shell = "powershell"
)

// Shells lists the supported shells.
var Shells = []Shell{Bash, Zsh, Fish, PowerShell}

// ErrUnknownShell is returned when the user's shell is not one archyl
// completes in.
var ErrUnknownShell = errors.New("unknown shell")

// Parse reads a shell name, as $SHELL or a flag gives it.
func Parse(name string) (Shell, error) {
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe") {
	case "bash":
		return Bash, nil
	case "zsh":
		return Zsh, nil
	case "fish":
		return Fish, nil
	case "pwsh", "powershell":
		return PowerShell, nil
	}
	return "", fmt.Errorf("%w %q: use bash, zsh, fish or powershell", ErrUnknownShell, name)
}

// Env is what locating a completion script depends on.
type Env struct {
	Getenv func(string) string
	Home   string
	GOOS   string
	// Fpath lists zsh's function directories; nil skips them.
	Fpath func() []string
}

// Detect returns the user's login shell.
func Detect(env Env) (Shell, error) {
	if shell := env.Getenv("SHELL"); shell != "" {
		return Parse(shell)
	}
	if env.GOOS == "windows" || env.Getenv("PSModulePath") != "" {
		return PowerShell, nil
	}
	return "", fmt.Errorf("%w: $SHELL is not set", ErrUnknownShell)
}

// Target is where a shell's completion script goes.
type Target struct {
	Path string
	// RCFile and RCLine are what the shell's startup file needs for the
	// script to load, when its location is not loaded on its own.
	RCFile string
	RCLine string
	// Note tells what else loading the script depends on.
	Note string
}

// TargetFor locates the conventional user-level completion file for shell.
func TargetFor(shell Shell, env Env) Target {
	switch shell {
	case Zsh:
		return zshTarget(env)
	case Fish:
		base := firstNonEmpty(env.Getenv("XDG_CONFIG_HOME"), filepath.Join(env.Home, ".config"))
		return Target{Path: filepath.Join(base, "fish", "completions", "archyl.fish")}
	case PowerShell:
		dir := filepath.Join(env.Home, ".config", "powershell")
		if env.GOOS == "windows" {
			dir = filepath.Join(env.Home, "Documents", "PowerShell")
		}
		path := filepath.Join(dir, "archyl.ps1")
		return Target{
			Path:   path,
			RCFile: filepath.Join(dir, "Microsoft.PowerShell_profile.ps1"),
			RCLine: `. "` + path + `"`,
		}
	default:
		base := firstNonEmpty(env.Getenv("XDG_DATA_HOME"), filepath.Join(env.Home, ".local", "share"))
		return Target{
			Path: filepath.Join(base, "bash-completion", "completions", "archyl"),
			Note: "bash loads it through bash-completion 2 (brew install bash-completion@2, apt install bash-completion).",
		}
	}
}

// zshTarget prefers a directory of zsh's fpath the user owns, and falls
// back to ~/.zfunc, which .zshrc must add to fpath.
func zshTarget(env Env) Target {
	if env.Fpath != nil {
		for _, dir := range env.Fpath() {
			if dir != "" && strings.HasPrefix(dir, env.Home+string(filepath.Separator)) && writable(dir) {
				return Target{Path: filepath.Join(dir, "_archyl")}
			}
		}
	}
	zdot := firstNonEmpty(env.Getenv("ZDOTDIR"), env.Home)
	return Target{
		Path:   filepath.Join(env.Home, ".zfunc", "_archyl"),
		RCFile: filepath.Join(zdot, ".zshrc"),
		RCLine: "fpath=(~/.zfunc $fpath); autoload -Uz compinit && compinit",
	}
}

// ZshFpath asks zsh for its function path. Startup files are not read, so
// directories .zshrc adds are missing.
func ZshFpath() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "zsh", "-fc", `print -rl -- $fpath`).Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".archyl-probe-*")
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return true
}

// Script generates root's completion script for shell.
func Script(root *cobra.Command, shell Shell) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch shell {
	case Bash:
		err = root.GenBashCompletionV2(&buf, true)
	case Zsh:
		err = root.GenZshCompletion(&buf)
	case Fish:
		err = root.GenFishCompletion(&buf, true)
	case PowerShell:
		err = root.GenPowerShellCompletionWithDesc(&buf)
	default:
		err = fmt.Errorf("%w %q", ErrUnknownShell, shell)
	}
	return buf.Bytes(), err
}

// Install writes script to target.Path, and reports created, updated or
// unchanged.
func Install(target Target, script []byte) (string, error) {
	existing, err := os.ReadFile(target.Path)
	switch {
	case err == nil && bytes.Equal(existing, script):
		return "unchanged", nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	action := "created"
	if err == nil {
		action = "updated"
	}
	dir := filepath.Dir(target.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".archyl-completion-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(script); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target.Path); err != nil {
		return "", err
	}
	return action, nil
}

// NeedsRCLine reports whether target's rc file still lacks its line.
func NeedsRCLine(target Target) bool {
	if target.RCLine == "" {
		return false
	}
	raw, err := os.ReadFile(target.RCFile)
	if err != nil {
		return true
	}
	return !strings.Contains(string(raw), target.RCLine)
}

// AddRCLine appends target's line to its rc file, once.
func AddRCLine(target Target) error {
	if !NeedsRCLine(target) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target.RCFile), 0o755); err != nil {
		return err
	}
	raw, err := os.ReadFile(target.RCFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(target.RCFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if len(raw) > 0 {
		prefix = "\n"
		if raw[len(raw)-1] != '\n' {
			prefix = "\n\n"
		}
	}
	_, err = fmt.Fprintf(f, "%s# archyl shell completion\n%s\n", prefix, target.RCLine)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
