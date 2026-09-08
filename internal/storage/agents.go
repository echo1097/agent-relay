package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent-relay/internal/agents"
)

const agentColumns = `id, node_id, display_name, provider, status, task, project, repository, branch, cwd, files, registered_at, last_seen_at, archived`
const agentTimeFormat = "2006-01-02T15:04:05.000000000Z"

func scanAgent(row interface{ Scan(...any) error }) (agents.Agent, error) {
	var agent agents.Agent
	var files, registeredAt, lastSeenAt string
	err := row.Scan(&agent.ID, &agent.NodeID, &agent.DisplayName, &agent.Provider, &agent.Status, &agent.Task, &agent.Project, &agent.Repository, &agent.Branch, &agent.Cwd, &files, &registeredAt, &lastSeenAt, &agent.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return agent, agents.ErrNotFound
	}
	if err != nil {
		return agent, err
	}
	if err := json.Unmarshal([]byte(files), &agent.Files); err != nil {
		return agent, fmt.Errorf("decode agent files: %w", err)
	}
	agent.RegisteredAt, err = time.Parse(time.RFC3339Nano, registeredAt)
	if err != nil {
		return agent, err
	}
	agent.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt)
	return agent, err
}

func (store *Store) RegisterAgent(ctx context.Context, agent agents.Agent, resume bool) (agents.Agent, error) {
	files, err := json.Marshal(agent.Files)
	if err != nil {
		return agents.Agent{}, err
	}
	if resume {
		return scanAgent(store.db.QueryRowContext(ctx, `UPDATE agents SET display_name = ?, provider = ?, status = 'online', archived = 0, task = ?, project = ?, repository = ?, branch = ?, cwd = ?, files = ?, last_seen_at = MAX(last_seen_at, ?) WHERE id = ? AND node_id = ? RETURNING `+agentColumns,
			agent.DisplayName, agent.Provider, agent.Task, agent.Project, agent.Repository, agent.Branch, agent.Cwd, string(files), agent.LastSeenAt.Format(agentTimeFormat), agent.ID, agent.NodeID))
	}
	return scanAgent(store.db.QueryRowContext(ctx, `INSERT INTO agents (`+agentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0) RETURNING `+agentColumns,
		agent.ID, agent.NodeID, agent.DisplayName, agent.Provider, agent.Status, agent.Task, agent.Project, agent.Repository, agent.Branch, agent.Cwd, string(files), agent.RegisteredAt.Format(agentTimeFormat), agent.LastSeenAt.Format(agentTimeFormat)))
}

func (store *Store) GetAgent(ctx context.Context, nodeID, agentID string) (agents.Agent, error) {
	return scanAgent(store.db.QueryRowContext(ctx, "SELECT "+agentColumns+" FROM agents WHERE id = ? AND node_id = ?", agentID, nodeID))
}

func (store *Store) ListAgents(ctx context.Context, nodeID string) ([]agents.Agent, error) {
	rows, err := store.db.QueryContext(ctx, "SELECT "+agentColumns+" FROM agents WHERE node_id = ? ORDER BY registered_at, id", nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []agents.Agent{}
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, agent)
	}
	return result, rows.Err()
}

func (store *Store) UpdateAgent(ctx context.Context, nodeID, agentID string, status *agents.Status, metadata *agents.Metadata, seenAt time.Time) (agents.Agent, error) {
	fields := []string{"last_seen_at = MAX(last_seen_at, ?)"}
	values := []any{seenAt.UTC().Format(agentTimeFormat)}
	if status != nil {
		fields = append(fields, "status = ?")
		values = append(values, *status)
	} else {
		fields = append(fields, "status = CASE WHEN status = 'offline' THEN 'online' ELSE status END")
	}
	if status == nil || *status != agents.Offline {
		fields = append(fields, "archived = 0")
	}
	if metadata != nil {
		files, err := json.Marshal(metadata.Files)
		if err != nil {
			return agents.Agent{}, err
		}
		fields = append(fields, "task = ?", "project = ?", "repository = ?", "branch = ?", "cwd = ?", "files = ?")
		values = append(values, metadata.Task, metadata.Project, metadata.Repository, metadata.Branch, metadata.Cwd, string(files))
	}
	values = append(values, agentID, nodeID)
	return scanAgent(store.db.QueryRowContext(ctx, "UPDATE agents SET "+strings.Join(fields, ", ")+" WHERE id = ? AND node_id = ? RETURNING "+agentColumns, values...))
}

func (store *Store) ExpireAgents(ctx context.Context, nodeID string, cutoff time.Time) (int64, error) {
	result, err := store.db.ExecContext(ctx, "UPDATE agents SET status = 'offline' WHERE node_id = ? AND status != 'offline' AND last_seen_at <= ?", nodeID, cutoff.UTC().Format(agentTimeFormat))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (store *Store) OfflineAgents(ctx context.Context, nodeID string) (int64, error) {
	result, err := store.db.ExecContext(ctx, "UPDATE agents SET status = 'offline' WHERE node_id = ? AND status != 'offline'", nodeID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
