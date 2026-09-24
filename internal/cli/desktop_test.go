package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"agent-relay/internal/storage"
)

func TestDesktopSnapshotAndHistory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "desktop-test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = store.RegisterAgent(ctx, agents.Agent{ID: "local", NodeID: node.ID, DisplayName: "Real local agent", Status: agents.Online, RegisteredAt: now, LastSeenAt: now}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateConversation(ctx, messaging.Conversation{ID: "conversation", LocalAgentID: "local", RemoteAgentID: "remote", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	message := messaging.Message{ID: "message", ConversationID: "conversation", SenderAgentID: "remote", RecipientAgentID: "local", Type: messaging.MessageType, Text: "Persisted conversation text", CreatedAt: now}
	if _, _, err := store.ReceiveMessage(ctx, message, now); err != nil {
		t.Fatal(err)
	}
	var output, errors bytes.Buffer
	if err := Run(ctx, []string{"desktop", "--home", home}, &output, &errors, "test"); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Agents []struct {
			DisplayName string `json:"display_name"`
		} `json:"agents"`
		Conversations []storage.DesktopConversation `json:"conversations"`
		Home          string                        `json:"home"`
	}
	if err := json.Unmarshal(output.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].DisplayName != "Real local agent" || len(snapshot.Conversations) != 1 || snapshot.Conversations[0].Preview != message.Text || snapshot.Home != home {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	output.Reset()
	if err := Run(ctx, []string{"desktop", "--home", home, "--conversation", "conversation"}, &output, &errors, "test"); err != nil {
		t.Fatal(err)
	}
	var history []messaging.Message
	if err := json.Unmarshal(output.Bytes(), &history); err != nil || len(history) != 1 || history[0].Text != message.Text || history[0].ReadAt != nil {
		t.Fatalf("history: %+v %v", history, err)
	}
	if err := Run(ctx, []string{"desktop", "--home", home, "--conversation", "absent"}, &output, &errors, "test"); err == nil {
		t.Fatal("missing conversation should fail")
	}
}
