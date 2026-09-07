package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"agent-relay/internal/service"
)

func runService(ctx context.Context, args []string, output, errorOutput io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintln(output, "Usage: agent-relay service <install|uninstall|start|stop|restart|status> [--name NAME]\nInstall also accepts --home PATH. Other actions use the recorded home.\nInstall copies the current executable, enables login startup, and starts the service.\nDefault name: agent-relay. Services run as the current user, without sudo.")
		return err
	}
	flags := flag.NewFlagSet("service "+args[0], flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	name := flags.String("name", "agent-relay", "service instance name")
	home := flags.String("home", "", "Relay home (install only)")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected service arguments")
	}
	manager, err := service.New(*name)
	if err != nil {
		return err
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		return err
	}
	return manager.Execute(ctx, args[0], *home, binaryPath, output)
}
