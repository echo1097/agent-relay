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
	for _, args := range [][]string{
		{"messages", "get"}, {"messages", "inbox"}, {"messages", "history"},
		{"messages", "send", "--from", senderID, "--to", recipientID, "--peer", peerID, "--type", "response", "--text", "not supported"},
		{"messages", "send", "--from", senderID, "--to", recipientID, "--peer", "unknown", "--text", "untrusted"},
	} {
		if _, err := run(args...); err == nil {
			t.Fatalf("accepted invalid command: %v", args)
		}
	}
}
