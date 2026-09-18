package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"agent-relay/migrations"
)

func messageFixture(t *testing.T) (*Store, messaging.Conversation, time.Time, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "messages.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	node, err := store.Node(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	_, err = store.RegisterAgent(ctx, agents.Agent{ID: "local", NodeID: node.ID, DisplayName: "local", Status: agents.Online, RegisteredAt: now, LastSeenAt: now}, false)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, messaging.Conversation{ID: "conv_test", LocalAgentID: "local", RemoteAgentID: "remote", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	return store, conversation, now, path
}

func incomingMessage(now time.Time, id string, messageType messaging.Type) messaging.Message {
	return messaging.Message{ID: id, ConversationID: "conv_test", SenderAgentID: "remote", RecipientAgentID: "local", Type: messageType, Text: "hello", CreatedAt: now}
}

func TestDurableInboxAndResponse(t *testing.T) {
	store, conversation, now, path := messageFixture(t)
	ctx := context.Background()
	question := incomingMessage(now, "msg_question", messaging.Question)
	saved, inserted, err := store.ReceiveMessage(ctx, question, now)
	if err != nil || !inserted || saved.Status != messaging.Delivered || saved.ReceivedAt == nil || saved.DeliveredAt == nil || !saved.ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("receive: %+v %v %v", saved, inserted, err)
	}
	processedAt, err := store.ProcessedMessage(ctx, question.ID)
	if err != nil || processedAt == nil || !processedAt.Equal(now) {
		t.Fatalf("processing: %v %v", processedAt, err)
	}
	inbox, err := store.ListInbox(ctx, "local", false)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox: %+v %v", inbox, err)
	}
	if inbox[0].ReadAt != nil {
		t.Fatal("listing marked message read")
	}
	read, err := store.MarkMessageRead(ctx, "local", question.ID, now.Add(time.Minute))
	if err != nil || read.Status != messaging.Pending {
		t.Fatalf("read: %+v %v", read, err)
	}
	again, err := store.MarkMessageRead(ctx, "local", question.ID, now.Add(2*time.Minute))
	if err != nil || !again.ReadAt.Equal(*read.ReadAt) {
		t.Fatalf("read retry: %+v %v", again, err)
	}
	inbox, err = store.ListInbox(ctx, "local", false)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("pending question hidden: %+v %v", inbox, err)
	}
	duplicate, inserted, err := store.ReceiveMessage(ctx, question, now.Add(3*time.Minute))
	if err != nil || inserted || duplicate.Status != messaging.Pending || !duplicate.ReceivedAt.Equal(now) {
		t.Fatalf("retry reset message: %+v %v %v", duplicate, inserted, err)
	}
	response := messaging.Message{ID: "msg_response", ConversationID: conversation.ID, SenderAgentID: "local", RecipientAgentID: "remote", Type: messaging.Response, Text: "answer", ReplyTo: question.ID, CreatedAt: now.Add(4 * time.Minute)}
	if _, _, err := store.SaveMessage(ctx, response, response.CreatedAt); err != nil {
		t.Fatal(err)
	}
	answered, err := store.GetMessage(ctx, question.ID)
	if err != nil || answered.Status != messaging.Answered || answered.AnsweredAt == nil {
		t.Fatalf("answer: %+v %v", answered, err)
	}
	inbox, err = store.ListInbox(ctx, "local", false)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("answered read question visible: %+v %v", inbox, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	inbox, err = reopened.ListInbox(ctx, "local", true)
	if err != nil || len(inbox) != 1 || inbox[0].ReadAt == nil || inbox[0].Status != messaging.Answered {
		t.Fatalf("restart: %+v %v", inbox, err)
	}
	history, err := reopened.ConversationHistory(ctx, conversation.ID)
	if err != nil || len(history) != 2 || history[0].ID != question.ID || history[1].ID != response.ID {
		t.Fatalf("history: %+v %v", history, err)
	}
	processedAt, err = reopened.ProcessedMessage(ctx, question.ID)
	if err != nil || processedAt == nil || !processedAt.Equal(now) {
		t.Fatalf("durable receipt: %v %v", processedAt, err)
	}
	if _, inserted, err := reopened.SaveMessage(ctx, response, now.Add(time.Hour)); err != nil || inserted {
		t.Fatalf("response retry: %v %v", inserted, err)
	}
}

