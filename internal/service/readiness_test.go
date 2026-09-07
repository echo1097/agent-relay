package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"agent-relay/internal/daemon"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func TestReadinessRequiresLocalIdentity(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.Node(ctx, "expected-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var responseMutex sync.Mutex
	response := protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.PublicNode(node.ID, node.Name), Version: "test"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		responseMutex.Lock()
		defer responseMutex.Unlock()
		writer.Header().Set(protocol.VersionHeader, "1")
		json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	configText := "[network]\ndevelopment = true\nbind_address = \"127.0.0.1\"\nport = " + port + "\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeState := daemon.Runtime{Address: server.Listener.Addr().String(), NodeID: node.ID, Version: "test"}
	writeRuntime := func() {
		t.Helper()
		data, err := json.Marshal(runtimeState)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "daemon.lock"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRuntime()
	if err := daemonReady(ctx, home); err != nil {
		t.Fatal("valid listener rejected", err)
	}
	responseMutex.Lock()
	response.Version = "old"
	responseMutex.Unlock()
	if err := daemonReady(ctx, home); err == nil {
		t.Fatal("mismatched listener version accepted")
	}
	responseMutex.Lock()
	response.Version = "test"
	response.Node.ID = "node_019a0000-0000-7000-8000-000000000001"
	responseMutex.Unlock()
	if err := daemonReady(ctx, home); err == nil {
		t.Fatal("different Relay node accepted as ready")
	}
	runtimeState.NodeID = response.Node.ID
	writeRuntime()
	if err := daemonReady(ctx, home); err == nil {
		t.Fatal("runtime record for different node accepted")
	}
}
