package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/daemon"
	"agent-relay/internal/discovery"
	"agent-relay/internal/protocol"
	"agent-relay/internal/setup"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type doctorReport struct {
	output   io.Writer
	failed   bool
	writeErr error
}

func (report *doctorReport) check(name string, ok bool, detail, remedy string) {
	state := "PASS"
	if !ok {
		state = "FAIL"
		report.failed = true
	}
	_, err := fmt.Fprintf(report.output, "%s %s: %s\n", state, name, detail)
	report.writeErr = errors.Join(report.writeErr, err)
	if !ok && remedy != "" {
		_, err = fmt.Fprintf(report.output, "  Fix: %s\n", remedy)
		report.writeErr = errors.Join(report.writeErr, err)
	}
}

func (report *doctorReport) next(name, remedy string) {
	_, err := fmt.Fprintf(report.output, "NEXT %s: %s\n", name, remedy)
	report.writeErr = errors.Join(report.writeErr, err)
}

func doctor(ctx context.Context, output io.Writer, paths config.Paths, version string, client tailscale.Client, prober discovery.Prober, clients []setup.Client, installationMode ...bool) (returnErr error) {
	installation := len(installationMode) > 0 && installationMode[0]
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	report := &doctorReport{output: output}
	defer func() {
		if report.failed {
			returnErr = errors.Join(returnErr, errors.New("doctor: checks failed; follow the remediation above"))
		}
		returnErr = errors.Join(returnErr, report.writeErr)
	}()
	report.check("Agent Relay version", version != "", version, "Rebuild or reinstall Agent Relay.")
	cfg, configErr := config.Load(paths.Config)
	configDetail := paths.Config + " (defaults for missing or omitted settings)"
	if configErr != nil {
		configDetail += ": " + configErr.Error()
	}
	report.check("Configuration readability", configErr == nil, configDetail, "Repair config.toml syntax and settings, and check file permissions. Then restart the daemon.")
	if configErr != nil {
		cfg = config.Defaults()
	}

	store, storeErr := storage.Inspect(ctx, paths.Database)
	nodeID := ""
	var trustedPeers []storage.PeerTrust
	if storeErr == nil {
		defer func() { returnErr = errors.Join(returnErr, store.Close()) }()
		storeErr = store.CheckIntegrity(ctx)
	}
	report.check("Database readability", storeErr == nil, paths.Database, "Check database and directory permissions. If damaged, stop Relay, preserve the database with its WAL/SHM files and restore a verified backup. For a new installation, run agent-relay status with this --home.")
	writeErr := storage.CheckWritable(ctx, paths.Database)
	writeDetail := "transactional write probe rolled back"
	if writeErr != nil {
		writeDetail = "SQLite write probe failed"
	}
	report.check("Database writability", writeErr == nil, writeDetail, "Check free disk space, database/directory write permissions, and competing SQLite writers. Preserve existing identities.")
	if store != nil {
		migrationErr := store.CheckMigrations(ctx)
		detail := "migration history matches this binary"
		if migrationErr != nil {
			detail = migrationErr.Error()
		}
		report.check("Migrations", migrationErr == nil, detail, "Back up the database and use the matching or newer Relay binary. Start the daemon to apply pending migrations; do not edit migration records manually.")
		node, nodeErr := store.ExistingNode(ctx)
		validNode := nodeErr == nil && (protocol.Node{ID: node.ID, Name: node.Name}).Validate() == nil
		if validNode {
			nodeID = node.ID
		}
		report.check("Persistent node identity", validNode, nodeID, "Restore the original database from backup. For a new installation only, run agent-relay status with this --home to create an identity.")
		count, agentErr := store.AgentCount(ctx, nodeID)
		if installation && validNode && agentErr == nil && count == 0 {
			report.next("Local registered agents", "Reconnect a configured MCP client to register an agent.")
		} else {
			report.check("Local registered agents", validNode && agentErr == nil && count > 0, strconv.Itoa(count), "Connect a configured MCP client, or run agent-relay agents register with this --home. Offline registrations remain valid.")
		}
		var trustErr error
		trustedPeers, trustErr = store.ListPeerTrust(ctx)
		report.check("Peer trust database", trustErr == nil, "stored trust decisions", "Check migrations and database integrity; inspect agent-relay trust-state with this --home.")
	} else {
		for _, name := range []string{"Migrations", "Persistent node identity", "Local registered agents", "Peer trust database"} {
			report.check(name, false, "database unavailable", "Resolve database readability first, then rerun doctor.")
		}
	}

	configured := 0
	detected := 0
	for _, mcpClient := range clients {
		if !mcpClient.Detected {
			continue
		}
		detected++
		checkErr := setup.Check(mcpClient, paths.Home)
		detail := mcpClient.Path
		if checkErr != nil {
			detail += ": " + checkErr.Error()
		} else {
			configured++
		}
		report.check("MCP configuration ("+mcpClient.Name+")", checkErr == nil, detail, "Run agent-relay setup "+mcpClient.Name+" --home "+strconv.Quote(paths.Home)+". Review conflicting entries before using --replace, then restart the MCP client.")
	}
	if installation && detected == 0 {
		report.next("MCP configuration", "Install Codex or Claude Code, then run agent-relay setup with this --home.")
	} else if configured == 0 {
		report.check("MCP configuration", false, "no usable client configuration detected", "Install Codex or Claude Code and run agent-relay setup with this --home.")
	}

	status, statusErr := client.Status(ctx)
	report.check("Tailscale installation", status.Installed, fmt.Sprint(status.Installed), "Install Tailscale and make the tailscale CLI available on PATH.")
	report.check("Tailscale running state", status.Running, fmt.Sprint(status.Running), "Start the Tailscale app or tailscaled; run tailscale status.")
	report.check("Tailnet connectivity", statusErr == nil && status.Connected, fmt.Sprint(status.Connected), "Run tailscale up and check login, network connectivity and tailnet access.")
	report.check("Local Tailscale IP", tailscale.IsIP(status.IP), status.IP, "Connect Tailscale with tailscale up and enable a Tailscale IPv4 address.")
	running, runningErr := daemon.Running(paths.Lock)
	report.check("Daemon running", runningErr == nil && running, fmt.Sprint(running), "Run agent-relay daemon --home "+strconv.Quote(paths.Home)+" or restart the installed service for this directory.")
	runtimeState, runtimeErr := daemon.ReadRuntime(paths.Lock)
	host, port, addressErr := net.SplitHostPort(runtimeState.Address)
	expectedHost := status.IP
	if cfg.Network.Development {
		expectedHost = cfg.Network.BindAddress
	}
	runtimeValid := running && runtimeErr == nil && addressErr == nil && runtimeState.NodeID == nodeID && nodeID != ""
	report.check("Listener interface", configErr == nil && runtimeValid && host == expectedHost, runtimeState.Address, "Restart the daemon with the current binary and configuration. Production must bind only to the current Tailscale IP; development must use explicit loopback.")
	report.check("Listener port", configErr == nil && runtimeValid && port == strconv.Itoa(cfg.Network.Port), "configured "+strconv.Itoa(cfg.Network.Port)+", running "+port, "Restart the daemon after changing network.port. Check for port conflicts; peers must use matching discovery ports.")
	if expectedHost != "" {
		hello, probeErr := prober.Hello(ctx, expectedHost, cfg.Network.Port)
		report.check("Local listener identity", configErr == nil && running && probeErr == nil && hello.Validate() == nil && hello.Node.ID == nodeID, net.JoinHostPort(expectedHost, strconv.Itoa(cfg.Network.Port)), "Start or restart this Relay daemon; check the listener port and firewall. A different node on this port means the wrong --home or process is in use.")
		report.check("Daemon version", probeErr == nil && hello.Version == version, hello.Version, "Restart the daemon with the current binary.")
	} else {
		report.check("Local listener identity", false, "no usable interface", "Restore Tailscale connectivity and restart the daemon.")
	}

	_, cacheErr := discovery.Read(filepath.Join(paths.Home, "peers.json"))
	report.check("Discovery cache", cacheErr == nil, "peers.json", "Remove only peers.json and restart Relay to rebuild this disposable cache. Preserve relay.db.")
	manager := discovery.Manager{Client: client, Prober: prober, Port: cfg.Network.Port, LocalID: nodeID}
	snapshot, refreshErr := manager.Refresh(ctx)
	peerCount := 0
	for _, peer := range snapshot.Peers {
		if peer.State == "online" {
			peerCount++
		}
		if peer.State == "invalid" || peer.State == "incompatible" {
			report.check("Peer protocol "+peer.IP, false, peer.State, "Upgrade both Relay nodes to compatible versions and verify the configured port serves Agent Relay.")
		}
	}
	if installation && configErr == nil && refreshErr == nil && peerCount == 0 {
		report.next("Discovered Agent Relay peers", "Install Relay on another tailnet device using the same port.")
	} else {
		report.check("Discovered Agent Relay peers", configErr == nil && refreshErr == nil && peerCount > 0, fmt.Sprintf("%d online", peerCount), "Start Relay on another tailnet device. Check matching ports, protocol versions, Tailscale ACLs and firewalls.")
	}
	trustedCount := 0
	for _, peer := range trustedPeers {
		if peer.State != storage.Trusted {
			continue
		}
		trustedCount++
		address := ""
		if cfg.Network.Development && peer.Development && net.ParseIP(peer.Address).IsLoopback() {
			address = peer.Address
		} else if !cfg.Network.Development && !peer.Development && status.Connected {
			for _, candidate := range status.Peers {
				if candidate.ID == peer.TailscaleID && tailscale.IsIP(candidate.IP) {
					address = candidate.IP
				}
			}
		}
		var hello protocol.Hello
		probeErr := errors.New("trusted device unavailable")
		if address != "" {
			hello, probeErr = prober.Hello(ctx, address, peer.Port)
		}
		report.check("Trusted peer reachability "+peer.NodeID, configErr == nil && probeErr == nil && hello.Validate() == nil && hello.Node.ID == peer.NodeID, address, "Start the remote daemon and check Tailscale ACLs, firewall and its saved port. Inspect trust-state; if device or Relay identity changed, verify the peer before trusting it again.")
	}
	if installation && trustedCount == 0 {
		report.next("Trusted peer reachability", "Install Relay on another tailnet device and wait for automatic discovery. Explicitly blocked nodes remain blocked.")
	} else if trustedCount == 0 {
		report.check("Trusted peer reachability", false, "no trusted peers", "Install Relay on another tailnet device and wait for automatic discovery. Inspect agent-relay peers for blocked nodes or changed device bindings.")
	}
	return nil
}

func runDiagnostics(ctx context.Context, output io.Writer, paths config.Paths, version string, client tailscale.Client, installation bool) error {
	env, err := setup.LocalEnvironment()
	if err != nil {
		return err
	}
	clients, err := setup.Detect(env)
	if err != nil {
		return err
	}
	return doctor(ctx, output, paths, version, client, discovery.NewProber(), clients, installation)
}