func TestMessageOrderingAndReadIsolation(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	for _, id := range []string{"msg_z", "msg_a", "msg_m"} {
		message := incomingMessage(now.In(time.FixedZone("offset", -7*60*60)), id, messaging.MessageType)
		if _, _, err := store.ReceiveMessage(ctx, message, now); err != nil {
			t.Fatal(err)
		}
	}
	history, err := store.ConversationHistory(ctx, "conv_test")
	if err != nil || len(history) != 3 || history[0].ID != "msg_a" || history[1].ID != "msg_m" || history[2].ID != "msg_z" {
		t.Fatalf("order: %+v %v", history, err)
	}
	if _, err := store.MarkMessageRead(ctx, "other", "msg_a", now); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("other recipient read: %v", err)
	}
	if _, err := store.MarkMessageRead(ctx, "local", "msg_a", now); err != nil {
		t.Fatal(err)
	}
	inbox, err := store.ListInbox(ctx, "local", false)
	if err != nil || len(inbox) != 2 {
		t.Fatalf("unread: %+v %v", inbox, err)
	}
	inbox, err = store.ListInbox(ctx, "remote", true)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("wrong inbox: %+v %v", inbox, err)
	}
	older := incomingMessage(now.Add(-time.Hour), "msg_older", messaging.MessageType)
	if _, _, err := store.ReceiveMessage(ctx, older, now); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.GetConversation(ctx, "conv_test")
	if err != nil || !conversation.UpdatedAt.Equal(now) {
		t.Fatalf("updated time regressed: %+v %v", conversation, err)
	}
	conversations, err := store.ListConversations(ctx, "local")
	if err != nil || len(conversations) != 1 {
		t.Fatalf("list: %+v %v", conversations, err)
	}
}

func TestMessageConflictsAndValidation(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	message := incomingMessage(now, "msg_test", messaging.MessageType)
	if _, _, err := store.ReceiveMessage(ctx, message, now); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*messaging.Message){
		"text":      func(value *messaging.Message) { value.Text = "changed" },
		"timestamp": func(value *messaging.Message) { value.CreatedAt = now.Add(time.Second) },
		"type":      func(value *messaging.Message) { value.Type = messaging.Question },
	} {
		t.Run(name, func(t *testing.T) {
			changed := message
			change(&changed)
			if _, _, err := store.ReceiveMessage(ctx, changed, now); !errors.Is(err, messaging.ErrConflict) {
				t.Fatalf("conflict: %v", err)
			}
		})
	}
	for name, change := range map[string]func(*messaging.Message){
		"third participant":    func(value *messaging.Message) { value.SenderAgentID = "third" },
		"wrong local agent":    func(value *messaging.Message) { value.RecipientAgentID = "third" },
		"self":                 func(value *messaging.Message) { value.SenderAgentID = "local" },
		"blank text":           func(value *messaging.Message) { value.Text = " \n" },
		"unknown type":         func(value *messaging.Message) { value.Type = "unknown" },
		"unknown conversation": func(value *messaging.Message) { value.ConversationID = "absent" },
		"missing question":     func(value *messaging.Message) { value.Type = messaging.Response; value.ReplyTo = "absent" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := message
			changed.ID = "invalid"
			change(&changed)
			if _, inserted, err := store.ReceiveMessage(ctx, changed, now); err == nil || inserted {
				t.Fatalf("accepted: %v %v", inserted, err)
			}
			if processed, err := store.ProcessedMessage(ctx, changed.ID); err != nil || processed != nil {
				t.Fatalf("invalid receipt: %v %v", processed, err)
			}
		})
	}
	if _, _, err := store.SaveMessage(ctx, message, now); !errors.Is(err, messaging.ErrInvalid) {
		t.Fatalf("outgoing sender: %v", err)
	}
	if _, err := store.GetMessage(ctx, "absent"); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := store.ConversationHistory(ctx, "absent"); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestConcurrentMessageInsertion(t *testing.T) {
	store, _, now, path := messageFixture(t)
	ctx := context.Background()
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var workers sync.WaitGroup
	var insertedCount atomic.Int32
	for index := range 12 {
		workers.Go(func() {
			current := store
			if index%2 == 1 {
				current = other
			}
			_, inserted, err := current.ReceiveMessage(ctx, incomingMessage(now, "msg_once", messaging.Question), now)
			if err != nil {
				t.Error(err)
			}
			if inserted {
				insertedCount.Add(1)
			}
		})
	}
	workers.Wait()
	if insertedCount.Load() != 1 {
		t.Fatalf("inserted %d times", insertedCount.Load())
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM processed_messages").Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipts: %d %v", count, err)
	}
}

