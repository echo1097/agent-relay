package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/config"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/transport"
)

type deliveryNode struct {
	store    *storage.Store
	node     storage.Node
	agent    agents.Agent
	service  *transport.Service
	registry *agents.Registry
	home     string
	address  string
	cancel   context.CancelFunc
	done     chan error
}

func makeDeliveryNode(t *testing.T, address string) *deliveryNode {
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
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := registry.Register(ctx, agents.Registration{DisplayName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Network.Development = true
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	service := transport.New(store, node.ID, host, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(service.Client.CloseIdleConnections)
	return &deliveryNode{store: store, node: node, agent: agent, registry: registry, service: service, home: home, address: address}
}

func (node *deliveryNode) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	node.cancel = cancel
	node.done = make(chan error, 1)
	ready := make(chan net.Addr, 1)
	go func() {
		node.done <- Run(ctx, filepath.Join(node.home, "daemon.lock"), node.service.Logger, node.registry, HTTPOptions{Address: node.address, Node: protocol.PublicNode(node.node.ID, node.node.Name), Version: "test", Delivery: node.service, Ready: func(address net.Addr) { ready <- address }})
	}()
	select {
	case address := <-ready:
		node.address = address.String()
	case err := <-node.done:
		t.Fatalf("start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("start timed out")
	}
	t.Cleanup(func() { node.stop(t) })
}

func (node *deliveryNode) stop(t *testing.T) {
	t.Helper()
	if node.cancel == nil {
		return
	}
	node.cancel()
	select {
	case err := <-node.done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("stop timed out")
	}
	node.cancel = nil
}

func trustNode(t *testing.T, source, target *deliveryNode) {
	t.Helper()
	host, portText, err := net.SplitHostPort(target.address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.store.SetPeerTrust(context.Background(), storage.PeerTrust{NodeID: target.node.ID, Name: "test", State: storage.Trusted, Address: host, Port: port, Development: true}); err != nil {
		t.Fatal(err)
	}
}

func waitMessage(t *testing.T, store *storage.Store, id string, status messaging.Status) messaging.Message {
	t.Helper()
	deadline := time.Now().Add(9 * time.Second)
	for time.Now().Before(deadline) {
		message, err := store.GetMessage(context.Background(), id)
		if err == nil && message.Status == status {
			return message
		}
		time.Sleep(20 * time.Millisecond)
	}
	message, err := store.GetMessage(context.Background(), id)
	t.Fatalf("wanted %s: %+v %v", status, message, err)
	return message
}

func TestTwoDaemonDeliveryAndRecovery(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	nodeA.start(t)
	nodeB.stop(t)
	trustNode(t, nodeB, nodeA)
	nodeB.start(t)
	question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "Did refresh validation change?"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, question.ID, messaging.Delivered)
	received := waitMessage(t, nodeB.store, question.ID, messaging.Delivered)
	if received.ReceivedAt == nil {
		t.Fatal("missing durable receipt")
	}
	processedAt, err := nodeB.store.ProcessedMessage(ctx, question.ID)
	if err != nil || processedAt == nil {
		t.Fatalf("missing processed marker: %v", err)
	}
	for range 3 {
		if retry, reason := nodeA.service.Send(ctx, question, nodeB.node.ID); retry || reason != "" {
			t.Fatalf("duplicate: %v %s", retry, reason)
		}
	}
	inbox, err := nodeB.store.ListInbox(ctx, nodeB.agent.ID, false)
	if err != nil || len(inbox) != 1 || !inbox[0].ReceivedAt.Equal(*received.ReceivedAt) {
		t.Fatalf("duplicate inbox: %+v %v", inbox, err)
	}
	followup, err := nodeB.service.Queue(ctx, messaging.Message{SenderAgentID: nodeB.agent.ID, RecipientAgentID: nodeA.agent.ID, ConversationID: question.ConversationID, Type: messaging.MessageType, Text: "I am checking it."}, nodeA.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeB.store, followup.ID, messaging.Delivered)
	waitMessage(t, nodeA.store, followup.ID, messaging.Delivered)
	nodeB.stop(t)
	queued, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, ConversationID: question.ConversationID, Type: messaging.MessageType, Text: "Follow-up while offline"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, queued.ID, messaging.PendingDelivery)
	nodeA.stop(t)
	nodeB.start(t)
	nodeA.start(t)
	waitMessage(t, nodeA.store, queued.ID, messaging.Delivered)
	waitMessage(t, nodeB.store, queued.ID, messaging.Delivered)
	history, err := nodeB.store.ConversationHistory(ctx, question.ConversationID)
	if err != nil || len(history) != 3 {
		t.Fatalf("history: %+v %v", history, err)
	}
}

func TestRemoteDeliveryBoundary(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	trustNode(t, nodeB, nodeA)
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "question"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := protocol.WireMessage(question)
	post := func(message protocol.Message, nodeID, suffix string) (int, []byte) {
		t.Helper()
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest("POST", "http://"+nodeB.address+"/v1/messages", bytes.NewBuffer(append(data, suffix...)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(protocol.NodeHeader, nodeID)
		response, err := nodeA.service.Client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	for _, testCase := range []struct {
		name   string
		change func(*protocol.Message)
		nodeID string
		suffix string
		status int
	}{
		{name: "untrusted", nodeID: "unknown", status: 403},
		{name: "invalid ID", change: func(message *protocol.Message) { message.ID = "bad" }, status: 400},
		{name: "missing recipient", change: func(message *protocol.Message) {
			message.RecipientAgentID = nodeA.agent.ID
			message.SenderAgentID = nodeB.agent.ID
		}, status: 404},
		{name: "expired", change: func(message *protocol.Message) {
			message.CreatedAt = time.Now().Add(-2 * time.Hour)
			expiresAt := time.Now().Add(-time.Hour)
			message.ExpiresAt = &expiresAt
		}, status: 410},
		{name: "unsupported", change: func(message *protocol.Message) { message.ProtocolVersion = 2 }, status: 400},
		{name: "trailing JSON", suffix: "{}", status: 400},
		{name: "response without link", change: func(message *protocol.Message) { message.Type = messaging.Response }, status: 400},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			message := original
			if testCase.change != nil {
				testCase.change(&message)
			}
			nodeID := testCase.nodeID
			if nodeID == "" {
				nodeID = nodeA.node.ID
			}
			status, body := post(message, nodeID, testCase.suffix)
			if status != testCase.status {
				t.Fatalf("status %d: %s", status, body)
			}
		})
	}
	inbox, err := nodeB.store.ListInbox(ctx, nodeB.agent.ID, false)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("invalid writes: %+v %v", inbox, err)
	}
	status, firstAck := post(original, nodeA.node.ID, "")
	if status != 200 {
		t.Fatalf("receive: %d %s", status, firstAck)
	}
	if _, err := nodeB.store.ExpireRequests(ctx, question.ExpiresAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	status, duplicateAck := post(original, nodeA.node.ID, "")
	if status != 200 || !bytes.Equal(firstAck, duplicateAck) {
		t.Fatalf("ack changed: %d %s %s", status, firstAck, duplicateAck)
	}
	changed := original
	changed.Content.Text = "different"
	status, body := post(changed, nodeA.node.ID, "")
	if status != 409 {
		t.Fatalf("conflict: %d %s", status, body)
	}
	nodeB.stop(t)
	nodeB.store.Close()
	reopened, err := storage.Open(ctx, filepath.Join(nodeB.home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	saved, err := reopened.GetMessage(ctx, question.ID)
	if err != nil || saved.Text != question.Text || saved.Status != messaging.Expired {
		t.Fatalf("restart persistence: %+v %v", saved, err)
	}
}

type lostAckTransport struct {
	base http.RoundTripper
	lost bool
}

func (client *lostAckTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := client.base.RoundTrip(request)
	if err == nil && !client.lost && response.StatusCode == 200 {
		client.lost = true
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return response, err
}

func TestLostAcknowledgmentAndPermanentFailure(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	trustNode(t, nodeB, nodeA)
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	baseTransport := nodeA.service.Client.Transport
	nodeA.service.Client.Transport = &lostAckTransport{base: baseTransport}
	t.Cleanup(func() { baseTransport.(*http.Transport).CloseIdleConnections() })
	nodeA.start(t)
	message, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.MessageType, Text: "ack may disappear"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, message.ID, messaging.PendingDelivery)
	waitMessage(t, nodeB.store, message.ID, messaging.Delivered)
	waitMessage(t, nodeA.store, message.ID, messaging.Delivered)
	inbox, err := nodeB.store.ListInbox(ctx, nodeB.agent.ID, false)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("lost ack duplicated delivery: %+v %v", inbox, err)
	}
	missingID, err := messaging.NewID("agent")
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: missingID, Type: messaging.MessageType, Text: "invalid recipient"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, rejected.ID, messaging.Failed)
	due, err := nodeA.store.DueDeliveries(ctx, time.Now().Add(time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatalf("terminal failure retried: %+v %v", due, err)
	}
}

func TestDaemonExpiresUndeliveredQuestion(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	peerID, err := messaging.NewID("node")
	if err != nil {
		t.Fatal(err)
	}
	if err := nodeA.store.SetPeerTrust(ctx, storage.PeerTrust{NodeID: peerID, Name: "test", State: storage.Trusted, Address: "127.0.0.1", Port: 1, Development: true}); err != nil {
		t.Fatal(err)
	}
	nodeA.start(t)
	remoteID, err := messaging.NewID("agent")
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := time.Now().Add(500 * time.Millisecond)
	question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: remoteID, Type: messaging.Question, Text: "short question", ExpiresAt: &expiresAt}, peerID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, question.ID, messaging.Expired)
	due, err := nodeA.store.DueDeliveries(ctx, time.Now().Add(time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatalf("expired question retried: %+v %v", due, err)
	}
}

func TestReceiptNeverAcknowledgesFailedWrite(t *testing.T) {
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	trustNode(t, nodeB, nodeA)
	messageID, err := messaging.NewID("msg")
	if err != nil {
		t.Fatal(err)
	}
	conversationID, err := messaging.NewID("conv")
	if err != nil {
		t.Fatal(err)
	}
	message := protocol.WireMessage(messaging.Message{ID: messageID, ConversationID: conversationID, SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.MessageType, Text: "must commit first", CreatedAt: time.Now().UTC()})
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	server, err := newHTTPServer(nodeB.registry, HTTPOptions{Node: protocol.PublicNode(nodeB.node.ID, "test"), Version: "test", Delivery: nodeB.service}, nodeB.service.Logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"canceled", "closed storage"} {
		t.Run(mode, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(data))
			request.RemoteAddr = "127.0.0.1:12345"
			request.Header.Set(protocol.NodeHeader, nodeA.node.ID)
			request.Header.Set("Content-Type", "application/json")
			expected := 500
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				request = request.WithContext(ctx)
				expected = 504
			} else if err := nodeB.store.Close(); err != nil {
				t.Fatal(err)
			}
			writer := httptest.NewRecorder()
			server.Handler.ServeHTTP(writer, request)
			if writer.Code != expected {
				t.Fatalf("failed write response: %d %s", writer.Code, writer.Body)
			}
		})
	}
}
