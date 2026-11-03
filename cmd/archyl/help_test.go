package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/archyl-com/cli/internal/commands"
	"github.com/archyl-com/cli/internal/ui"
)

// TestHelpRenders renders every command's help through fang, which parses
// each Example line as a shell command and panics on some shapes.
func TestHelpRenders(
	t *testing.T,
) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildBinary(t)

	var paths [][]string
	var walk func(cmd *cobra.Command, path []string)
	walk = func(cmd *cobra.Command, path []string) {
		for _, sub := range cmd.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			p := append(append([]string(nil), path...), sub.Name())
			paths = append(paths, p)
			walk(sub, p)
		}
	}
	walk(commands.NewRoot(&commands.App{Printer: ui.NewPrinter(false)}), nil)

	for _, path := range paths {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			cmd := exec.Command(bin, append(path, "--help")...)
			cmd.Env = isolatedEnv(t)
			out, err := cmd.CombinedOutput()
			assert.NoError(t, err, string(out))
			assert.NotContains(t, string(out), "panic")
		})
	}
}
