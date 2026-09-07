package agents

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	Online  Status = "online"
	Busy    Status = "busy"
	Idle    Status = "idle"
	Offline Status = "offline"
)

var ErrNotFound = errors.New("local agent not found")

type Metadata struct {
	Task       string   `json:"task"`
	Project    string   `json:"project"`
	Repository string   `json:"repository"`
	Branch     string   `json:"branch"`
	Cwd        string   `json:"cwd"`
	Files      []string `json:"files"`
}

type Agent struct {
	ID          string `json:"id"`
	NodeID      string `json:"node_id"`
	DisplayName string `json:"display_name"`
	Provider    string `json:"provider"`
	Status      Status `json:"status"`
	Metadata
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

type Registration struct {
	ID          string
	DisplayName string
	Provider    string
	Metadata
}

type Store interface {
	RegisterAgent(context.Context, Agent, bool) (Agent, error)
	GetAgent(context.Context, string, string) (Agent, error)
	ListAgents(context.Context, string) ([]Agent, error)
	UpdateAgent(context.Context, string, string, *Status, *Metadata, time.Time) (Agent, error)
	ExpireAgents(context.Context, string, time.Time) (int64, error)
	OfflineAgents(context.Context, string) (int64, error)
}

type Options struct {
	OfflineAfter time.Duration
	Now          func() time.Time
	Logger       *slog.Logger
}

type Registry struct {
	store        Store
	nodeID       string
	offlineAfter time.Duration
	now          func() time.Time
	logger       *slog.Logger
}

func New(store Store, nodeID string, options Options) (*Registry, error) {
	if store == nil || nodeID == "" {
		return nil, errors.New("registry requires a store and local node ID")
	}
	if options.OfflineAfter <= 0 {
		return nil, errors.New("offline timeout must be positive")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Registry{store: store, nodeID: nodeID, offlineAfter: options.OfflineAfter, now: options.Now, logger: options.Logger}, nil
}

func (status Status) Valid() bool {
	switch status {
	case Online, Busy, Idle, Offline:
		return true
	default:
		return false
	}
}

func (registry *Registry) Register(ctx context.Context, input Registration) (Agent, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return Agent{}, errors.New("agent display name is required")
	}
	resume := input.ID != ""
	if !resume {
		agentUUID, err := uuid.NewV7()
		if err != nil {
			return Agent{}, err
		}
		input.ID = "agent_" + agentUUID.String()
	}
	now := registry.now().UTC()
	agent := Agent{ID: input.ID, NodeID: registry.nodeID, DisplayName: input.DisplayName, Provider: input.Provider, Status: Online, Metadata: input.Metadata, RegisteredAt: now, LastSeenAt: now}
	result, err := registry.store.RegisterAgent(ctx, agent, resume)
	if err == nil {
		registry.logger.Info("agent registered", "agent_id", result.ID, "resumed", resume)
	}
	return result, err
}

func (registry *Registry) Get(ctx context.Context, agentID string) (Agent, error) {
	if _, err := registry.Expire(ctx); err != nil {
		return Agent{}, err
	}
	return registry.store.GetAgent(ctx, registry.nodeID, agentID)
}

func (registry *Registry) List(ctx context.Context) ([]Agent, error) {
	if _, err := registry.Expire(ctx); err != nil {
		return nil, err
	}
	return registry.store.ListAgents(ctx, registry.nodeID)
}

func (registry *Registry) Heartbeat(ctx context.Context, agentID string) (Agent, error) {
	if _, err := registry.Expire(ctx); err != nil {
		return Agent{}, err
	}
	return registry.store.UpdateAgent(ctx, registry.nodeID, agentID, nil, nil, registry.now().UTC())
}

func (registry *Registry) UpdateStatus(ctx context.Context, agentID string, status Status) (Agent, error) {
	if !status.Valid() {
		return Agent{}, errors.New("agent status must be online, busy, idle, or offline")
	}
	result, err := registry.store.UpdateAgent(ctx, registry.nodeID, agentID, &status, nil, registry.now().UTC())
	if err == nil && status == Offline {
		registry.logger.Info("agent offline", "agent_id", agentID, "reason", "explicit")
	}
	return result, err
}

func (registry *Registry) UpdateMetadata(ctx context.Context, agentID string, metadata Metadata) (Agent, error) {
	if _, err := registry.Expire(ctx); err != nil {
		return Agent{}, err
	}
	return registry.store.UpdateAgent(ctx, registry.nodeID, agentID, nil, &metadata, registry.now().UTC())
}

func (registry *Registry) Disconnect(ctx context.Context, agentID string) error {
	_, err := registry.UpdateStatus(ctx, agentID, Offline)
	return err
}

func (registry *Registry) Expire(ctx context.Context) (int64, error) {
	count, err := registry.store.ExpireAgents(ctx, registry.nodeID, registry.now().UTC().Add(-registry.offlineAfter))
	if err == nil && count > 0 {
		registry.logger.Info("agent offline", "count", count, "reason", "timeout")
	}
	return count, err
}

func (registry *Registry) OfflineAll(ctx context.Context) error {
	count, err := registry.store.OfflineAgents(ctx, registry.nodeID)
	if err == nil && count > 0 {
		registry.logger.Info("agent offline", "count", count, "reason", "daemon shutdown")
	}
	return err
}