func TestMessageTransactionRollback(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	if _, err := store.db.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON processed_messages BEGIN SELECT RAISE(ABORT, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	message := incomingMessage(now.Add(time.Minute), "msg_fail", messaging.Question)
	if _, inserted, err := store.ReceiveMessage(ctx, message, now); err == nil || inserted {
		t.Fatalf("false success: %v %v", inserted, err)
	}
	if _, err := store.GetMessage(ctx, message.ID); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatal(err)
	}
	conversation, err := store.GetConversation(ctx, "conv_test")
	if err != nil || !conversation.UpdatedAt.Equal(now) {
		t.Fatalf("rollback: %+v %v", conversation, err)
	}
	if _, err := store.db.Exec("DROP TRIGGER fail_receipt"); err != nil {
		t.Fatal(err)
	}
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, inserted, err := store.ReceiveMessage(canceledCtx, message, now); err == nil || inserted {
		t.Fatalf("canceled success: %v %v", inserted, err)
	}
	if _, inserted, err := store.ReceiveMessage(ctx, message, now); err != nil || !inserted {
		t.Fatalf("retry: %v %v", inserted, err)
	}
}

func TestExpirationAndLifecycle(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	question := incomingMessage(now, "question", messaging.Question)
	deadline := now.Add(time.Hour)
	question.ExpiresAt = &deadline
	question.SenderAgentID, question.RecipientAgentID = "local", "remote"
	saved, _, err := store.SaveMessage(ctx, question, now)
	if err != nil || saved.Status != messaging.Created {
		t.Fatalf("save: %+v %v", saved, err)
	}
	for _, status := range []messaging.Status{messaging.Sending, messaging.Failed, messaging.Sending, messaging.PendingDelivery, messaging.Sending, messaging.Delivered, messaging.Pending} {
		saved, err = store.UpdateMessageStatus(ctx, question.ID, status, now)
		if err != nil || saved.Status != status {
			t.Fatalf("transition %s: %+v %v", status, saved, err)
		}
	}
	if saved.DeliveredAt == nil {
		t.Fatal("delivery timestamp missing")
	}
	if _, err := store.UpdateMessageStatus(ctx, question.ID, messaging.Created, now); !errors.Is(err, messaging.ErrTransition) {
		t.Fatal(err)
	}
	if _, err := store.UpdateMessageStatus(ctx, question.ID, messaging.Answered, now); !errors.Is(err, messaging.ErrTransition) {
		t.Fatal(err)
	}
	if count, err := store.ExpireRequests(ctx, deadline.Add(-time.Nanosecond)); err != nil || count != 0 {
		t.Fatalf("early expiration: %d %v", count, err)
	}
	if count, err := store.ExpireRequests(ctx, deadline); err != nil || count != 1 {
		t.Fatalf("expiration: %d %v", count, err)
	}
	if count, err := store.ExpireRequests(ctx, deadline); err != nil || count != 0 {
		t.Fatalf("repeat expiration: %d %v", count, err)
	}
	if _, err := store.UpdateMessageStatus(ctx, question.ID, messaging.Sending, deadline); !errors.Is(err, messaging.ErrTransition) {
		t.Fatal(err)
	}
	response := incomingMessage(deadline, "late_response", messaging.Response)
	response.ReplyTo = question.ID
	if _, _, err := store.ReceiveMessage(ctx, response, deadline); !errors.Is(err, messaging.ErrTransition) {
		t.Fatalf("late response: %v", err)
	}
	expired := incomingMessage(now, "expired_arrival", messaging.Question)
	expired.ExpiresAt = &deadline
	saved, _, err = store.ReceiveMessage(ctx, expired, deadline)
	if err != nil || saved.Status != messaging.Expired {
		t.Fatalf("expired arrival: %+v %v", saved, err)
	}
	if _, err := store.MarkMessageRead(ctx, "local", saved.ID, deadline); err != nil {
		t.Fatal(err)
	}
	inbox, err := store.ListInbox(ctx, "local", false)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("expired read item: %+v %v", inbox, err)
	}
}

