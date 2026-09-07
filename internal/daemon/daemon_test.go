package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func TestLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: 30 * time.Second, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := registry.Register(context.Background(), agents.Registration{DisplayName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	backgroundStopped := make(chan struct{})
	options := HTTPOptions{Address: "127.0.0.1:0", Node: protocol.PublicNode(node.ID, node.Name), Version: "test", Background: func(runCtx context.Context) { <-runCtx.Done(); close(backgroundStopped) }}
	go func() { done <- Run(ctx, path, logger, registry, options) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		running, err := Running(path)
		if err != nil {
			t.Fatal(err)
		}
		if running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := Run(ctx, path, logger, registry, options); err == nil {
		t.Fatal("second daemon was accepted")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop")
	}
	select {
	case <-backgroundStopped:
	default:
		t.Fatal("daemon returned before background loop stopped")
	}
	running, err := Running(path)
	if err != nil || running {
		t.Fatalf("lock not released: %v, %v", running, err)
	}
	agent, err = registry.Get(context.Background(), agent.ID)
	if err != nil || agent.Status != agents.Offline {
		t.Fatalf("shutdown presence: %+v, %v", agent, err)
	}
}

func TestDaemonExpiresAgentsWithoutReads(t *testing.T) {
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: 20 * time.Millisecond, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := registry.Register(ctx, agents.Registration{DisplayName: "expires"})
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
		currentAgent, err := store.GetAgent(ctx, node.ID, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if currentAgent.Status == agents.Offline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not persist timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
