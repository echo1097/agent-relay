package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"agent-relay/installers"
)

func runMaintenance(ctx context.Context, action string, args []string, output, errorOutput io.Writer) error {
	helpText := "Usage: agent-relay uninstall\nRemove this managed installation, client entries, startup instructions, and unchanged managed skills.\nData, identities, messages, logs, and backups are retained. No network connection is needed.\n"
	if action == "update" {
		helpText = "Usage: agent-relay update [--version TAG]\nUpdate this managed installation to the latest release, or the specified release tag.\nReuses the recorded data directory and service name, then restarts the background service.\n"
	}
	if len(args) == 1 && args[0] == "help" {
		_, err := fmt.Fprint(output, helpText)
		return err
	}
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	flags.Usage = func() { fmt.Fprint(output, helpText) }
	releaseVersion := ""
	if action == "update" {
		flags.StringVar(&releaseVersion, "version", "", "release tag; defaults to the latest published release")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected %s arguments", action)
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}
	return installers.Run(ctx, action, binaryPath, releaseVersion, output, errorOutput)
}
