package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent-relay/internal/discovery"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
)

func TestTrustCommands(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	peerID, _ := messaging.NewID("node")
	advertisedID := peerID
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		json.NewEncoder(writer).Encode(protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.Node{ID: advertisedID, Name: "peer"}, Version: "test"})
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	port := strings.Split(address, ":")[1]
	cfg := fmt.Sprintf("[network]\ndevelopment = true\nbind_address = \"127.0.0.1\"\n[[trusted_peers]]\nnode_id = %q\naddress = \"127.0.0.1\"\nport = %s\n", peerID, port)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		var output, errorOutput bytes.Buffer
		err := Run(ctx, append(args, "--home", home), &output, &errorOutput, "test")
		return output.String(), err
	}
	output, err := run("trust-state", peerID)
	if err != nil || !strings.Contains(output, "unknown") {
		t.Fatalf("legacy config granted trust: %s %v", output, err)
	}
	for _, testCase := range []struct{ command, state string }{{"trust", "trusted"}, {"block", "blocked"}, {"untrust", "unknown"}, {"trust", "trusted"}} {
		output, err := run(testCase.command, peerID)
		if err != nil || !strings.Contains(output, testCase.state) {
			t.Fatalf("%s: %s %v", testCase.command, output, err)
		}
		output, err = run("trust-state", "peer")
		if err != nil || !strings.Contains(output, testCase.state) {
			t.Fatalf("inspect: %s %v", output, err)
		}
	}
	for _, command := range []string{"trust", "trust-state", "peers", "status"} {
		output, err := run(command)
		if err != nil || !strings.Contains(output, "Peer trust (SQLite)") || !strings.Contains(output, peerID) {
			t.Fatalf("%s: %s %v", command, output, err)
		}
	}
	for _, args := range [][]string{{"trust", "127.0.0.1"}, {"block"}, {"trust", "missing"}, {"trust", peerID, "extra"}} {
		if _, err := run(args...); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	advertisedID, _ = messaging.NewID("node")
	if _, err := run("trust", peerID); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("accepted changed identity: %v", err)
	}
	if _, err := run("block", peerID); err != nil {
		t.Fatal(err)
	}
	anotherID, _ := messaging.NewID("node")
	snapshot := discovery.Snapshot{Peers: []discovery.Peer{{Node: protocol.Node{ID: peerID, Name: "duplicate"}}, {Node: protocol.Node{ID: anotherID, Name: "duplicate"}}}}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "peers.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("trust", "duplicate"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("accepted duplicate name: %v", err)
	}
	server.Close()
	if _, err := run("block", anotherID); err != nil {
		t.Fatalf("offline block: %v", err)
	}
}
