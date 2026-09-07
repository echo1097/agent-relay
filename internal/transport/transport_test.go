package transport

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	cfg.TrustedPeers = []config.TrustedPeer{{NodeID: peerID, Address: host, Port: port}}
	service := New(nil, localID, "", cfg, nil)
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
	for _, kind := range []string{"valid", "wrong node", "wrong message", "wrong version", "malformed", "oversized"} {
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
	service := &Service{NodeID: "local", Peers: []config.TrustedPeer{{NodeID: "peer", Address: "100.64.0.2"}}}
	if !service.Trusted("peer", "100.64.0.2:1234") || service.Trusted("other", "100.64.0.2:1234") || service.Trusted("peer", "100.64.0.3:1234") || service.Trusted("peer", "invalid") {
		t.Fatal("trust must match both identity and source IP")
	}
}
