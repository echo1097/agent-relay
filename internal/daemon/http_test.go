package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/protocol"
	"agent-relay/internal/storage"
)

func TestDaemonHTTPEndpoints(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(home, "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "local-test")
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: time.Minute, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	localAgent, err := registry.Register(ctx, agents.Registration{DisplayName: "codex-auth", Provider: "codex", Metadata: agents.Metadata{Task: "Checking auth", Repository: "github.com/example/backend", Cwd: "/Users/private", Files: []string{"/Users/private/secret.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_RELAY_TEST_SECRET", "secret-environment-value")
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	options := HTTPOptions{Address: "127.0.0.1:0", Node: protocol.PublicNode(node.ID, node.Name), Version: "integration-test", Ready: func(address net.Addr) { ready <- address }}
	go func() { done <- Run(ctx, filepath.Join(home, "daemon.lock"), logger, registry, options) }()
	stopped := false
	defer func() {
		cancel()
		if !stopped {
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(7 * time.Second):
				t.Error("daemon did not stop")
			}
		}
	}()
	var address net.Addr
	select {
	case address = <-ready:
	case err := <-done:
		stopped = true
		t.Fatalf("startup failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not start")
	}
	baseURL := "http://" + address.String()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/v1/health", "/v1/hello", "/v1/agents"} {
		response, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get(protocol.VersionHeader) != "1" || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("%s: %d, %s", path, response.StatusCode, data)
		}
		for _, forbidden := range []string{"/Users", "secret.go", "secret-environment-value", "cwd", "files", home} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s leaked %q: %s", path, forbidden, data)
			}
		}
		switch path {
		case "/v1/health":
			var health protocol.Health
			if err := json.Unmarshal(data, &health); err != nil {
				t.Fatal(err)
			}
			if err := health.Validate(); err != nil {
				t.Fatal(err)
			}
		case "/v1/hello":
			var hello protocol.Hello
			if err := json.Unmarshal(data, &hello); err != nil {
				t.Fatal(err)
			}
			if err := hello.Validate(); err != nil {
				t.Fatal(err)
			}
			if hello.Node.ID != node.ID || hello.Version != "integration-test" {
				t.Fatalf("wrong hello: %+v", hello)
			}
		case "/v1/agents":
			var list protocol.AgentList
			if err := json.Unmarshal(data, &list); err != nil {
				t.Fatal(err)
			}
			if err := list.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(list.Agents) != 1 || list.Agents[0].ID != localAgent.ID || list.Agents[0].Task != "Checking auth" {
				t.Fatalf("wrong list: %+v", list)
			}
		}
	}
	for _, testCase := range []struct {
		method, path, version, body, code string
		status                            int
	}{
		{"GET", "/v1/hello", "2", "", protocol.UnsupportedProtocol, 400},
		{"GET", "/v1/hello", "bad", "", protocol.InvalidRequest, 400},
		{"GET", "/v2/hello", "", "", protocol.UnsupportedProtocol, 400},
		{"POST", "/v1/agents", "1", "", protocol.MethodNotAllowed, 405},
		{"GET", "/v1/missing", "1", "", protocol.NotFound, 404},
		{"GET", "/v1/hello?secret=private", "1", "", protocol.InvalidRequest, 400},
		{"GET", "/v1/hello", "1", "private-body", protocol.InvalidRequest, 400},
	} {
		request, err := http.NewRequest(testCase.method, baseURL+testCase.path, strings.NewReader(testCase.body))
		if err != nil {
			t.Fatal(err)
		}
		if testCase.version != "" {
			request.Header.Set(protocol.VersionHeader, testCase.version)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result protocol.Error
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != testCase.status || result.Error.Code != testCase.code {
			t.Fatalf("%+v: %d, %+v", testCase, response.StatusCode, result)
		}
		if err := result.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("daemon did not shut down")
	}
	if response, err := client.Get(baseURL + "/v1/health"); err == nil {
		response.Body.Close()
		t.Fatal("listener remained open")
	}
	agent, err := store.GetAgent(context.Background(), node.ID, localAgent.ID)
	if err != nil || agent.Status != agents.Offline {
		t.Fatalf("shutdown presence: %+v, %v", agent, err)
	}
}

