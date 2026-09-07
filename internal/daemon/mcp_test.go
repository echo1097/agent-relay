package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/discovery"
	relayMCP "agent-relay/internal/mcp"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/relay"
	"agent-relay/internal/storage"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectNodeMCP(t *testing.T, node *deliveryNode) *sdk.ClientSession {
	t.Helper()
	session := &relay.Session{Directory: &relay.Directory{Registry: node.registry, Store: node.store, Node: protocol.PublicNode(node.node.ID, node.node.Name), Path: filepath.Join(node.home, "peers.json"), Development: true, Prober: discovery.NewProber()}, Delivery: node.service, Registration: agents.Registration{ID: node.agent.ID, DisplayName: "mcp-test"}}
	serverWire, clientWire := sdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relayMCP.Run(ctx, session, serverWire, time.Second, "test", node.service.Logger) }()
	client, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, clientWire, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("MCP shutdown timeout")
		}
	})
	return client
}

func callNodeTool(t *testing.T, client *sdk.ClientSession, name string, arguments any) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := client.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil || result.IsError {
		t.Fatalf("%s: %+v %v", name, result, err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func TestMCPEndToEndConversation(t *testing.T) {
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	nodeA.start(t)
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	trustNode(t, nodeB, nodeA)
	clientA := connectNodeMCP(t, nodeA)
	clientB := connectNodeMCP(t, nodeB)
	var listing struct {
		Agents []relay.Agent `json:"agents"`
	}
	if err := json.Unmarshal(callNodeTool(t, clientA, "relay.list_agents", map[string]any{}), &listing); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, agent := range listing.Agents {
		if agent.ID == nodeB.agent.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("MCP did not discover remote agent")
	}
	conversationID := ""
	messageIDs := []string{}
	texts := []string{"Did you change refresh token validation?", "Yes, I changed the validation logic.", "Was session_id added?", "Yes."}
	for turn := range 2 {
		var question struct {
			MessageID      string `json:"message_id"`
			ConversationID string `json:"conversation_id"`
		}
		raw := callNodeTool(t, clientA, "relay.ask_agent", map[string]any{"agent_id": nodeB.agent.ID, "question": texts[turn*2], "conversation_id": conversationID})
		if err := json.Unmarshal(raw, &question); err != nil {
			t.Fatal(err)
		}
		conversationID = question.ConversationID
		waitMessage(t, nodeB.store, question.MessageID, messaging.Delivered)
		var inbox struct {
			Messages []struct {
				MessageID string `json:"message_id"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(callNodeTool(t, clientB, "relay.check_inbox", map[string]any{"mark_read": true}), &inbox); err != nil {
			t.Fatal(err)
		}
		if len(inbox.Messages) != 1 || inbox.Messages[0].MessageID != question.MessageID {
			t.Fatalf("inbox: %+v", inbox)
		}
		var answer struct {
			MessageID string `json:"message_id"`
		}
		if err := json.Unmarshal(callNodeTool(t, clientB, "relay.respond", map[string]any{"message_id": question.MessageID, "response": texts[turn*2+1]}), &answer); err != nil {
			t.Fatal(err)
		}
		waitMessage(t, nodeA.store, answer.MessageID, messaging.Delivered)
		for _, node := range []*deliveryNode{nodeA, nodeB} {
			waitMessage(t, node.store, question.MessageID, messaging.Answered)
		}
		messageIDs = append(messageIDs, question.MessageID, answer.MessageID)
	}
	for _, client := range []*sdk.ClientSession{clientA, clientB} {
		var history struct {
			Messages []struct {
				MessageID  string     `json:"message_id"`
				Text       string     `json:"text"`
				ReplyTo    string     `json:"reply_to"`
				AnsweredAt *time.Time `json:"answered_at"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(callNodeTool(t, client, "relay.get_conversation", map[string]any{"conversation_id": conversationID}), &history); err != nil {
			t.Fatal(err)
		}
		if len(history.Messages) != 4 {
			t.Fatalf("history: %+v", history)
		}
		for index, message := range history.Messages {
			if message.MessageID != messageIDs[index] || message.Text != texts[index] {
				t.Fatal("incorrect ordering or content")
			}
			if index%2 == 1 && message.ReplyTo != messageIDs[index-1] {
				t.Fatal("missing answer link")
			}
			if index%2 == 0 && message.AnsweredAt == nil {
				t.Fatal("missing answered timestamp")
			}
		}
	}
	nodeB.stop(t)
	var queued struct {
		MessageID string `json:"message_id"`
	}
	raw := callNodeTool(t, clientA, "relay.send_message", map[string]any{"agent_id": nodeB.agent.ID, "conversation_id": conversationID, "text": "Thanks for the context."})
	if err := json.Unmarshal(raw, &queued); err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, queued.MessageID, messaging.PendingDelivery)
	nodeB.start(t)
	waitMessage(t, nodeB.store, queued.MessageID, messaging.Delivered)
	peer, err := nodeA.store.PeerTrust(context.Background(), nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer.State = storage.Blocked
	if err := nodeA.store.SetPeerTrust(context.Background(), peer); err != nil {
		t.Fatal(err)
	}
	result, err := clientA.CallTool(context.Background(), &sdk.CallToolParams{Name: "relay.ask_agent", Arguments: map[string]any{"agent_id": nodeB.agent.ID, "conversation_id": conversationID, "question": "blocked"}})
	if err != nil || !result.IsError {
		t.Fatalf("trust bypass: %+v %v", result, err)
	}
	for _, node := range []*deliveryNode{nodeA, nodeB} {
		reopened, err := storage.Open(context.Background(), filepath.Join(node.home, "relay.db"))
		if err != nil {
			t.Fatal(err)
		}
		history, err := reopened.ConversationHistory(context.Background(), conversationID)
		reopened.Close()
		if err != nil || len(history) != 5 {
			t.Fatalf("durable history: %d %v", len(history), err)
		}
	}
}
