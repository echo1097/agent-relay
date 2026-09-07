package daemon

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-relay/internal/protocol"
	"agent-relay/internal/tailscale"
)

type discoveryTailnet struct {
	status tailscale.Status
	err    error
}

func (client discoveryTailnet) Status(context.Context) (tailscale.Status, error) {
	return client.status, client.err
}

func TestProductionDiscoveryRequiresTailnetSource(t *testing.T) {
	node := makeDeliveryNode(t, "127.0.0.1:0")
	node.service.Development = false
	server, err := newHTTPServer(node.registry, HTTPOptions{Node: protocol.PublicNode(node.node.ID, "test"), Version: "test", Delivery: node.service}, node.service.Logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name      string
		source    string
		connected bool
		err       error
		want      int
	}{
		{name: "local", source: "100.64.0.1:1000", connected: true, want: 200},
		{name: "unknown tailnet peer", source: "100.64.0.2:1000", connected: true, want: 200},
		{name: "unrecognized tailnet address", source: "100.64.0.3:1000", connected: true, want: 403},
		{name: "lan source", source: "192.168.1.2:1000", connected: true, want: 403},
		{name: "internet source", source: "203.0.113.2:1000", connected: true, want: 403},
		{name: "loopback", source: "127.0.0.1:1000", connected: true, want: 403},
		{name: "malformed", source: "invalid", connected: true, want: 403},
		{name: "disconnected", source: "100.64.0.2:1000", want: 403},
		{name: "status failure", source: "100.64.0.2:1000", connected: true, err: errors.New("status unavailable"), want: 403},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			node.service.Tailscale = discoveryTailnet{status: tailscale.Status{Connected: testCase.connected, IP: "100.64.0.1", Peers: []tailscale.Peer{{ID: "device-b", IP: "100.64.0.2"}}}, err: testCase.err}
			for _, path := range []string{"/v1/health", "/v1/hello", "/v1/agents"} {
				request := httptest.NewRequest("GET", path, nil)
				request.RemoteAddr = testCase.source
				request.Header.Set("X-Forwarded-For", "100.64.0.2")
				writer := httptest.NewRecorder()
				server.Handler.ServeHTTP(writer, request)
				if writer.Code != testCase.want {
					t.Fatalf("%s: %d %s", path, writer.Code, writer.Body)
				}
				if writer.Code != 200 && strings.Contains(writer.Body.String(), node.agent.ID) {
					t.Fatal("rejected discovery leaked agent metadata")
				}
			}
		})
	}
}
