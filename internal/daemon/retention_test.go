package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func TestDaemonRetainsSessionsAtStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for id, age := range map[string]time.Duration{"archived": 8 * 24 * time.Hour, "deleted": 31 * 24 * time.Hour, "recent": 24 * time.Hour} {
		seenAt := now.Add(-age)
		_, err := store.RegisterAgent(ctx, agents.Agent{ID: id, NodeID: node.ID, DisplayName: id, Status: agents.Online, RegisteredAt: seenAt, LastSeenAt: seenAt}, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: 30 * time.Second, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	options := HTTPOptions{Address: "127.0.0.1:0", Node: protocol.PublicNode(node.ID, node.Name), Version: "test"}
	go func() { done <- Run(ctx, filepath.Join(home, "daemon.lock"), logger, registry, options) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("daemon failed to stop")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		archived, archiveErr := store.GetAgent(ctx, node.ID, "archived")
		_, deleteErr := store.GetAgent(ctx, node.ID, "deleted")
		if archiveErr == nil && archived.Archived && errors.Is(deleteErr, agents.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not apply retention: %+v %v %v", archived, archiveErr, deleteErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	recent, err := store.GetAgent(ctx, node.ID, "recent")
	if err != nil || recent.Archived || recent.Status != agents.Offline {
		t.Fatalf("recent session changed: %+v %v", recent, err)
	}
}
