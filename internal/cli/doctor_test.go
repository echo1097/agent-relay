package cli

import (
	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/daemon"
	"agent-relay/internal/protocol"
	"agent-relay/internal/setup"
	"agent-relay/internal/storage"
)

func TestDoctorReportsAllFailuresWithoutInitialization(t *testing.T) {
	paths, err := config.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Config, []byte("broken = ["), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = doctor(context.Background(), &output, paths, "test", testClient{}, doctorProber{}, nil)
	if err == nil {
		t.Fatal("failed diagnostics succeeded")
	}
	for _, label := range []string{"Configuration readability", "Database readability", "Database writability", "Migrations", "Persistent node identity", "Tailscale installation", "Tailscale running state", "Tailnet connectivity", "Local Tailscale IP", "Daemon running", "Listener interface", "Listener port", "MCP configuration", "Local registered agents", "Discovered Agent Relay peers", "Trusted peer reachability"} {
		if !strings.Contains(output.String(), "FAIL "+label) {
			t.Fatalf("missing %s: %s", label, output.String())
		}
	}
	if _, err := os.Stat(paths.Database); !os.IsNotExist(err) {
		t.Fatal("doctor created database")
	}
}

func TestDoctorChecksActualListenerAndTrustedIdentity(t *testing.T) {
	ctx := context.Background()
	paths, _ := config.Resolve(t.TempDir())
	store, err := storage.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	remoteStore, err := storage.Open(ctx, filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer remoteStore.Close()
	remote, err := remoteStore.Node(ctx, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPeerTrust(ctx, storage.PeerTrust{NodeID: remote.ID, Name: "remote", State: storage.Trusted, TailscaleID: "remote", Address: "100.64.0.2", Port: 47832}); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(daemon.Runtime{NodeID: node.ID, Address: "0.0.0.0:9999", Version: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Lock, data, 0600); err != nil {
		t.Fatal(err)
	}
	local := protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.PublicNode(node.ID, node.Name), Version: "test"}
	remoteHello := local
	remoteHello.Node = protocol.PublicNode(remote.ID, remote.Name)
	var output bytes.Buffer
	if err := doctor(ctx, &output, paths, "test", doctorClient{}, doctorProber{local: local, remote: remoteHello}, []setup.Client{}); err == nil {
		t.Fatal("stopped daemon accepted")
	}
	for _, label := range []string{"PASS Database readability", "PASS Database writability", "PASS Persistent node identity", "PASS Discovered Agent Relay peers", "PASS Trusted peer reachability " + remote.ID, "FAIL Listener interface", "FAIL Listener port"} {
		if !strings.Contains(output.String(), label) {
			t.Fatalf("missing %s: %s", label, output.String())
		}
	}
	output.Reset()
	remoteHello.Node = local.Node
	doctor(ctx, &output, paths, "test", doctorClient{}, doctorProber{local: local, remote: remoteHello}, nil)
	if !strings.Contains(output.String(), "FAIL Trusted peer reachability "+remote.ID) {
		t.Fatal(output.String())
	}
}

func TestDoctorHealthyConfiguration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paths, _ := config.Resolve(t.TempDir())
	store, err := storage.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Minute, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(ctx, agents.Registration{DisplayName: "doctor-test"}); err != nil {
		t.Fatal(err)
	}
	remoteID, err := messaging.NewID("node")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPeerTrust(ctx, storage.PeerTrust{NodeID: remoteID, Name: "remote", State: storage.Trusted, Development: true, Address: "127.0.0.2", Port: 47832}); err != nil {
		t.Fatal(err)
	}
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, paths.Lock, logger, registry, daemon.HTTPOptions{Address: "127.0.0.1:0", Node: protocol.PublicNode(node.ID, node.Name), Version: "test", Ready: func(address net.Addr) { ready <- address }})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	var address net.Addr
	select {
	case address = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not start")
	}
	_, port, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatal(err)
	}
	data := "[network]\ndevelopment = true\nbind_address = '127.0.0.1'\nport = " + port + "\n"
	if err := os.WriteFile(paths.Config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := setup.Client{Name: "codex", Path: filepath.Join(paths.Home, "codex.toml"), Detected: true}
	if _, err := setup.Configure(client, binary, paths.Home, false); err != nil {
		t.Fatal(err)
	}
	local := protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.PublicNode(node.ID, node.Name), Version: "test"}
	remote := local
	remote.Node = protocol.PublicNode(remoteID, "remote")
	var output bytes.Buffer
	if err := doctor(ctx, &output, paths, "test", doctorClient{}, doctorProber{local: local, remote: remote}, []setup.Client{client}); err != nil {
		t.Fatalf("%v\n%s", err, output.String())
	}
	if strings.Contains(output.String(), "FAIL") {
		t.Fatal(output.String())
	}
}

func TestInstallationDoctorDoesNotHideBrokenInstallation(t *testing.T) {
	paths, _ := config.Resolve(t.TempDir())
	var output bytes.Buffer
	if err := doctor(context.Background(), &output, paths, "test", testClient{}, doctorProber{}, nil, true); err == nil {
		t.Fatal("broken installation passed")
	}
	for _, label := range []string{"FAIL Database readability", "FAIL Tailnet connectivity", "FAIL Daemon running", "NEXT MCP configuration", "NEXT Trusted peer reachability"} {
		if !strings.Contains(output.String(), label) {
			t.Fatalf("missing %s: %s", label, output.String())
		}
	}
}

func TestInstallationDoctorFirstRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	paths, _ := config.Resolve(t.TempDir())
	store, err := storage.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Minute, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- daemon.Run(ctx, paths.Lock, logger, registry, daemon.HTTPOptions{Address: "127.0.0.1:0", Node: protocol.PublicNode(node.ID, node.Name), Version: "test", Ready: func(address net.Addr) { ready <- address }})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	var address net.Addr
	select {
	case address = <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not start")
	}
	_, port, err := net.SplitHostPort(address.String())
	if err != nil {
		t.Fatal(err)
	}
	data := "[network]\ndevelopment = true\nbind_address = '127.0.0.1'\nport = " + port + "\n"
	if err := os.WriteFile(paths.Config, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	client := setup.Client{Name: "codex", Path: filepath.Join(paths.Home, "codex.toml"), Detected: true}
	if _, err := setup.Configure(client, binary, paths.Home, false); err != nil {
		t.Fatal(err)
	}
	local := protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.PublicNode(node.ID, node.Name), Version: "test"}
	remote := local
	remote.Node = protocol.PublicNode(node.ID, "remote")
	var output bytes.Buffer
	if err := doctor(ctx, &output, paths, "test", connectedClient{}, doctorProber{local: local, remote: remote}, []setup.Client{client}, true); err != nil {
		t.Fatalf("%v\n%s", err, output.String())
	}
	for _, label := range []string{"NEXT Local registered agents", "NEXT Discovered Agent Relay peers", "NEXT Trusted peer reachability"} {
		if !strings.Contains(output.String(), label) {
			t.Fatalf("missing %s: %s", label, output.String())
		}
	}
	if strings.Contains(output.String(), "FAIL") {
		t.Fatal(output.String())
	}
}
