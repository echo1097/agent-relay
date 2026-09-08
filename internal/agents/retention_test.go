package agents_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"agent-relay/internal/agents"
)

func TestArchivedSessionCanResume(t *testing.T) {
	for _, action := range []string{"heartbeat", "metadata", "status", "reconnect"} {
		t.Run(action, func(t *testing.T) {
			fixture := newFixture(t)
			ctx := context.Background()
			agent, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "resumable"})
			if err != nil {
				t.Fatal(err)
			}
			fixture.now = fixture.now.Add(7 * 24 * time.Hour)
			if _, err := fixture.registry.Expire(ctx); err != nil {
				t.Fatal(err)
			}
			result, err := fixture.registry.Retain(ctx)
			if err != nil || result.Archived != 1 {
				t.Fatalf("archive: %+v %v", result, err)
			}
			archived, err := fixture.registry.Get(ctx, agent.ID)
			if err != nil || !archived.Archived || !archived.LastSeenAt.Equal(agent.LastSeenAt) {
				t.Fatalf("archive changed last seen: %+v %v", archived, err)
			}
			var resumed agents.Agent
			switch action {
			case "heartbeat":
				resumed, err = fixture.registry.Heartbeat(ctx, agent.ID)
			case "metadata":
				resumed, err = fixture.registry.UpdateMetadata(ctx, agent.ID, agents.Metadata{Task: "resumed"})
			case "status":
				resumed, err = fixture.registry.Update(ctx, agent.ID, agents.Busy, agent.Metadata)
			case "reconnect":
				resumed, err = fixture.registry.Register(ctx, agents.Registration{ID: agent.ID, DisplayName: "resumed"})
			}
			if err != nil || resumed.Archived || resumed.Status == agents.Offline || !resumed.LastSeenAt.Equal(fixture.now) || !resumed.RegisteredAt.Equal(agent.RegisteredAt) {
				t.Fatalf("resume: %+v %v", resumed, err)
			}
			fixture.now = fixture.now.Add(24 * 24 * time.Hour)
			if _, err := fixture.registry.Expire(ctx); err != nil {
				t.Fatal(err)
			}
			result, err = fixture.registry.Retain(ctx)
			if err != nil || result.Deleted != 0 || result.Archived != 1 {
				t.Fatalf("retention did not restart from resumed activity: %+v %v", result, err)
			}
		})
	}
}

func TestDeletedSessionCannotResume(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	agent, err := fixture.registry.Register(ctx, agents.Registration{DisplayName: "expired"})
	if err != nil {
		t.Fatal(err)
	}
	fixture.now = fixture.now.Add(30 * 24 * time.Hour)
	if _, err := fixture.registry.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.registry.Retain(ctx)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("delete: %+v %v", result, err)
	}
	if _, err := fixture.registry.Register(ctx, agents.Registration{ID: agent.ID, DisplayName: "resumed"}); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("deleted identity was recreated: %v", err)
	}
}
