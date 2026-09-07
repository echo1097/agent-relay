package agents_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/storage"
)

type fixture struct {
	store    *storage.Store
	registry *agents.Registry
	nodeID   string
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	node, err := store.Node(context.Background(), "test-node")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &fixture{store: store, nodeID: node.ID, now: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)}
	fixture.registry, err = agents.New(store, node.ID, agents.Options{OfflineAfter: 30 * time.Second, Now: func() time.Time { return fixture.now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestRegistrationAndUpdates(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	input := agents.Registration{DisplayName: "codex-auth", Provider: "codex", Metadata: agents.Metadata{Task: "auth bug", Project: "backend", Repository: "example/backend", Branch: "fix/auth", Cwd: "/local/backend", Files: []string{"auth.go"}}}
	agent, err := fixture.registry.Register(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(agent.ID, "agent_") || agent.NodeID != fixture.nodeID || agent.Status != agents.Online || !agent.RegisteredAt.Equal(fixture.now) || !reflect.DeepEqual(agent.Metadata, input.Metadata) {
		t.Fatalf("registration: %+v", agent)
	}
	secondAgent, err := fixture.registry.Register(ctx, input)
	if err != nil || secondAgent.ID == agent.ID {
		t.Fatalf("same names must represent separate sessions: %+v, %v", secondAgent, err)
	}
	fixture.now = fixture.now.Add(time.Second)
	input.ID = agent.ID
	input.Task = "new task"
	resumedAgent, err := fixture.registry.Register(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if resumedAgent.ID != agent.ID || !resumedAgent.RegisteredAt.Equal(agent.RegisteredAt) || !resumedAgent.LastSeenAt.Equal(fixture.now) || resumedAgent.Task != input.Task {
		t.Fatalf("resume: %+v", resumedAgent)
	}
	input.ID = "agent_unknown"
	if _, err := fixture.registry.Register(ctx, input); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("unknown resume: %v", err)
	}
	localAgents, err := fixture.registry.List(ctx)
	if err != nil || len(localAgents) != 2 {
		t.Fatalf("duplicate resume created a row: %d, %v", len(localAgents), err)
	}
	updatedAgent, err := fixture.registry.UpdateMetadata(ctx, agent.ID, agents.Metadata{Task: "replacement"})
	if err != nil || updatedAgent.Task != "replacement" || updatedAgent.Repository != "" || updatedAgent.RegisteredAt != agent.RegisteredAt {
		t.Fatalf("metadata replacement: %+v, %v", updatedAgent, err)
	}
}

func TestHeartbeatAndOfflineBoundary(t *testing.T) {
	for _, status := range []agents.Status{agents.Online, agents.Busy, agents.Idle} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newFixture(t)
			ctx := context.Background()
			agent, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "agent"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.registry.UpdateStatus(ctx, agent.ID, status); err != nil {
				t.Fatal(err)
			}
			fixture.now = fixture.now.Add(10 * time.Second)
			heartbeatAgent, err := fixture.registry.Heartbeat(ctx, agent.ID)
			if err != nil || heartbeatAgent.Status != status || !heartbeatAgent.LastSeenAt.Equal(fixture.now) {
				t.Fatalf("heartbeat: %+v, %v", heartbeatAgent, err)
			}
			fixture.now = fixture.now.Add(30*time.Second - time.Nanosecond)
			currentAgent, err := fixture.registry.Get(ctx, agent.ID)
			if err != nil || currentAgent.Status != status {
				t.Fatalf("expired early: %+v, %v", currentAgent, err)
			}
			fixture.now = fixture.now.Add(time.Nanosecond)
			currentAgent, err = fixture.registry.Get(ctx, agent.ID)
			if err != nil || currentAgent.Status != agents.Offline || !currentAgent.LastSeenAt.Equal(heartbeatAgent.LastSeenAt) {
				t.Fatalf("timeout: %+v, %v", currentAgent, err)
			}
			if count, err := fixture.registry.Expire(ctx); err != nil || count != 0 {
				t.Fatalf("duplicate timeout: %d, %v", count, err)
			}
			currentAgent, err = fixture.registry.Heartbeat(ctx, agent.ID)
			if err != nil || currentAgent.Status != agents.Online {
				t.Fatalf("revival: %+v, %v", currentAgent, err)
			}
			if err := fixture.registry.Disconnect(ctx, agent.ID); err != nil {
				t.Fatal(err)
			}
			currentAgent, err = fixture.registry.Get(ctx, agent.ID)
			if err != nil || currentAgent.Status != agents.Offline {
				t.Fatalf("disconnect: %+v, %v", currentAgent, err)
			}
		})
	}
}

func TestHeartbeatAfterUnobservedTimeout(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	agent, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.registry.UpdateStatus(ctx, agent.ID, agents.Busy); err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(time.Minute)
	agent, err = fixture.registry.Heartbeat(ctx, agent.ID)
	if err != nil || agent.Status != agents.Online {
		t.Fatalf("stale busy heartbeat: %+v, %v", agent, err)
	}
}

func TestLookupValidationAndIsolation(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	if _, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "  "}); err == nil {
		t.Fatal("accepted empty name")
	}
	agent, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.registry.UpdateStatus(ctx, agent.ID, "invalid"); err == nil {
		t.Fatal("accepted invalid status")
	}
	for _, operation := range []func() error{
		func() error { _, err := fixture.registry.Get(ctx, "unknown"); return err },
		func() error { _, err := fixture.registry.Heartbeat(ctx, "unknown"); return err },
		func() error { _, err := fixture.registry.UpdateStatus(ctx, "unknown", agents.Idle); return err },
		func() error { _, err := fixture.registry.UpdateMetadata(ctx, "unknown", agents.Metadata{}); return err },
		func() error { return fixture.registry.Disconnect(ctx, "unknown") },
	} {
		if err := operation(); !errors.Is(err, agents.ErrNotFound) {
			t.Fatalf("missing agent: %v", err)
		}
	}
	otherRegistry, err := agents.New(fixture.store, "another-node", agents.Options{OfflineAfter: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherRegistry.Get(ctx, agent.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("read another node: %v", err)
	}
	if _, err := otherRegistry.UpdateStatus(ctx, agent.ID, agents.Offline); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("updated another node: %v", err)
	}
	localAgents, err := otherRegistry.List(ctx)
	if err != nil || len(localAgents) != 0 {
		t.Fatalf("listed another node: %+v, %v", localAgents, err)
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	now := time.Now().UTC()
	var firstAgent agents.Agent
	for index := range 2 {
		store, err := storage.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		node, err := store.Node(ctx, "test")
		if err != nil {
			t.Fatal(err)
		}
		registry, err := agents.New(store, node.ID, agents.Options{OfflineAfter: 30 * time.Second, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			firstAgent, err = registry.Register(ctx, agents.Registration{DisplayName: "persistent", Metadata: agents.Metadata{Task: "saved", Files: []string{"a.go", "b.go"}}})
			if err != nil {
				t.Fatal(err)
			}
		} else {
			loadedAgent, err := registry.Get(ctx, firstAgent.ID)
			if err != nil || !reflect.DeepEqual(loadedAgent, firstAgent) {
				t.Fatalf("restart lost agent: %+v, %v", loadedAgent, err)
			}
			now = now.Add(time.Minute)
			localAgents, err := registry.List(ctx)
			if err != nil || len(localAgents) != 1 || localAgents[0].Status != agents.Offline {
				t.Fatalf("restart stale presence: %+v, %v", localAgents, err)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentHeartbeatAndMetadata(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	agent, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "concurrent"})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	workers.Go(func() {
		if _, err := fixture.registry.UpdateMetadata(ctx, agent.ID, agents.Metadata{Task: "saved task"}); err != nil {
			t.Error(err)
		}
	})
	workers.Go(func() {
		if _, err := fixture.registry.UpdateStatus(ctx, agent.ID, agents.Busy); err != nil {
			t.Error(err)
		}
	})
	workers.Go(func() {
		if _, err := fixture.registry.Heartbeat(ctx, agent.ID); err != nil {
			t.Error(err)
		}
	})
	workers.Wait()
	agent, err = fixture.registry.Get(ctx, agent.ID)
	if err != nil || agent.Status != agents.Busy || agent.Task != "saved task" {
		t.Fatalf("lost concurrent update: %+v, %v", agent, err)
	}
}

func TestCombinedOfflineUpdateLogsWithoutMetadata(t *testing.T) {
	fixture := newFixture(t)
	var output bytes.Buffer
	registry, err := agents.New(fixture.store, fixture.nodeID, agents.Options{OfflineAfter: 30 * time.Second, Logger: slog.New(slog.NewTextHandler(&output, nil))})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := registry.Register(context.Background(), agents.Registration{DisplayName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Update(context.Background(), agent.ID, agents.Offline, agents.Metadata{Task: "private-context-sentinel", Cwd: "/private/path"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "agent offline") || strings.Contains(output.String(), "private-context-sentinel") || strings.Contains(output.String(), "/private/path") {
		t.Fatal("offline logging contract violated", output.String())
	}
}
