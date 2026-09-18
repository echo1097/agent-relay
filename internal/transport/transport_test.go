package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/config"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func transportFixture(t *testing.T, handler http.HandlerFunc) (*Service, messaging.Message, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	localID, _ := messaging.NewID("node")
	peerID, _ := messaging.NewID("node")
	cfg := config.Defaults()
	cfg.Network.Development = true
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.SetPeerTrust(context.Background(), storage.PeerTrust{NodeID: peerID, Name: "test", State: storage.Trusted, Address: host, Port: port, Development: true}); err != nil {
		t.Fatal(err)
	}
	service := New(store, localID, "", cfg, nil)
	t.Cleanup(service.Client.CloseIdleConnections)
	messageID, _ := messaging.NewID("msg")
	conversationID, _ := messaging.NewID("conv")
	senderID, _ := messaging.NewID("agent")
	recipientID, _ := messaging.NewID("agent")
	message := messaging.Message{ID: messageID, ConversationID: conversationID, SenderAgentID: senderID, RecipientAgentID: recipientID, Type: messaging.MessageType, Text: "hello", CreatedAt: time.Now().UTC()}
	return service, message, peerID
}

func TestRequestTimeout(t *testing.T) {
	service, message, peerID := transportFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	})
	service.Client.Timeout = 30 * time.Millisecond
	start := time.Now()
	retry, reason := service.Send(context.Background(), message, peerID)
	if !retry || reason != "PEER_UNREACHABLE" || time.Since(start) > time.Second {
		t.Fatalf("timeout: %v %s", retry, reason)
	}
}

func TestDeliveryFailures(t *testing.T) {
	for _, testCase := range []struct {
		status int
		retry  bool
	}{
		{403, false}, {404, false}, {409, false}, {410, false}, {400, false}, {408, true}, {429, true}, {500, true}, {503, true}, {302, false},
	} {
		t.Run(strconv.Itoa(testCase.status), func(t *testing.T) {
			service, message, peerID := transportFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Location", "http://127.0.0.1:1/must-not-follow")
				writer.WriteHeader(testCase.status)
			})
			retry, reason := service.Send(context.Background(), message, peerID)
			if retry != testCase.retry || reason == "" {
				t.Fatalf("failure: %v %s", retry, reason)
			}
		})
	}
}

func TestAcknowledgmentValidation(t *testing.T) {
	for _, kind := range []string{"valid", "wrong node", "wrong message", "wrong version", "duplicate version", "malformed", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			peerID := ""
			service, message, targetID := transportFixture(t, func(writer http.ResponseWriter, request *http.Request) {
				var incoming protocol.Message
				if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
					t.Error(err)
					return
				}
				writer.Header().Set(protocol.VersionHeader, "1")
				ack := protocol.DeliveryAck{ProtocolVersion: 1, NodeID: peerID, MessageID: incoming.ID, Status: "delivered", ReceivedAt: time.Now().UTC()}
				switch kind {
				case "wrong node":
					ack.NodeID = request.Header.Get(protocol.NodeHeader)
				case "wrong message":
					ack.MessageID, _ = messaging.NewID("msg")
				case "wrong version":
					writer.Header().Set(protocol.VersionHeader, "2")
				case "duplicate version":
					writer.Header().Add(protocol.VersionHeader, "2")
				case "malformed":
					writer.Write([]byte("invalid"))
					return
				case "oversized":
					writer.Write(make([]byte, 65537))
					return
				}
				if err := json.NewEncoder(writer).Encode(ack); err != nil {
					t.Error(err)
				}
			})
			peerID = targetID
			retry, reason := service.Send(context.Background(), message, peerID)
			if kind == "valid" {
				if retry || reason != "" {
					t.Fatalf("valid ack: %v %s", retry, reason)
				}
			} else if !retry || reason != "INVALID_ACKNOWLEDGMENT" {
				t.Fatalf("bad ack: %v %s", retry, reason)
			}
		})
	}
}

