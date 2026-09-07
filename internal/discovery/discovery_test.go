package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agent-relay/internal/protocol"
	"agent-relay/internal/tailscale"
	"github.com/google/uuid"
)

type fakeClient struct {
	status tailscale.Status
	err    error
}

func (client *fakeClient) Status(context.Context) (tailscale.Status, error) {
	return client.status, client.err
}

type fakeProber struct {
	hello protocol.Hello
	err   error
}

func (prober *fakeProber) Hello(context.Context, string, int) (protocol.Hello, error) {
	return prober.hello, prober.err
}

func validHello() protocol.Hello {
	id := uuid.Must(uuid.NewV7())
	return protocol.Hello{Protocol: protocol.Name, ProtocolVersion: protocol.Version, Node: protocol.Node{ID: "node_" + id.String(), Name: "peer"}, Version: "test"}
}

func TestProbeValidation(t *testing.T) {
	hello := validHello()
	data, _ := json.Marshal(hello)
	for _, testCase := range []struct {
		name, body, header string
		status             int
		want               error
	}{
		{name: "valid", body: string(data), status: 200},
		{name: "malformed", body: `{`, status: 200, want: ErrMalformed},
		{name: "missing fields", body: `{}`, status: 200, want: ErrMalformed},
		{name: "trailing JSON", body: string(data) + `{}`, status: 200, want: ErrMalformed},
		{name: "oversize", body: strings.Repeat(" ", 65537), status: 200, want: ErrMalformed},
		{name: "mismatch", body: strings.Replace(string(data), `"protocol_version":1`, `"protocol_version":2`, 1), status: 200, want: ErrIncompatible},
		{name: "header mismatch", body: string(data), header: "2", status: 200, want: ErrMalformed},
		{name: "rejected version", body: `{"error":{"code":"UNSUPPORTED_PROTOCOL"}}`, status: 400, want: ErrIncompatible},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/v1/hello" || request.Header.Get(protocol.VersionHeader) != "1" {
					t.Error("incorrect request")
				}
				if testCase.header != "" {
					writer.Header().Set(protocol.VersionHeader, testCase.header)
				}
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()
			host, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
			port, _ := strconv.Atoi(portText)
			_, err := NewProber().Hello(context.Background(), host, port)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("got %v want %v", err, testCase.want)
			}
		})
	}
}

func TestProbeCancellationAndRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Wait") == "yes" {
			<-request.Context().Done()
			return
		}
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer server.Close()
	host, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	if _, err := NewProber().Hello(context.Background(), host, port); err == nil || targetCalls.Load() != 0 {
		t.Fatal("redirect followed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewProber().Hello(ctx, host, port); err == nil {
		t.Fatal("cancellation ignored")
	}
	server.Close()
	if _, err := NewProber().Hello(context.Background(), host, port); err == nil {
		t.Fatal("closed peer accepted")
	}
}

func TestCacheTransitions(t *testing.T) {
	client := &fakeClient{status: tailscale.Status{Connected: true, IP: "100.64.0.1", Peers: []tailscale.Peer{{ID: "peer", IP: "100.64.0.2"}}}}
	prober := &fakeProber{hello: validHello()}
	manager := Manager{Client: client, Prober: prober, Path: filepath.Join(t.TempDir(), "peers.json"), Port: 47832}
	snapshot, err := manager.Refresh(context.Background())
	if err != nil || len(snapshot.Peers) != 1 || snapshot.Peers[0].State != "online" {
		t.Fatalf("%+v %v", snapshot, err)
	}
	seen := snapshot.Peers[0].LastSeen
	prober.err = errors.New("unreachable")
	for _, state := range []string{"suspect", "suspect", "unreachable"} {
		snapshot, err = manager.Refresh(context.Background())
		if err != nil || snapshot.Peers[0].State != state || !snapshot.Peers[0].LastSeen.Equal(seen) {
			t.Fatalf("%+v %v", snapshot, err)
		}
	}
	prober.err = ErrIncompatible
	snapshot, _ = manager.Refresh(context.Background())
	if snapshot.Peers[0].State != "incompatible" {
		t.Fatal(snapshot)
	}
	prober.err = nil
	snapshot, _ = manager.Refresh(context.Background())
	if snapshot.Peers[0].State != "online" || snapshot.Peers[0].Failures != 0 {
		t.Fatal(snapshot)
	}
	client.status.Peers = nil
	snapshot, _ = manager.Refresh(context.Background())
	if snapshot.Peers[0].State != "disappeared" {
		t.Fatal(snapshot)
	}
	restarted := Manager{Client: client, Prober: prober, Path: manager.Path}
	snapshot, err = restarted.Refresh(context.Background())
	if err != nil || len(snapshot.Peers) != 1 || snapshot.Peers[0].LastSeen.IsZero() {
		t.Fatalf("%+v %v", snapshot, err)
	}
	client.err = errors.New("offline")
	snapshot, _ = restarted.Refresh(context.Background())
	if snapshot.Peers[0].State != "unknown" || snapshot.Error == "" {
		t.Fatal(snapshot)
	}
}

type blockingProber struct{ calls chan struct{} }

func (prober blockingProber) Hello(ctx context.Context, _ string, _ int) (protocol.Hello, error) {
	prober.calls <- struct{}{}
	<-ctx.Done()
	return protocol.Hello{}, ctx.Err()
}

func TestLoopShutdown(t *testing.T) {
	calls := make(chan struct{}, 1)
	manager := Manager{Client: &fakeClient{status: tailscale.Status{Connected: true, Peers: []tailscale.Peer{{ID: "peer", IP: "100.64.0.2"}}}}, Prober: blockingProber{calls: calls}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { manager.Run(ctx, time.Millisecond); close(done) }()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("probe not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not stop")
	}
}

type countingProber struct{ calls chan struct{} }

func (prober countingProber) Hello(context.Context, string, int) (protocol.Hello, error) {
	prober.calls <- struct{}{}
	return validHello(), nil
}

func TestPeriodicRefresh(t *testing.T) {
	calls := make(chan struct{}, 10)
	manager := Manager{Client: &fakeClient{status: tailscale.Status{Connected: true, Peers: []tailscale.Peer{{ID: "peer", IP: "100.64.0.2"}}}}, Prober: countingProber{calls: calls}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); manager.Run(ctx, 5*time.Millisecond) }()
	defer func() { cancel(); <-done }()
	for range 2 {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("periodic refresh missing")
		}
	}
}

func TestProbeDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { <-request.Context().Done() }))
	defer server.Close()
	host, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NewProber().Hello(ctx, host, port); err == nil {
		t.Fatal("stalled peer accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout did not bound request")
	}
}
