package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func TestRemoteAgentCommands(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	nodeID, _ := messaging.NewID("node")
	agentID, _ := messaging.NewID("agent")
	remoteNode := protocol.Node{ID: nodeID, Name: "machine-b"}
	remoteAgent := protocol.Agent{ID: agentID, DisplayName: "claude-auth", Status: agents.Busy, Task: "Auth context"}
	archivedID, _ := messaging.NewID("agent")
	archivedAgent := protocol.Agent{ID: archivedID, DisplayName: "archived-auth", Status: agents.Offline, Archived: true, Task: "Old auth context"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(protocol.VersionHeader, "1")
		if request.URL.Path == "/v1/hello" {
			json.NewEncoder(writer).Encode(protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: remoteNode, Version: "test"})
		} else {
			remoteAgents := []protocol.Agent{remoteAgent}
			if request.URL.Query().Get("include_archived") == "true" {
				remoteAgents = append(remoteAgents, archivedAgent)
			}
			json.NewEncoder(writer).Encode(protocol.AgentList{ProtocolVersion: 1, Agents: remoteAgents})
		}
	}))
	defer server.Close()
	address, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[network]\ndevelopment = true\nbind_address = \"127.0.0.1\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Node(ctx, "machine-a"); err != nil {
		t.Fatal(err)
	}
	peer := storage.PeerTrust{NodeID: nodeID, Name: remoteNode.Name, State: storage.Trusted, Address: address, Port: port, Development: true}
	if err := store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		var output bytes.Buffer
		if err := runWithClient(ctx, append(args, "--home", home), &output, &bytes.Buffer{}, "test", testClient{}); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	for _, command := range []string{"agents", "status"} {
		output := run(command)
		if !strings.Contains(output, "claude-auth") || !strings.Contains(output, "machine-b") || !strings.Contains(output, "Auth context") {
			t.Fatalf("%s omitted remote agent: %s", command, output)
		}
	}
	if output := run("agents", "--local"); strings.Contains(output, "claude-auth") {
		t.Fatal("remote agent in local listing")
	}
	if output := run("agents"); strings.Contains(output, "archived-auth") {
		t.Fatal("archived remote agent in default directory")
	}
	if output := run("agents", "--all"); !strings.Contains(output, "archived-auth") || !strings.Contains(output, "offline (archived)") {
		t.Fatal("archived remote agent missing from all directory", output)
	}
	peer.State = storage.Blocked
	if err := store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if output := run("agents"); strings.Contains(output, "claude-auth") {
		t.Fatal("blocked agent in directory")
	}
	peer.State = storage.Trusted
	if err := store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if output := run("agents"); !strings.Contains(output, "Unavailable node: "+nodeID) {
		t.Fatal("unreachable peer not explained", output)
	}
}

func TestInboxAndConversationOverviews(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	run := func(args ...string) (string, error) {
		var output bytes.Buffer
		err := runWithClient(ctx, append(args, "--home", home), &output, &bytes.Buffer{}, "test", testClient{})
		return output.String(), err
	}
	for _, command := range []string{"inbox", "conversations"} {
		if output, err := run(command); err != nil || output == "" {
			t.Fatalf("empty %s: %s %v", command, output, err)
		}
	}
	first, err := run("agents", "register", "--name", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := run("agents", "register", "--name", "second")
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := strings.TrimSpace(first), strings.TrimSpace(second)
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	peerID, _ := messaging.NewID("node")
	remoteID, _ := messaging.NewID("agent")
	messageIDs := []string{}
	conversationIDs := []string{}
	now := time.Now().UTC()
	for index, localID := range []string{firstID, secondID} {
		messageID, _ := messaging.NewID("msg")
		conversationID, _ := messaging.NewID("conv")
		message := messaging.Message{ID: messageID, ConversationID: conversationID, SenderAgentID: remoteID, RecipientAgentID: localID, Type: messaging.Question, Text: "Question with control \x1b[2J", CreatedAt: now.Add(time.Duration(index) * time.Second)}
		if _, _, err := store.ReceiveRemote(ctx, message, peerID, now); err != nil {
			t.Fatal(err)
		}
		messageIDs = append(messageIDs, messageID)
		conversationIDs = append(conversationIDs, conversationID)
	}
	output, err := run("inbox")
	if err != nil || !strings.Contains(output, messageIDs[0]) || !strings.Contains(output, messageIDs[1]) || strings.Contains(output, "\x1b") {
		t.Fatalf("inbox: %s %v", output, err)
	}
	saved, err := store.GetMessage(ctx, messageIDs[0])
	if err != nil || saved.ReadAt != nil {
		t.Fatal("CLI read marked message read", err)
	}
	output, err = run("inbox", "--agent", firstID)
	if err != nil || !strings.Contains(output, messageIDs[0]) || strings.Contains(output, messageIDs[1]) {
		t.Fatalf("filtered inbox: %s %v", output, err)
	}
	if _, err := store.MarkMessageRead(ctx, firstID, messageIDs[0], now); err != nil {
		t.Fatal(err)
	}
	output, err = run("inbox", "--agent", firstID)
	if err != nil || !strings.Contains(output, messageIDs[0]) {
		t.Fatal("pending question hidden after read", output, err)
	}
	if _, err := store.ExpireRequests(ctx, now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	output, err = run("inbox", "--agent", firstID)
	if err != nil || strings.Contains(output, messageIDs[0]) {
		t.Fatal("expired read question remained in default inbox", output, err)
	}
	output, err = run("inbox", "--agent", firstID, "--include-read")
	if err != nil || !strings.Contains(output, messageIDs[0]) {
		t.Fatal("include-read missing history", output, err)
	}
	output, err = run("conversations", "--limit", "1")
	if err != nil || !strings.Contains(output, conversationIDs[1]) || strings.Contains(output, conversationIDs[0]) {
		t.Fatal("recent conversation order/limit", output, err)
	}
	output, err = run("conversations", "--agent", firstID)
	if err != nil || !strings.Contains(output, conversationIDs[0]) || strings.Contains(output, conversationIDs[1]) {
		t.Fatal("conversation agent filter", output, err)
	}
	for _, args := range [][]string{{"inbox", "--agent", "missing"}, {"conversations", "--agent", "missing"}, {"conversations", "--limit", "0"}, {"conversations", "--limit", "1001"}} {
		if _, err := run(args...); err == nil {
			t.Fatal("accepted invalid overview options", args)
		}
	}
}
