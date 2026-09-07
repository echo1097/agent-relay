package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/config"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/relay"
	"agent-relay/internal/storage"
	"agent-relay/internal/transport"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func makeSession(t *testing.T) *relay.Session {
	t.Helper()
	ctx := context.Background()
	home := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	node, err := store.Node(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Second, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	service := transport.New(store, node.ID, "", cfg, logger)
	t.Cleanup(service.Client.CloseIdleConnections)
	return &relay.Session{Directory: &relay.Directory{Store: store, Registry: registry, Node: protocol.PublicNode(node.ID, node.Name), Path: filepath.Join(home, "peers.json"), Development: true}, Delivery: service}
}

func connectClient(t *testing.T, session *relay.Session) (*sdk.ClientSession, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	serverWire, clientWire := sdk.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, session, serverWire, 20*time.Millisecond, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	client := sdk.NewClient(&sdk.Implementation{Name: "test-coder", Version: "1"}, nil)
	connection, err := client.Connect(ctx, clientWire, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(func() {
			connection.Close()
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("MCP did not stop")
			}
		})
	}
	return connection, closeClient
}

func callTool(t *testing.T, client *sdk.ClientSession, name string, arguments any, wantError bool) map[string]any {
	t.Helper()
	result, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		if wantError {
			return nil
		}
		t.Fatal(err)
	}
	if result.IsError != wantError {
		t.Fatalf("%s: %+v", name, result)
	}
	if wantError {
		return nil
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func TestToolsAndSessionLifecycle(t *testing.T) {
	session := makeSession(t)
	client, closeClient := connectClient(t, session)
	defer closeClient()
	tools, err := client.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 8 {
		t.Fatalf("tools: %+v %v", tools, err)
	}
	names := make(map[string]bool)
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.InputSchema == nil || len(tool.Description) < 100 {
			t.Fatalf("weak tool contract: %+v", tool)
		}
	}
	for _, name := range []string{"list_agents", "get_agent", "ask_agent", "send_message", "check_inbox", "respond", "get_conversation", "update_status"} {
		if !names["relay."+name] {
			t.Fatal(name)
		}
	}
	data := callTool(t, client, "relay.update_status", map[string]any{"status": "busy", "task": "Investigating auth", "repository": "github.com/team/backend", "files": []string{"/private/secret.go"}}, false)
	agentID := data["agent_id"].(string)
	if agentID == "" || session.ID() != agentID {
		t.Fatal("missing automatic registration")
	}
	callTool(t, client, "relay.update_status", map[string]any{"branch": "fix/auth"}, false)
	agent, err := session.Directory.Registry.Get(context.Background(), agentID)
	if err != nil || agent.Task != "Investigating auth" || agent.Branch != "fix/auth" || agent.Status != agents.Busy {
		t.Fatalf("patch: %+v %v", agent, err)
	}
	data = callTool(t, client, "relay.get_agent", map[string]any{"agent_id": agentID}, false)
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "secret.go") || strings.Contains(string(raw), "cwd") {
		t.Fatal("private metadata exposed")
	}
	callTool(t, client, "relay.list_agents", map[string]any{"repository": "github.com/team/backend", "status": "busy"}, false)
	callTool(t, client, "relay.update_status", map[string]any{"status": "invalid", "task": "must not persist"}, true)
	agent, _ = session.Directory.Registry.Get(context.Background(), agentID)
	if agent.Task != "Investigating auth" {
		t.Fatal("invalid update partially persisted")
	}
	callTool(t, client, "relay.ask_agent", map[string]any{"agent_id": agentID}, true)
	callTool(t, client, "relay.check_inbox", map[string]any{"include_read": "yes"}, true)
	callTool(t, client, "relay.check_inbox", map[string]any{"agent_id": "another-session"}, true)
	callTool(t, client, "relay.update_status", map[string]any{"status": "offline"}, false)
	if err := session.Heartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	agent, _ = session.Directory.Registry.Get(context.Background(), agentID)
	if agent.Status != agents.Offline {
		t.Fatal("heartbeat revived explicitly offline agent")
	}
	callTool(t, client, "relay.update_status", map[string]any{"status": "idle", "task": ""}, false)
	closeClient()
	agent, err = session.Directory.Registry.Get(context.Background(), agentID)
	if err != nil || agent.Status != agents.Offline || agent.Task != "" {
		t.Fatalf("disconnect: %+v %v", agent, err)
	}
}

func TestSessionIsolation(t *testing.T) {
	session := makeSession(t)
	client, closeClient := connectClient(t, session)
	defer closeClient()
	callTool(t, client, "relay.update_status", map[string]any{}, false)
	other, err := session.Directory.Registry.Register(context.Background(), agents.Registration{DisplayName: "other"})
	if err != nil {
		t.Fatal(err)
	}
	remoteID, _ := messaging.NewID("agent")
	conversationID, _ := messaging.NewID("conv")
	conversation, err := session.Delivery.Store.CreateConversation(context.Background(), messaging.Conversation{ID: conversationID, LocalAgentID: other.ID, RemoteAgentID: remoteID, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	callTool(t, client, "relay.get_conversation", map[string]any{"conversation_id": conversation.ID}, true)
	callTool(t, client, "relay.ask_agent", map[string]any{"agent_id": remoteID, "conversation_id": conversation.ID, "question": "not mine"}, true)
	messageID, _ := messaging.NewID("msg")
	expiresAt := time.Now().Add(time.Hour)
	_, _, err = session.Delivery.Store.ReceiveMessage(context.Background(), messaging.Message{ID: messageID, ConversationID: conversation.ID, SenderAgentID: remoteID, RecipientAgentID: other.ID, Text: "private question", Type: messaging.Question, CreatedAt: time.Now(), ExpiresAt: &expiresAt}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	callTool(t, client, "relay.respond", map[string]any{"message_id": messageID, "response": "not mine"}, true)
	inbox := callTool(t, client, "relay.check_inbox", map[string]any{}, false)
	if len(inbox["messages"].([]any)) != 0 {
		t.Fatal("another session's inbox leaked")
	}
}
