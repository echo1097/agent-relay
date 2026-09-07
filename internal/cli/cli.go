package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/config"
	"agent-relay/internal/daemon"
	"agent-relay/internal/discovery"
	"agent-relay/internal/logging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
	"agent-relay/internal/transport"
)

const usage = `Agent Relay

Usage: agent-relay <command> [--home PATH]

Commands:
  daemon    Run the local daemon and presence checks in the foreground
  status    Initialize local storage and show daemon, node, and database status
  peers     Show cached peer discovery and last-seen state
  trust     Trust a peer by node ID or unique name
  block     Block a peer immediately
  untrust   Reset a peer to unknown
  trust-state  Inspect peer trust and device binding
  doctor    Check Tailscale and live peer reachability
  agents    List, inspect, register, or update local agents (agents help)
  messages  Queue messages or inspect local inboxes (messages help)
  version   Print the binary version
  help      Show this help

Options for daemon, status, peers, doctor, and agent actions:
  --home PATH   Application directory (default: ~/.agent-relay)
`

func Run(ctx context.Context, args []string, output, errorOutput io.Writer, version string) error {
	return runWithClient(ctx, args, output, errorOutput, version, tailscale.New())
}

func runWithClient(ctx context.Context, args []string, output, errorOutput io.Writer, version string, client tailscale.Client) (returnErr error) {
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
	case "daemon", "status", "agents", "peers", "doctor", "messages", "trust", "block", "untrust", "trust-state":
	default:
		return fmt.Errorf("unknown command %q; run agent-relay help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	home := flags.String("home", "", "application directory")
	commandArgs := args[1:]
	trustCommand := command == "trust" || command == "block" || command == "untrust" || command == "trust-state"
	peerValue := ""
	if trustCommand && len(commandArgs) > 0 && !strings.HasPrefix(commandArgs[0], "-") {
		peerValue, commandArgs = commandArgs[0], commandArgs[1:]
	}
	var messageFlags *messageOptions
	if command == "messages" {
		if len(commandArgs) > 0 && commandArgs[0] == "help" {
			_, err := fmt.Fprint(output, messageUsage)
			return err
		}
		messageFlags, commandArgs, returnErr = messageArguments(flags, commandArgs)
		if returnErr != nil {
			return returnErr
		}
	}
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
		if trustCommand && peerValue == "" && flags.NArg() == 1 {
			peerValue = flags.Arg(0)
		} else {
			return errors.New("unexpected positional arguments")
		}
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
	if trustCommand {
		snapshot, err := discovery.Read(filepath.Join(paths.Home, "peers.json"))
		if err != nil {
			return err
		}
		if peerValue == "" {
			if command == "trust" || command == "trust-state" {
				return showTrust(ctx, output, store, snapshot)
			}
			return errors.New("specify a peer node ID or unique name")
		}
		peer, err := resolveTrustPeer(ctx, store, cfg, snapshot, peerValue)
		if err != nil {
			return err
		}
		if peer.NodeID == node.ID {
			return errors.New("the local node is not a peer")
		}
		if command == "trust-state" {
			saved, err := store.PeerTrust(ctx, peer.NodeID)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(output, "%s  %s  %s\n  Tailscale device: %s\n  Development binding: %t\n", peer.Name, peer.NodeID, saved.State, saved.TailscaleID, saved.Development)
			return err
		}
		state := storage.Unknown
		if command == "trust" {
			state = storage.Trusted
		}
		if command == "block" {
			state = storage.Blocked
		}
		peer, err = setTrust(ctx, store, cfg, client, discovery.NewProber(), peer, state)
		if err != nil {
			return err
		}
		logger.Info("trust changed", "node_id", peer.NodeID, "state", peer.State)
		_, err = fmt.Fprintf(output, "%s  %s  %s\n", peer.Name, peer.NodeID, peer.State)
		return err
	}
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Duration(cfg.Presence.OfflineAfterSeconds) * time.Second, Logger: logger})
	if err != nil {
		return err
	}
	if command == "messages" {
		service := transport.New(store, node.ID, "", cfg, logger)
		defer service.Client.CloseIdleConnections()
		return runMessages(ctx, service, messageFlags, output)
	}
	if command == "daemon" {
		address, err := listenAddress(ctx, cfg, client)
		if err != nil {
			return err
		}
		boundIP, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		localIP := boundIP
		if cfg.Network.Development {
			boundIP = ""
		}
		manager := discovery.Manager{Store: store, Client: client, Prober: discovery.NewProber(), Port: cfg.Network.Port, LocalID: node.ID, BoundIP: boundIP, Path: filepath.Join(paths.Home, "peers.json"), Logger: logger}
		delivery := transport.New(store, node.ID, localIP, cfg, logger)
		delivery.Tailscale = client
		return daemon.Run(ctx, paths.Lock, logger, registry, daemon.HTTPOptions{Delivery: delivery, Address: address, Node: protocol.PublicNode(node.ID, node.Name), Version: version, Background: func(runCtx context.Context) {
			manager.Run(runCtx, time.Duration(cfg.Discovery.IntervalSeconds)*time.Second)
		}})
	}
	if command == "agents" {
		return runAgents(ctx, registry, agentFlags, output)
	}
	running, err := daemon.Running(paths.Lock)
	if err != nil {
		return err
	}
	if command == "doctor" {
		snapshot, err := discovery.Read(filepath.Join(paths.Home, "peers.json"))
		if err != nil {
			return err
		}
		if err := showTrust(ctx, output, store, snapshot); err != nil {
			return err
		}
		return runDoctor(ctx, output, cfg, client, discovery.NewProber(), node.ID, running)
	}
	snapshot, err := discovery.Read(filepath.Join(paths.Home, "peers.json"))
	if err != nil {
		return fmt.Errorf("read peer cache: %w", err)
	}
	if command == "peers" {
		if err := showTrust(ctx, output, store, snapshot); err != nil {
			return err
		}
		return showPeers(output, snapshot, running, cfg.Discovery.IntervalSeconds)
	}
	daemonState := "Stopped"
	if running {
		daemonState = "Running"
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
	if err := listAgents(ctx, registry, output); err != nil {
		return err
	}
	if _, err := showTailscale(ctx, output, client); err != nil {
		return err
	}
	if err := showTrust(ctx, output, store, snapshot); err != nil {
		return err
	}
	return showPeers(output, snapshot, running, cfg.Discovery.IntervalSeconds)
}
