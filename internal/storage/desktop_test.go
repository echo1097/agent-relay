package storage

import (
	"context"
	"testing"
	"time"

	"agent-relay/internal/messaging"
)

func TestDesktopSummariesPreserveInboxAndExpireQuestions(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	question := incomingMessage(now, "desktop-question", messaging.Question)
	question.Text = "<script>alert('incoming')</script>"
	if _, _, err := store.ReceiveMessage(ctx, question, now); err != nil {
		t.Fatal(err)
	}
	conversations, err := store.DesktopConversations(ctx, now)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("summaries: %+v %v", conversations, err)
	}
	item := conversations[0]
	if !item.Unread || !item.NeedsReply || item.Title != question.Text || item.MessageCount != 1 {
		t.Fatalf("unexpected summary: %+v", item)
	}
	if _, err := store.ConversationHistory(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetMessage(ctx, question.ID)
	if err != nil || saved.ReadAt != nil || saved.Status != messaging.Delivered {
		t.Fatalf("desktop read changed the agent inbox: %+v %v", saved, err)
	}
	conversations, err = store.DesktopConversations(ctx, now.Add(25*time.Hour))
	if err != nil || conversations[0].NeedsReply || conversations[0].Status != "expired" {
		t.Fatalf("expired question still needs reply: %+v %v", conversations, err)
	}
}

func TestDesktopOutgoingQuestionIsNotIncomingUnread(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	question := messaging.Message{ID: "outgoing", ConversationID: "conv_test", SenderAgentID: "local", RecipientAgentID: "remote", Type: messaging.Question, Text: "A question", CreatedAt: now}
	if _, _, err := store.SaveMessage(ctx, question, now); err != nil {
		t.Fatal(err)
	}
	items, err := store.DesktopConversations(ctx, now)
	if err != nil || len(items) != 1 || items[0].Unread || items[0].NeedsReply {
		t.Fatalf("outgoing question appeared in incoming inbox: %+v %v", items, err)
	}
}
