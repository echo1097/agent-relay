package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/messaging"
	"agent-relay/internal/storage"
)

func TestMessageCommands(t *testing.T) {
	home := t.TempDir()
	run := func(args ...string) (string, error) {
		t.Helper()
		var output, errorOutput bytes.Buffer
		args = append(args, "--home", home)
		err := Run(context.Background(), args, &output, &errorOutput, "test")
		return output.String(), err
	}
	output, err := run("agents", "register", "--name", "sender")
	if err != nil {
		t.Fatal(err)
	}
	senderID := strings.TrimSpace(output)
	peerID, err := messaging.NewID("node")
	if err != nil {
		t.Fatal(err)
	}
	recipientID, err := messaging.NewID("agent")
	if err != nil {
		t.Fatal(err)
	}
	cfg := "[network]\ndevelopment = true\nbind_address = \"127.0.0.1\"\n[messages]\nrequest_expiration_hours = 2\n[[trusted_peers]]\nnode_id = \"" + peerID + "\"\naddress = \"127.0.0.1\"\nport = 47932\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	trustStore, err := storage.Open(context.Background(), filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := trustStore.SetPeerTrust(context.Background(), storage.PeerTrust{NodeID: peerID, Name: "test", State: storage.Trusted, Address: "127.0.0.1", Port: 47932, Development: true}); err != nil {
		t.Fatal(err)
	}
	trustStore.Close()
	output, err = run("messages", "send", "--from", senderID, "--to", recipientID, "--peer", peerID, "--type", "question", "--text", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var message messaging.Message
	if err := json.Unmarshal([]byte(output), &message); err != nil {
		t.Fatal(err)
	}
	if message.Status != messaging.Created || message.ExpiresAt == nil || message.ExpiresAt.Sub(message.CreatedAt) != 2*time.Hour {
		t.Fatalf("queue: %+v", message)
	}
	for _, args := range [][]string{{"messages", "get", "--id", message.ID}, {"messages", "history", "--conversation", message.ConversationID}} {
		output, err := run(args...)
		if err != nil || !strings.Contains(output, message.ID) {
			t.Fatalf("read %v: %s %v", args, output, err)
		}
	}
	output, err = run("messages", "inbox", "--agent", senderID)
	if err != nil || strings.TrimSpace(output) != "[]" {
		t.Fatalf("outgoing leaked into inbox: %s %v", output, err)
	}
	store, err := storage.Open(context.Background(), filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	incoming := message
	incoming.ID, err = messaging.NewID("msg")
	if err != nil {
		t.Fatal(err)
	}
	incoming.SenderAgentID, incoming.RecipientAgentID = recipientID, senderID
	if _, _, err := store.ReceiveRemote(context.Background(), incoming, peerID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	output, err = run("messages", "respond", "--from", senderID, "--id", incoming.ID, "--text", "yes")
	if err != nil {
		t.Fatal(err)
	}
	var answer messaging.Message
	if err := json.Unmarshal([]byte(output), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.ReplyTo != incoming.ID || answer.ConversationID != message.ConversationID || answer.RecipientAgentID != recipientID || answer.Type != messaging.Response {
		t.Fatalf("automatic return routing: %+v", answer)
	}
	output, err = run("messages", "get", "--id", incoming.ID)
	if err != nil || !strings.Contains(output, `"Status":"answered"`) {
		t.Fatalf("answered question: %s %v", output, err)
	}
	if _, err := run("messages", "respond", "--from", senderID, "--id", incoming.ID, "--text", "duplicate"); err == nil {
		t.Fatal("accepted duplicate CLI response")
	}
	for _, args := range [][]string{
		{"messages", "respond"}, {"messages", "respond", "--from", senderID, "--id", "missing", "--text", "no"}, {"messages", "get"}, {"messages", "inbox"}, {"messages", "history"},
		{"messages", "send", "--from", senderID, "--to", recipientID, "--peer", peerID, "--type", "response", "--text", "not supported"},
		{"messages", "send", "--from", senderID, "--to", recipientID, "--peer", "unknown", "--text", "untrusted"},
	} {
		if _, err := run(args...); err == nil {
			t.Fatalf("accepted invalid command: %v", args)
		}
	}
}
