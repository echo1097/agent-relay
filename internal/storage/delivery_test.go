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

func TestRemoteReceiptRollbackAndOwnership(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	message := incomingMessage(now, "remote-message", messaging.MessageType)
	message.ConversationID = "new-conversation"
	invalid := message
	invalid.RecipientAgentID = "missing"
	if _, _, err := store.ReceiveRemote(ctx, invalid, "peer", now); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("recipient: %v", err)
	}
	if _, err := store.GetConversation(ctx, message.ConversationID); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("orphan conversation: %v", err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := store.ReceiveRemote(canceledCtx, message, "peer", now); err == nil {
		t.Fatal("canceled write accepted")
	}
	if _, _, err := store.ReceiveRemote(ctx, message, "peer", now); err != nil {
		t.Fatal(err)
	}
	message.ID = "hijack"
	message.ConversationID = "hijack-conversation"
	if _, _, err := store.ReceiveRemote(ctx, message, "different-peer", now); !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("remote agent ownership: %v", err)
	}
	if _, err := store.GetConversation(ctx, message.ConversationID); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("hijack orphan: %v", err)
	}
}

func TestQueuedResponseIsAtomicAndExpires(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	question := incomingMessage(now, "question", messaging.Question)
	if _, _, err := store.ReceiveRemote(ctx, question, "peer", now); err != nil {
		t.Fatal(err)
	}
	response := messaging.Message{ID: "response", ConversationID: question.ConversationID, SenderAgentID: "local", RecipientAgentID: "remote", Type: messaging.Response, ReplyTo: question.ID, Text: "yes", CreatedAt: now.Add(time.Minute)}
	deadline := now.Add(time.Hour)
	if _, inserted, err := store.QueueMessage(ctx, response, "peer", response.CreatedAt, deadline); err != nil || !inserted {
		t.Fatalf("queue response: %v %v", inserted, err)
	}
	saved, err := store.GetMessage(ctx, question.ID)
	if err != nil || saved.Status != messaging.Answered || saved.AnsweredAt == nil || !saved.AnsweredAt.Equal(response.CreatedAt) {
		t.Fatalf("question completion: %+v %v", saved, err)
	}
	if _, inserted, err := store.QueueMessage(ctx, response, "peer", response.CreatedAt, deadline); err != nil || inserted {
		t.Fatalf("exact retry: %v %v", inserted, err)
	}
	response.ID = "duplicate-response"
	if _, _, err := store.QueueMessage(ctx, response, "peer", response.CreatedAt, deadline); !errors.Is(err, messaging.ErrTransition) {
		t.Fatalf("duplicate answer: %v", err)
	}
	due, err := store.DueDeliveries(ctx, response.CreatedAt)
	if err != nil || len(due) != 1 || due[0].MessageID != "response" {
		t.Fatalf("atomic outbox: %+v %v", due, err)
	}
	if err := store.ExpireDeliveries(ctx, deadline); err != nil {
		t.Fatal(err)
	}
	saved, err = store.GetMessage(ctx, "response")
	if err != nil || saved.Status != messaging.Failed {
		t.Fatalf("response deadline: %+v %v", saved, err)
	}
}

func TestFinishDeliveryReportsMissingOutbox(t *testing.T) {
	store, conversation, now, _ := messageFixture(t)
	ctx := context.Background()
	message := messaging.Message{ID: "outgoing", ConversationID: conversation.ID, SenderAgentID: conversation.LocalAgentID, RecipientAgentID: conversation.RemoteAgentID, Type: messaging.MessageType, Text: "hello", CreatedAt: now}
	if _, _, err := store.SaveMessage(ctx, message, now); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishDelivery(ctx, message.ID, messaging.Delivered, now, now, ""); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("missing outbox: %v", err)
	}
	saved, err := store.GetMessage(ctx, message.ID)
	if err != nil || saved.Status != messaging.Created {
		t.Fatalf("message changed after missing outbox: %+v %v", saved, err)
	}
}