type listFunc func(context.Context) ([]agents.Agent, error)

func (list listFunc) List(ctx context.Context) ([]agents.Agent, error) { return list(ctx) }

func testHTTPOptions() HTTPOptions {
	return HTTPOptions{Node: protocol.PublicNode("node_019a84fc-1b72-7000-8000-000000000001", "test"), Version: "test"}
}

func TestHTTPFailureAndTimeout(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		list   listFunc
		status int
		code   string
	}{
		{"storage", func(context.Context) ([]agents.Agent, error) {
			return nil, errors.New("private database /Users/private secret=private")
		}, 500, protocol.InternalError},
		{"invalid response", func(context.Context) ([]agents.Agent, error) {
			return []agents.Agent{{ID: "invalid", NodeID: testHTTPOptions().Node.ID, Status: agents.Online}}, nil
		}, 500, protocol.InternalError},
		{"timeout", func(ctx context.Context) ([]agents.Agent, error) { <-ctx.Done(); return nil, ctx.Err() }, 504, protocol.RequestTimeout},
		{"panic", func(context.Context) ([]agents.Agent, error) { panic("private panic") }, 500, protocol.InternalError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			server, err := newHTTPServer(testCase.list, testHTTPOptions(), logger)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			request := httptest.NewRequest(http.MethodGet, "/v1/agents", nil).WithContext(ctx)
			writer := httptest.NewRecorder()
			server.Handler.ServeHTTP(writer, request)
			var result protocol.Error
			if err := json.Unmarshal(writer.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if writer.Code != testCase.status || result.Error.Code != testCase.code || strings.Contains(writer.Body.String(), "private") {
				t.Fatalf("response: %d %s", writer.Code, writer.Body)
			}
		})
	}
}

func TestHTTPFiltersOtherNodesAndDuplicateVersions(t *testing.T) {
	options := testHTTPOptions()
	server, err := newHTTPServer(listFunc(func(context.Context) ([]agents.Agent, error) {
		return []agents.Agent{{ID: "invalid", NodeID: "different-node"}}, nil
	}), options, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	writer := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/v1/agents", nil)
	server.Handler.ServeHTTP(writer, request)
	var list protocol.AgentList
	if err := json.Unmarshal(writer.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if writer.Code != 200 || list.Agents == nil || len(list.Agents) != 0 {
		t.Fatalf("nonlocal agent leaked: %s", writer.Body)
	}
	writer = httptest.NewRecorder()
	request.Header.Add(protocol.VersionHeader, "1")
	request.Header.Add(protocol.VersionHeader, "2")
	server.Handler.ServeHTTP(writer, request)
	if writer.Code != 400 {
		t.Fatalf("duplicate version accepted: %s", writer.Body)
	}
}

func TestHTTPGracefulShutdownWaitsForRequest(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRequest()
	server, err := newHTTPServer(listFunc(func(ctx context.Context) ([]agents.Agent, error) {
		close(entered)
		select {
		case <-release:
			return []agents.Agent{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}), testHTTPOptions(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	requestDone := make(chan error, 1)
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	go func() {
		response, err := client.Get("http://" + listener.Addr().String() + "/v1/agents")
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = errors.New("in-flight request did not succeed")
			}
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}
	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- server.Shutdown(ctx)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown abandoned active request: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseRequest()
	if err := <-requestDone; err != nil {
		t.Fatal(err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("server termination: %v", err)
	}
}

func TestServerTimeouts(t *testing.T) {
	server, err := newHTTPServer(listFunc(func(ctx context.Context) ([]agents.Agent, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second || time.Until(deadline) < 4*time.Second {
			t.Error("request has no five-second deadline")
		}
		return []agents.Agent{}, nil
	}), testHTTPOptions(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.WriteTimeout <= 0 || server.IdleTimeout <= 0 || server.MaxHeaderBytes <= 0 {
		t.Fatal("server limits are missing")
	}
	server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/agents", nil))
}
