package cli

import (
	"agent-relay/internal/config"
	"agent-relay/internal/protocol"
	"agent-relay/internal/tailscale"
	"bytes"
	"context"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommands(t *testing.T) {
	home := filepath.Join(t.TempDir(), "relay")
	for _, args := range [][]string{{"version"}, {"help"}} {
		var output bytes.Buffer
		if err := Run(context.Background(), args, &output, &output, "test-version"); err != nil {
			t.Fatal(err)
		}
		if output.Len() == 0 {
			t.Fatal("empty output")
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("unexpected application directory")
	}
	var output bytes.Buffer
	if err := runWithClient(context.Background(), []string{"status", "--home", home}, &output, &output, "test-version", testClient{}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Stopped", "node_", "Schema version: 5", "test-version"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q in %s", expected, output.String())
		}
	}
	for _, args := range [][]string{{"peers", "extra"}, {"status", "extra"}, {"version", "extra"}, {"status", "--unknown"}} {
		if err := Run(context.Background(), args, &output, &output, "test"); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}

func TestAgentCommands(t *testing.T) {
	home := t.TempDir()
	run := func(args ...string) (string, error) {
		t.Helper()
		var output, errorOutput bytes.Buffer
		commandArgs := append([]string{"agents"}, args...)
		commandArgs = append(commandArgs, "--home", home)
		err := Run(context.Background(), commandArgs, &output, &errorOutput, "test")
		return output.String(), err
	}
	output, err := run("list")
	if err != nil || !strings.Contains(output, "No local agents") {
		t.Fatalf("empty list: %q, %v", output, err)
	}
	output, err = run("register", "--name", "cli-agent", "--task", "test", "--file", "a.go", "--file", "b.go")
	if err != nil {
		t.Fatal(err)
	}
	agentID := strings.TrimSpace(output)
	if !strings.HasPrefix(agentID, "agent_") {
		t.Fatalf("registration output: %q", output)
	}
	for _, args := range [][]string{
		{"get", "--id", agentID},
		{"set-status", "--id", agentID, "--state", "busy"},
		{"heartbeat", "--id", agentID},
		{"update-metadata", "--id", agentID, "--task", "changed"},
	} {
		output, err := run(args...)
		if err != nil || !strings.Contains(output, agentID) {
			t.Fatalf("command %v: %q, %v", args, output, err)
		}
	}
	output, err = run("list")
	if err != nil || !strings.Contains(output, "busy") || !strings.Contains(output, "changed") {
		t.Fatalf("list: %q, %v", output, err)
	}
	if _, err := run("disconnect", "--id", agentID); err != nil {
		t.Fatal(err)
	}
	output, err = run("get", "--id", agentID)
	if err != nil || !strings.Contains(output, `"status": "offline"`) {
		t.Fatalf("disconnect: %q, %v", output, err)
	}
	for _, args := range [][]string{{"get"}, {"register"}, {"heartbeat", "--id", "unknown"}, {"set-status", "--id", agentID, "--state", "wrong"}, {"list", "--task", "ignored"}} {
		if _, err := run(args...); err == nil {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
}

type testClient struct{}

func (testClient) Status(context.Context) (tailscale.Status, error) { return tailscale.Status{}, nil }

type connectedClient struct{}

func (connectedClient) Status(context.Context) (tailscale.Status, error) {
	return tailscale.Status{Installed: true, Running: true, Connected: true, IP: "100.64.0.1"}, nil
}

func TestProductionBinding(t *testing.T) {
	cfg := config.Defaults()
	address, err := listenAddress(context.Background(), cfg, connectedClient{})
	if err != nil || address != "100.64.0.1:47832" {
		t.Fatalf("%s %v", address, err)
	}
	if _, err := listenAddress(context.Background(), cfg, testClient{}); err == nil {
		t.Fatal("disconnected production accepted")
	}
	for _, ip := range []string{"0.0.0.0", "::", "127.0.0.1", "192.168.1.1"} {
		cfg.Network.BindAddress = ip
		if _, err := listenAddress(context.Background(), cfg, connectedClient{}); err == nil {
			t.Fatalf("unsafe production bind %s", ip)
		}
	}
	cfg.Network.Development = true
	cfg.Network.BindAddress = "127.0.0.1"
	address, err = listenAddress(context.Background(), cfg, testClient{})
	if err != nil || address != "127.0.0.1:47832" {
		t.Fatalf("%s %v", address, err)
	}
	cfg.Network.BindAddress = "0.0.0.0"
	if _, err := listenAddress(context.Background(), cfg, testClient{}); err == nil {
		t.Fatal("development wildcard accepted")
	}
}

func TestPeerAndDoctorCommands(t *testing.T) {
	var output bytes.Buffer
	home := t.TempDir()
	if err := runWithClient(context.Background(), []string{"peers", "--home", home}, &output, &output, "test", testClient{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No peers discovered") || !strings.Contains(output.String(), "stale") {
		t.Fatal(output.String())
	}
	output.Reset()
	if err := runWithClient(context.Background(), []string{"doctor", "--home", home}, &output, &output, "test", testClient{}); err == nil {
		t.Fatal("doctor accepted disconnected Tailscale")
	}
	if !strings.Contains(output.String(), "tailscale up") {
		t.Fatal(output.String())
	}
}

type doctorClient struct{}

func (doctorClient) Status(context.Context) (tailscale.Status, error) {
	return tailscale.Status{Installed: true, Running: true, Connected: true, IP: "100.64.0.1", Peers: []tailscale.Peer{{ID: "remote", IP: "100.64.0.2"}}}, nil
}

type doctorProber struct{ local, remote protocol.Hello }

func (prober doctorProber) Hello(_ context.Context, ip string, _ int) (protocol.Hello, error) {
	if ip == "100.64.0.1" {
		return prober.local, nil
	}
	return prober.remote, nil
}

func TestDoctorHealthy(t *testing.T) {
	local := protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.Node{ID: "node_" + uuid.Must(uuid.NewV7()).String(), Name: "local"}, Version: "test"}
	remote := local
	remote.Node = protocol.Node{ID: "node_" + uuid.Must(uuid.NewV7()).String(), Name: "remote"}
	var output bytes.Buffer
	err := runDoctor(context.Background(), &output, config.Defaults(), doctorClient{}, doctorProber{local: local, remote: remote}, local.Node.ID, true)
	if err != nil || !strings.Contains(output.String(), "online") || !strings.Contains(output.String(), "100.64.0.1:47832: true") {
		t.Fatalf("%s %v", output.String(), err)
	}
}
