package transport

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
	"agent-relay/internal/tailscale"
)

type trustTailnet struct {
	status tailscale.Status
	err    error
}

func (client *trustTailnet) Status(context.Context) (tailscale.Status, error) {
	return client.status, client.err
}

func TestStableDeviceAuthorization(t *testing.T) {
	service, message, peerID := transportFixture(t, func(http.ResponseWriter, *http.Request) {})
	ctx := context.Background()
	peer := storage.PeerTrust{NodeID: peerID, Name: "peer", State: storage.Trusted, TailscaleID: "stable-device", Address: "100.64.0.2", Port: 47832}
	if err := service.Store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	service.Development = false
	client := &trustTailnet{status: tailscale.Status{Connected: true, Peers: []tailscale.Peer{{ID: "stable-device", IP: "100.64.0.3"}, {ID: "replacement", IP: "100.64.0.2"}}}}
	service.Tailscale = client
	for _, testCase := range []struct {
		name, nodeID, address string
		allowed               bool
	}{
		{"changed address", peerID, "100.64.0.3:1234", true},
		{"reassigned address", peerID, "100.64.0.2:1234", false},
		{"untrusted claim", service.NodeID, "100.64.0.3:1234", false},
		{"missing identity", "", "100.64.0.3:1234", false},
		{"malformed source", peerID, "invalid", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := service.Authorize(ctx, testCase.nodeID, testCase.address)
			if (err == nil) != testCase.allowed {
				t.Fatalf("authorization: %v", err)
			}
		})
	}
	for _, state := range []storage.TrustState{storage.Unknown, storage.Blocked} {
		peer.State = state
		if err := service.Store.SetPeerTrust(ctx, peer); err != nil {
			t.Fatal(err)
		}
		if err := service.Authorize(ctx, peerID, "100.64.0.3:1234"); err == nil || !strings.Contains(err.Error(), string(state)) {
			t.Fatalf("state %s: %v", state, err)
		}
		if _, err := service.Queue(ctx, message, peerID); err == nil {
			t.Fatal("queued unauthorized message")
		}
		retry, reason := service.Send(ctx, message, peerID)
		if retry || !strings.Contains(reason, protocol.NodeNotTrusted) {
			t.Fatalf("retry after revocation: %v %s", retry, reason)
		}
	}
	peer.State = storage.Trusted
	if err := service.Store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	client.err = errors.New("unavailable")
	if err := service.Authorize(ctx, peerID, "100.64.0.3:1234"); err == nil {
		t.Fatal("allowed unavailable Tailscale")
	}
	client.err = nil
	client.status.Connected = false
	if err := service.Authorize(ctx, peerID, "100.64.0.3:1234"); err == nil {
		t.Fatal("allowed disconnected Tailscale")
	}
	service.Development = true
	if err := service.Authorize(ctx, peerID, "127.0.0.1:1234"); err == nil {
		t.Fatal("reused production trust in development")
	}
}

func TestRemoteTrustRejectionPreserved(t *testing.T) {
	service, message, peerID := transportFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(403)
		writer.Write([]byte(`{"protocol_version":1,"error":{"code":"NODE_NOT_TRUSTED","message":"This node is blocked."}}`))
	})
	retry, reason := service.Send(context.Background(), message, peerID)
	if retry || !strings.HasPrefix(reason, "NODE_NOT_TRUSTED:") || strings.Contains(reason, "This node is blocked.") {
		t.Fatalf("rejection: %v %s", retry, reason)
	}
}

type trustHello struct{ nodeID string }

func (prober trustHello) Hello(context.Context, string, int) (protocol.Hello, error) {
	return protocol.Hello{Protocol: protocol.Name, ProtocolVersion: 1, Node: protocol.Node{ID: prober.nodeID, Name: "peer"}, Version: "test"}, nil
}

func TestChangedDestinationReceivesNoMessage(t *testing.T) {
	requests := 0
	service, message, peerID := transportFixture(t, func(http.ResponseWriter, *http.Request) { requests++ })
	ctx := context.Background()
	peer := storage.PeerTrust{NodeID: peerID, Name: "peer", State: storage.Trusted, TailscaleID: "stable-device", Address: "100.64.0.2", Port: 47832}
	if err := service.Store.SetPeerTrust(ctx, peer); err != nil {
		t.Fatal(err)
	}
	service.Development = false
	service.Tailscale = &trustTailnet{status: tailscale.Status{Connected: true, Peers: []tailscale.Peer{{ID: "stable-device", IP: "100.64.0.2"}}}}
	service.Prober = trustHello{nodeID: service.NodeID}
	retry, reason := service.Send(ctx, message, peerID)
	if retry || !strings.Contains(reason, protocol.NodeNotTrusted) || requests != 0 {
		t.Fatalf("destination mismatch: %v %s requests=%d", retry, reason, requests)
	}
}
