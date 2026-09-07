package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
)

func TestDurableDeliveryQueue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	node, err := store.Node(ctx, "sender")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := registry.Register(ctx, agents.Registration{DisplayName: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	message := messaging.Message{ID: "message", ConversationID: "conversation", SenderAgentID: agent.ID, RecipientAgentID: "remote", Type: messaging.MessageType, Text: "hello", CreatedAt: now}
	if _, inserted, err := store.QueueMessage(ctx, message, "peer", now, now.Add(time.Hour)); err != nil || !inserted {
		t.Fatalf("queue: %v %v", inserted, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	due, err := store.DueDeliveries(ctx, now)
	if err != nil || len(due) != 1 {
		t.Fatalf("restart: %+v %v", due, err)
	}
	if _, inserted, err := store.QueueMessage(ctx, message, "peer", now, now.Add(time.Hour)); err != nil || inserted {
		t.Fatalf("duplicate: %v %v", inserted, err)
	}
	message.Text = "conflicting"
	if _, _, err := store.QueueMessage(ctx, message, "peer", now, now.Add(time.Hour)); !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if err := store.FinishDelivery(ctx, message.ID, messaging.PendingDelivery, now.Add(5*time.Second), now, "PEER_UNREACHABLE"); err != nil {
		t.Fatal(err)
	}
	due, err = store.DueDeliveries(ctx, now)
	if err != nil || len(due) != 0 {
		t.Fatalf("early retry: %+v %v", due, err)
	}
	due, err = store.DueDeliveries(ctx, now.Add(5*time.Second))
	if err != nil || len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("retry: %+v %v", due, err)
	}
	if err := store.ExpireDeliveries(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetMessage(ctx, message.ID)
	if err != nil || saved.Status != messaging.Failed {
		t.Fatalf("expiration: %+v %v", saved, err)
	}
}
