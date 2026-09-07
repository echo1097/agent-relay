package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agent-relay/internal/config"
	"agent-relay/internal/setup"
)

func runSetup(args []string, output, errorOutput io.Writer) error {
	clientName := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		clientName, args = args[0], args[1:]
	}
	if clientName == "help" {
		_, err := fmt.Fprintln(output, "Usage: agent-relay setup [codex|claude] [--home PATH] [--config PATH] [--replace]\nDetect installed clients and configure the agent-relay stdio MCP server.\n--config requires an explicit client. --replace replaces a conflicting agent-relay entry.")
		return err
	}
	if clientName != "" && clientName != "codex" && clientName != "claude" {
		return fmt.Errorf("unsupported client %q; choose codex or claude", clientName)
	}
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	home := flags.String("home", "", "Relay application directory; use the same home as the daemon")
	configPath := flags.String("config", "", "explicit client config file (requires codex or claude)")
	replace := flags.Bool("replace", false, "replace a conflicting agent-relay entry after backing up")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected setup arguments")
	}
	if *configPath != "" && clientName == "" {
		return errors.New("--config requires setup codex or setup claude")
	}
	paths, err := config.Resolve(*home)
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
	env, err := setup.LocalEnvironment()
	if err != nil {
		return err
	}
	clients, err := setup.Detect(env)
	if err != nil {
		return err
	}
	count := 0
	var setupErrors error
	for _, client := range clients {
		if clientName != "" && client.Name != clientName {
			continue
		}
		if clientName == "" && !client.Detected {
			fmt.Fprintf(output, "%s: not detected; skipped\n", client.Name)
			continue
		}
		if *configPath != "" {
			client.Path, err = filepath.Abs(*configPath)
			if err != nil {
				return err
			}
		}
		count++
		result, err := setup.Configure(client, binaryPath, paths.Home, *replace)
		if result.Backup != "" {
			fmt.Fprintf(output, "%s backup: %s\n", client.Name, result.Backup)
		}
		if err != nil {
			setupErrors = errors.Join(setupErrors, fmt.Errorf("%s: %w", client.Name, err))
			continue
		}
		state := "already configured"
		if result.Changed {
			state = "configured"
		}
		fmt.Fprintf(output, "%s: %s\n  Config: %s\n  Server: agent-relay\n  Command: %s mcp --home %s\n", client.Name, state, client.Path, binaryPath, paths.Home)
	}
	if count == 0 {
		return errors.New("no supported clients detected; install Codex or Claude Code, or run setup codex/setup claude explicitly")
	}
	if setupErrors != nil {
		return setupErrors
	}
	_, err = fmt.Fprintln(output, "Restart or reconnect the coding client to load Relay tools. Use the same --home for the daemon. Setup does not start the daemon or change client tool approvals.")
	return err
}
