package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"agent-relay/migrations"
)

func TestRetentionSettingsUpgradeAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	oldStore := &Store{db: db}
	if err := oldStore.migrate(ctx, migrations.All()[:6]); err != nil {
		t.Fatal(err)
	}
	if err := oldStore.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	policy, err := store.RetentionPolicy(ctx)
	if err != nil || policy != agents.DefaultRetentionPolicy() {
		t.Fatalf("upgraded defaults: %+v %v", policy, err)
	}
	archiveDays := 14
	policy, err = store.UpdateRetentionPolicy(ctx, &archiveDays, nil)
	if err != nil || policy.ArchiveAfterDays != 14 || policy.DeleteAfterDays != 30 {
		t.Fatalf("partial settings update: %+v %v", policy, err)
	}
	invalidPolicies := []agents.RetentionPolicy{
		{ArchiveAfterDays: 0, DeleteAfterDays: 30},
		{ArchiveAfterDays: -1, DeleteAfterDays: 30},
		{ArchiveAfterDays: 30, DeleteAfterDays: 30},
		{ArchiveAfterDays: 31, DeleteAfterDays: 30},
		{ArchiveAfterDays: 7, DeleteAfterDays: 0},
		{ArchiveAfterDays: 7, DeleteAfterDays: 106752},
	}
	for _, invalidPolicy := range invalidPolicies {
		if _, err := store.UpdateRetentionPolicy(ctx, &invalidPolicy.ArchiveAfterDays, &invalidPolicy.DeleteAfterDays); err == nil {
			t.Fatalf("accepted invalid settings: %+v", invalidPolicy)
		}
		unchanged, err := store.RetentionPolicy(ctx)
		if err != nil || unchanged != policy {
			t.Fatalf("invalid update changed settings: %+v %v", unchanged, err)
		}
	}
	deleteDays := 60
	policy, err = store.UpdateRetentionPolicy(ctx, nil, &deleteDays)
	if err != nil || policy.ArchiveAfterDays != 14 || policy.DeleteAfterDays != 60 {
		t.Fatalf("delete setting: %+v %v", policy, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.RetentionPolicy(ctx)
	if err != nil || saved != policy {
		t.Fatalf("settings did not persist: %+v %v", saved, err)
	}
}

func TestRetentionBoundariesAndNodeScope(t *testing.T) {
	store, _, now, _ := messageFixture(t)
	ctx := context.Background()
	node, err := store.ExistingNode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "INSERT INTO nodes (id, name, trust_state) VALUES ('another-node', 'other', 'unknown')"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id     string
		nodeID string
		status agents.Status
		age    time.Duration
	}{
		{"before-archive", node.ID, agents.Offline, 7*24*time.Hour - time.Nanosecond},
		{"at-archive", node.ID, agents.Offline, 7 * 24 * time.Hour},
		{"before-delete", node.ID, agents.Offline, 30*24*time.Hour - time.Nanosecond},
		{"at-delete", node.ID, agents.Offline, 30 * 24 * time.Hour},
		{"active-old", node.ID, agents.Busy, 40 * 24 * time.Hour},
		{"other-node", "another-node", agents.Offline, 40 * 24 * time.Hour},
	} {
		seenAt := now.Add(-item.age)
		_, err := store.RegisterAgent(ctx, agents.Agent{ID: item.id, NodeID: item.nodeID, DisplayName: item.id, Status: item.status, RegisteredAt: seenAt, LastSeenAt: seenAt}, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.RetainAgents(ctx, node.ID, now)
	if err != nil || result.Archived != 2 || result.Deleted != 1 {
		t.Fatalf("retention: %+v %v", result, err)
	}
	for _, id := range []string{"before-archive", "at-archive", "before-delete", "active-old"} {
		agent, err := store.GetAgent(ctx, node.ID, id)
		wantArchived := id == "at-archive" || id == "before-delete"
		if err != nil || agent.Archived != wantArchived {
			t.Fatalf("retained %s: %+v %v", id, agent, err)
		}
	}
	if _, err := store.GetAgent(ctx, node.ID, "at-delete"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("delete boundary: %v", err)
	}
	if agent, err := store.GetAgent(ctx, "another-node", "other-node"); err != nil || agent.Archived {
		t.Fatalf("other node changed: %+v %v", agent, err)
	}
	result, err = store.RetainAgents(ctx, node.ID, now)
	if err != nil || result != (agents.RetentionResult{}) {
		t.Fatalf("repeated sweep: %+v %v", result, err)
	}
	archiveDays := 14
	if _, err := store.UpdateRetentionPolicy(ctx, &archiveDays, nil); err != nil {
		t.Fatal(err)
	}
	result, err = store.RetainAgents(ctx, node.ID, now)
	if err != nil || result.Restored != 1 {
		t.Fatalf("updated settings were not applied: %+v %v", result, err)
	}
}

func TestRetentionDeletesOnlyExpiredSessionHistory(t *testing.T) {
	store, _, seenAt, _ := messageFixture(t)
	ctx := context.Background()
	node, err := store.ExistingNode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	question := incomingMessage(seenAt, "question", messaging.Question)
	if _, _, err := store.ReceiveRemote(ctx, question, "peer", seenAt); err != nil {
		t.Fatal(err)
	}
	response := messaging.Message{ID: "response", ConversationID: question.ConversationID, SenderAgentID: "local", RecipientAgentID: "remote", Type: messaging.Response, ReplyTo: question.ID, Text: "answer", CreatedAt: seenAt.Add(time.Minute)}
	if _, _, err := store.QueueMessage(ctx, response, "peer", response.CreatedAt, seenAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err = store.RegisterAgent(ctx, agents.Agent{ID: "retained", NodeID: node.ID, DisplayName: "retained", Status: agents.Offline, RegisteredAt: seenAt, LastSeenAt: seenAt.Add(24 * time.Hour)}, false)
	if err != nil {
		t.Fatal(err)
	}
	otherMessage := question
	otherMessage.ID = "retained-message"
	otherMessage.ConversationID = "retained-conversation"
	otherMessage.RecipientAgentID = "retained"
	if _, _, err := store.ReceiveRemote(ctx, otherMessage, "peer", seenAt); err != nil {
		t.Fatal(err)
	}
	status := agents.Offline
	if _, err := store.UpdateAgent(ctx, node.ID, "local", &status, nil, seenAt); err != nil {
		t.Fatal(err)
	}
	now := seenAt.Add(30 * 24 * time.Hour)
	result, err := store.RetainAgents(ctx, node.ID, now)
	if err != nil || result.Deleted != 1 || result.Archived != 1 {
		t.Fatalf("delete history: %+v %v", result, err)
	}
	for table, wantCount := range map[string]int{"agents": 1, "conversations": 1, "messages": 1, "processed_messages": 1, "conversation_peers": 1, "outbox": 0, "remote_agents": 1, "nodes": 1} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != wantCount {
			t.Fatalf("%s: got %d, want %d: %v", table, count, wantCount, err)
		}
	}
	if err := store.CheckIntegrity(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMessage(ctx, otherMessage.ID); err != nil {
		t.Fatalf("other session history lost: %v", err)
	}
	if _, _, err := store.ReceiveRemote(ctx, question, "peer", now); !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("late delivery to deleted session: %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("late delivery recreated history: %d %v", count, err)
	}
}

func TestRetentionRollsBackHistoryOnFailure(t *testing.T) {
	store, _, seenAt, _ := messageFixture(t)
	ctx := context.Background()
	node, err := store.ExistingNode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message := incomingMessage(seenAt, "message", messaging.MessageType)
	if _, _, err := store.ReceiveRemote(ctx, message, "peer", seenAt); err != nil {
		t.Fatal(err)
	}
	status := agents.Offline
	if _, err := store.UpdateAgent(ctx, node.ID, "local", &status, nil, seenAt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "CREATE TRIGGER reject_delete BEFORE DELETE ON agents BEGIN SELECT RAISE(ABORT, 'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	result, err := store.RetainAgents(ctx, node.ID, seenAt.Add(30*24*time.Hour))
	if err == nil || result != (agents.RetentionResult{}) {
		t.Fatalf("expected failed sweep: %+v %v", result, err)
	}
	for _, table := range []string{"agents", "conversations", "messages", "processed_messages", "conversation_peers"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("rollback lost %s: %d %v", table, count, err)
		}
	}
	if err := store.CheckIntegrity(ctx); err != nil {
		t.Fatal(err)
	}
}
