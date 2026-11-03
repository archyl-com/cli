// Command archyl is the Archyl CLI.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"charm.land/fang/v2"

	"github.com/archyl-com/cli/internal/buildinfo"
	"github.com/archyl-com/cli/internal/commands"
	"github.com/archyl-com/cli/internal/ui"
)

func main() {
	app := &commands.App{Printer: ui.NewPrinter(false)}
	root := commands.NewRoot(app)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := fang.Execute(ctx, root,
		fang.WithVersion(buildinfo.Version),
		fang.WithCommit(buildinfo.Commit),
		fang.WithColorSchemeFunc(commands.ColorScheme),
		fang.WithErrorHandler(commands.ErrorHandler(app.Printer)),
	)
	stop()

	var exit *commands.ExitError
	switch {
	case errors.As(err, &exit):
		os.Exit(exit.Code)
	case err != nil:
		os.Exit(1)
	}
}
