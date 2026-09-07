package storage

import (
	"context"
	"path/filepath"
	"testing"

	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
)

func TestTrustPersistenceAndDiscovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.Node(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	nodeID, _ := messaging.NewID("node")
	peer, err := store.PeerTrust(ctx, nodeID)
	if err != nil || peer.State != Unknown {
		t.Fatalf("default: %+v %v", peer, err)
	}
	peer = PeerTrust{NodeID: nodeID, Name: "peer", State: Trusted, TailscaleID: "device-1", Address: "100.64.0.2", Port: 47832}
	for _, state := range []TrustState{Trusted, Blocked, Unknown, Trusted} {
		peer.State = state
		if err := store.SetPeerTrust(ctx, peer); err != nil {
			t.Fatal(err)
		}
		if err := store.ObservePeer(ctx, protocol.Node{ID: nodeID, Name: "impostor"}); err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := store.PeerTrust(ctx, nodeID)
		if err != nil || saved != peer {
			t.Fatalf("persisted: %+v %v", saved, err)
		}
	}
	defer store.Close()
	peer.NodeID = local.ID
	if err := store.SetPeerTrust(ctx, peer); err == nil {
		t.Fatal("changed local trust")
	}
	peer.NodeID = nodeID
	peer.TailscaleID = ""
	if err := store.SetPeerTrust(ctx, peer); err == nil {
		t.Fatal("trusted by IP alone")
	}
	peer.State = "invalid"
	if err := store.SetPeerTrust(ctx, peer); err == nil {
		t.Fatal("accepted invalid state")
	}
	peers, err := store.ListPeerTrust(ctx)
	if err != nil || len(peers) != 1 {
		t.Fatalf("peers: %+v %v", peers, err)
	}
}