func TestRetryScheduleAndTrust(t *testing.T) {
	for index, delay := range []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute, 15 * time.Minute} {
		if actual := RetryDelay(index, 15*time.Minute); actual != delay {
			t.Fatalf("attempt %d: %v", index, actual)
		}
	}
}

func TestRejectionTextNeverEscapesPeerResponse(t *testing.T) {
	secret := "private message echoed by peer"
	service, message, peerID := transportFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		json.NewEncoder(writer).Encode(protocol.Error{ProtocolVersion: 1, Error: protocol.ErrorDetail{Code: protocol.NodeNotTrusted, Message: secret}})
	})
	retry, reason := service.Send(context.Background(), message, peerID)
	if retry || strings.Contains(reason, secret) || !strings.HasPrefix(reason, protocol.NodeNotTrusted) {
		t.Fatalf("unsafe reason: %s", reason)
	}
}

func TestTickSkipsMessageDeletedDuringSend(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	store, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := messaging.NewID("msg")
	if err != nil {
		t.Fatal(err)
	}
	conversationID, err := messaging.NewID("conv")
	if err != nil {
		t.Fatal(err)
	}
	senderID, err := messaging.NewID("agent")
	if err != nil {
		t.Fatal(err)
	}
	recipientID, err := messaging.NewID("agent")
	if err != nil {
		t.Fatal(err)
	}
	peerID, err := messaging.NewID("node")
	if err != nil {
		t.Fatal(err)
	}
	seenAt := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if _, err := store.RegisterAgent(ctx, agents.Agent{ID: senderID, NodeID: node.ID, DisplayName: "sender", Status: agents.Offline, RegisteredAt: seenAt, LastSeenAt: seenAt}, false); err != nil {
		t.Fatal(err)
	}
	message := messaging.Message{ID: messageID, ConversationID: conversationID, SenderAgentID: senderID, RecipientAgentID: recipientID, Type: messaging.MessageType, Text: "hello", CreatedAt: seenAt}
	queueAt := time.Now().UTC()
	deadline := queueAt.Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var incoming protocol.Message
		if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := store.RetainAgents(ctx, node.ID, time.Now().UTC())
		if err != nil || result.Deleted != 1 {
			http.Error(writer, "retention did not delete the queued session", http.StatusInternalServerError)
			return
		}
		writer.Header().Set(protocol.VersionHeader, "1")
		_ = json.NewEncoder(writer).Encode(protocol.DeliveryAck{ProtocolVersion: protocol.Version, NodeID: peerID, MessageID: incoming.ID, Status: "delivered", ReceivedAt: time.Now().UTC()})
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPeerTrust(ctx, storage.PeerTrust{NodeID: peerID, Name: "peer", State: storage.Trusted, Address: host, Port: port, Development: true}); err != nil {
		t.Fatal(err)
	}
	if _, inserted, err := store.QueueMessage(ctx, message, peerID, queueAt, deadline); err != nil || !inserted {
		t.Fatalf("queue: %v %v", inserted, err)
	}
	var logs bytes.Buffer
	cfg := config.Defaults()
	cfg.Network.Development = true
	service := New(store, node.ID, "", cfg, slog.New(slog.NewTextHandler(&logs, nil)))
	defer service.Client.CloseIdleConnections()
	if err := service.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if _, err := store.GetMessage(ctx, message.ID); err == nil {
		t.Fatal("retention resurrected deleted message")
	} else if !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("message lookup: %v", err)
	}
	if _, err := store.GetConversation(ctx, conversationID); err == nil {
		t.Fatal("retention resurrected deleted conversation")
	} else if !errors.Is(err, messaging.ErrNotFound) {
		t.Fatalf("conversation lookup: %v", err)
	}
	if strings.Contains(logs.String(), "message delivered") || strings.Contains(logs.String(), "delivery failed") {
		t.Fatalf("logged delivery for deleted message: %s", logs.String())
	}
}
