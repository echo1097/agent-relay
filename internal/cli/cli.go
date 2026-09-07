package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/config"
	"agent-relay/internal/daemon"
	"agent-relay/internal/logging"
	"agent-relay/internal/storage"
)

const usage = `Agent Relay

Usage: agent-relay <command> [--home PATH]

Commands:
  daemon    Run the local daemon and presence checks in the foreground
  status    Initialize local storage and show daemon, node, and database status
  agents    List, inspect, register, or update local agents (agents help)
  version   Print the binary version
  help      Show this help

Options for daemon, status, and agent actions:
  --home PATH   Application directory (default: ~/.agent-relay)
`

func Run(ctx context.Context, args []string, output, errorOutput io.Writer, version string) (returnErr error) {
	if len(args) == 0 {
		_, err := fmt.Fprint(output, usage)
		return err
	}
	command := args[0]
	switch command {
	case "help", "--help", "-h":
		if len(args) != 1 {
			return errors.New("help does not accept arguments")
		}
		_, err := fmt.Fprint(output, usage)
		return err
	case "version":
		if len(args) != 1 {
			return errors.New("version does not accept arguments")
		}
		_, err := fmt.Fprintf(output, "agent-relay %s\n", version)
		return err
	case "daemon", "status", "agents":
	default:
		return fmt.Errorf("unknown command %q; run agent-relay help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	home := flags.String("home", "", "application directory")
	commandArgs := args[1:]
	var agentFlags *agentOptions
	if command == "agents" {
		if len(commandArgs) > 0 && commandArgs[0] == "help" {
			_, err := fmt.Fprint(output, agentUsage)
			return err
		}
		agentFlags, commandArgs, returnErr = agentArguments(flags, commandArgs)
		if returnErr != nil {
			return returnErr
		}
	}
	if err := flags.Parse(commandArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	paths, err := config.Resolve(*home)
	if err != nil {
		return err
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	if err := paths.Ensure(); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(paths.Logs, "agent-relay.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, logFile.Close()) }()
	logger, err := logging.New(io.MultiWriter(errorOutput, logFile), cfg.Logging.Level)
	if err != nil {
		return err
	}
	store, err := storage.Open(ctx, paths.Database)
	if err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	node, err := store.Node(ctx, hostname)
	if err != nil {
		return fmt.Errorf("load node identity: %w", err)
	}
	logger.Debug("local storage initialized", "node_id", node.ID)
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Duration(cfg.Presence.OfflineAfterSeconds) * time.Second, Logger: logger})
	if err != nil {
		return err
	}
	if command == "daemon" {
		return daemon.Run(ctx, paths.Lock, logger, registry)
	}
	if command == "agents" {
		return runAgents(ctx, registry, agentFlags, output)
	}
	running, err := daemon.Running(paths.Lock)
	if err != nil {
		return err
	}
	daemonState := "Stopped"
	if running {
		daemonState = "Running (local registry)"
	}
	schemaVersion, err := store.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "Agent Relay %s\n\nDaemon\n  %s\n\nNode\n  %s\n  %s\n\nDatabase\n  Ready\n  %s\n  Schema version: %d\n\nConfiguration\n  %s (defaults for omitted settings)\n", version, daemonState, node.Name, node.ID, paths.Database, schemaVersion, paths.Config)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(output, "\nLocal agents\n")
	if err != nil {
		return err
	}
	return listAgents(ctx, registry, output)
}
