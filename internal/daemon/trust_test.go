package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func TestIncomingTrustStatesAndLiveRevocation(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	server, err := newHTTPServer(nodeB.registry, HTTPOptions{Node: protocol.PublicNode(nodeB.node.ID, "test"), Version: "test", Delivery: nodeB.service}, nodeB.service.Logger)
	if err != nil {
		t.Fatal(err)
	}
	messageID, _ := messaging.NewID("msg")
	conversationID, _ := messaging.NewID("conv")
	message := messaging.Message{ID: messageID, ConversationID: conversationID, SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "trust test", CreatedAt: time.Now().UTC()}
	expiresAt := message.CreatedAt.Add(time.Hour)
	message.ExpiresAt = &expiresAt
	data, err := json.Marshal(protocol.WireMessage(message))
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, headers []string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest("POST", path, body)
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Content-Type", "application/json")
		for _, header := range headers {
			request.Header.Add(protocol.NodeHeader, header)
		}
		request.Header.Set("X-Forwarded-For", "100.64.0.2")
		writer := httptest.NewRecorder()
		server.Handler.ServeHTTP(writer, request)
		return writer
	}
	for _, state := range []storage.TrustState{storage.Unknown, storage.Trusted, storage.Blocked, storage.Unknown, storage.Trusted} {
		peer := storage.PeerTrust{NodeID: nodeA.node.ID, Name: "sender", State: state, Address: "127.0.0.1", Port: 47832, Development: true}
		if err := nodeB.store.SetPeerTrust(ctx, peer); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/v1/messages", "/v1/responses"} {
			writer := post(path, []string{nodeA.node.ID}, bytes.NewReader(data))
			if state != storage.Trusted {
				if writer.Code != 403 || !strings.Contains(writer.Body.String(), protocol.NodeNotTrusted) || !strings.Contains(writer.Body.String(), string(state)) {
					t.Fatalf("%s %s: %d %s", state, path, writer.Code, writer.Body)
				}
			} else if path == "/v1/messages" && writer.Code != 200 {
				t.Fatalf("trusted: %d %s", writer.Code, writer.Body)
			}
		}
	}
	for _, headers := range [][]string{nil, {"unknown"}, {nodeA.node.ID, nodeA.node.ID}, {nodeB.node.ID}} {
		writer := post("/v1/messages", headers, bytes.NewReader(data))
		if writer.Code != 403 {
			t.Fatalf("bad identity: %d %s", writer.Code, writer.Body)
		}
	}
	request := httptest.NewRequest("GET", "/v1/hello", nil)
	writer := httptest.NewRecorder()
	server.Handler.ServeHTTP(writer, request)
	if writer.Code != 200 {
		t.Fatal("unknown nodes cannot discover")
	}
	inbox, err := nodeB.store.ListInbox(ctx, nodeB.agent.ID, false)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("duplicate or rejected write: %+v %v", inbox, err)
	}
	peer, err := nodeB.store.PeerTrust(ctx, nodeA.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	reader := &trustRevokingReader{Reader: bytes.NewReader(data), revoke: func() {
		peer.State = storage.Blocked
		if err := nodeB.store.SetPeerTrust(ctx, peer); err != nil {
			t.Fatal(err)
		}
	}}
	writer = post("/v1/messages", []string{nodeA.node.ID}, reader)
	if writer.Code != 403 {
		t.Fatalf("revoked during read: %d %s", writer.Code, writer.Body)
	}
}

type trustRevokingReader struct {
	io.Reader
	revoke func()
}

func (reader *trustRevokingReader) Read(data []byte) (int, error) {
	if reader.revoke != nil {
		reader.revoke()
		reader.revoke = nil
	}
	return reader.Reader.Read(data)
}

