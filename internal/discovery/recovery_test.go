package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent-relay/internal/tailscale"
)

func TestCorruptCacheRebuilt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	if err := os.WriteFile(path, []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{status: tailscale.Status{Connected: true, IP: "100.64.0.1", Peers: []tailscale.Peer{{ID: "remote", IP: "100.64.0.2"}}}}
	manager := Manager{Client: client, Prober: &fakeProber{hello: validHello()}, Path: path, Port: 47832}
	snapshot, err := manager.Refresh(context.Background())
	if err != nil || len(snapshot.Peers) != 1 || snapshot.Peers[0].State != "online" {
		t.Fatalf("cache recovery: %+v %v", snapshot, err)
	}
	if _, err := Read(path); err != nil {
		t.Fatal(err)
	}
}
