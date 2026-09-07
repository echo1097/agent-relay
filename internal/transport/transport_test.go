package transport

import (
	"agent-relay/internal/storage"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/messaging"
	"agent-relay/internal/protocol"
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
