package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