func TestResponsesAreLinkedAndAtomic(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	question := incomingMessage(now, "question", messaging.Question)
	question.SenderAgentID, question.RecipientAgentID = "local", "remote"
	if _, _, err := store.SaveMessage(ctx, question, now); err != nil {
		t.Fatal(err)
	}
	response := incomingMessage(now.Add(time.Minute), "response", messaging.Response)
	response.ReplyTo = question.ID
	if _, err := store.db.Exec(`CREATE TRIGGER fail_receipt BEFORE INSERT ON processed_messages BEGIN SELECT RAISE(ABORT, 'forced failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReceiveMessage(ctx, response, now); err == nil {
		t.Fatal("expected rollback")
	}
	unchanged, err := store.GetMessage(ctx, question.ID)
	if err != nil || unchanged.Status != messaging.Created || unchanged.AnsweredAt != nil {
		t.Fatalf("question not rolled back: %+v %v", unchanged, err)
	}
	if _, err := store.db.Exec("DROP TRIGGER fail_receipt"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReceiveMessage(ctx, response, now); err != nil {
		t.Fatal(err)
	}
	response.ID = "second_response"
	if _, _, err := store.ReceiveMessage(ctx, response, now); !errors.Is(err, messaging.ErrTransition) {
		t.Fatalf("second answer: %v", err)
	}
	if count, err := store.ExpireRequests(ctx, now.Add(48*time.Hour)); err != nil || count != 0 {
		t.Fatalf("answered expired: %d %v", count, err)
	}
}

func TestConversationConstraintsAndSchemaUpgrade(t *testing.T) {
	store, conversation, now, _ := messageFixture(t)
	ctx := context.Background()
	for _, localID := range []string{"remote", "missing"} {
		invalid := conversation
		invalid.ID, invalid.LocalAgentID = "invalid", localID
		if _, err := store.CreateConversation(ctx, invalid); err == nil {
			t.Fatal("accepted invalid participant")
		}
	}
	for _, query := range []string{
		"UPDATE conversations SET remote_agent_id = 'third'",
		"INSERT INTO conversations VALUES ('bad', 'local', 'local', 'now', 'now')",
		"INSERT INTO messages (id, conversation_id, sender_agent_id, recipient_agent_id, type, text, status, created_at) VALUES ('bad', 'conv_test', 'third', 'local', 'message', 'text', 'delivered', 'now')",
	} {
		if _, err := store.db.Exec(query); err == nil {
			t.Fatalf("constraint bypass: %s", query)
		}
	}
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	if err := old.migrate(ctx, migrations.All()[:2]); err != nil {
		t.Fatal(err)
	}
	node, err := old.Node(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.ExecContext(ctx, "INSERT INTO agents (id, node_id, display_name, status, registered_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)", "old_agent", node.ID, "old", agents.Online, messageTime(now), messageTime(now)); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if _, err := upgraded.GetAgent(ctx, node.ID, "old_agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.CreateConversation(ctx, messaging.Conversation{ID: "new", LocalAgentID: "old_agent", RemoteAgentID: "remote", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
}