func TestQueuedMessageFailsAfterBlock(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	trustNode(t, nodeA, nodeB)
	message, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.MessageType, Text: "must not send"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := nodeA.store.PeerTrust(ctx, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer.State = storage.Blocked
	if err := nodeA.store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if err := nodeA.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err := nodeA.store.GetMessage(ctx, message.ID)
	if err != nil || saved.Status != messaging.Failed {
		t.Fatalf("revoked queue: %+v %v", saved, err)
	}
	delivery, err := nodeA.store.GetDelivery(ctx, message.ID)
	if err != nil || delivery == nil || !strings.Contains(delivery.LastError, "NODE_NOT_TRUSTED") || !strings.Contains(delivery.LastError, "blocked") {
		t.Fatalf("missing failure reason: %+v %v", delivery, err)
	}
	due, err := nodeA.store.DueDeliveries(ctx, time.Now().Add(time.Hour))
	if err != nil || len(due) != 0 {
		t.Fatalf("revoked message retried: %+v %v", due, err)
	}
}

func TestDndRejectsNewRequests(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	if err := nodeB.store.SetPeerTrust(ctx, storage.PeerTrust{NodeID: nodeA.node.ID, Name: "sender", State: storage.Trusted, Address: "127.0.0.1", Port: 47832, Development: true}); err != nil {
		t.Fatal(err)
	}
	server, err := newHTTPServer(nodeB.registry, HTTPOptions{Node: protocol.PublicNode(nodeB.node.ID, "test"), Version: "test", Delivery: nodeB.service}, nodeB.service.Logger)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := nodeB.store.ToggleDnd(ctx); err != nil || !enabled {
		t.Fatalf("toggle: %v %v", enabled, err)
	}
	for _, messageType := range []messaging.Type{messaging.MessageType, messaging.Question} {
		messageId, _ := messaging.NewID("msg")
		conversationId, _ := messaging.NewID("conv")
		now := time.Now().UTC()
		expires := now.Add(time.Hour)
		message := messaging.Message{ID: messageId, ConversationID: conversationId, SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messageType, Text: "hello", CreatedAt: now}
		if messageType == messaging.Question {
			message.ExpiresAt = &expires
		}
		data, _ := json.Marshal(protocol.WireMessage(message))
		post := func() *httptest.ResponseRecorder {
			request := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(data))
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(protocol.NodeHeader, nodeA.node.ID)
			writer := httptest.NewRecorder()
			server.Handler.ServeHTTP(writer, request)
			return writer
		}
		writer := post()
		if writer.Code != 403 || !strings.Contains(writer.Body.String(), protocol.DoNotDisturb) {
			t.Fatalf("DND: %d %s", writer.Code, writer.Body)
		}
		if _, err := nodeB.store.GetMessage(ctx, messageId); err == nil {
			t.Fatal("DND stored incoming message")
		}
		if _, err := nodeB.store.ToggleDnd(ctx); err != nil {
			t.Fatal(err)
		}
		writer = post()
		if writer.Code != 200 {
			t.Fatalf("DND off: %d %s", writer.Code, writer.Body)
		}
		if _, err := nodeB.store.ToggleDnd(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDndAllowsOutgoingAndResponses(t *testing.T) {
	ctx := context.Background()
	nodeA := makeDeliveryNode(t, "127.0.0.1:0")
	nodeB := makeDeliveryNode(t, "127.0.0.1:0")
	nodeA.start(t)
	nodeB.start(t)
	trustNode(t, nodeA, nodeB)
	trustNode(t, nodeB, nodeA)
	if _, err := nodeA.store.ToggleDnd(ctx); err != nil {
		t.Fatal(err)
	}
	question, err := nodeA.service.Queue(ctx, messaging.Message{SenderAgentID: nodeA.agent.ID, RecipientAgentID: nodeB.agent.ID, Type: messaging.Question, Text: "outgoing during DND"}, nodeB.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeB.store, question.ID, messaging.Delivered)
	response, err := nodeB.service.Respond(ctx, nodeB.agent.ID, question.ID, "reply during DND")
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeA.store, response.ID, messaging.Delivered)
	message, err := nodeB.service.Queue(ctx, messaging.Message{SenderAgentID: nodeB.agent.ID, RecipientAgentID: nodeA.agent.ID, Type: messaging.MessageType, Text: "new request"}, nodeA.node.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitMessage(t, nodeB.store, message.ID, messaging.Failed)
	delivery, err := nodeB.store.GetDelivery(ctx, message.ID)
	if err != nil || delivery == nil || !strings.Contains(delivery.LastError, protocol.DoNotDisturb) {
		t.Fatalf("DND delivery: %+v %v", delivery, err)
	}
}
