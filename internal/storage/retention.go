package storage

import (
	"context"
	"database/sql"
	"time"

	"agent-relay/internal/agents"
)

func readRetentionPolicy(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (agents.RetentionPolicy, error) {
	var policy agents.RetentionPolicy
	err := query.QueryRowContext(ctx, "SELECT archive_after_days, delete_after_days FROM local_settings WHERE id = 1").Scan(&policy.ArchiveAfterDays, &policy.DeleteAfterDays)
	if err != nil {
		return policy, err
	}
	return policy, policy.Validate()
}

func (store *Store) RetentionPolicy(ctx context.Context) (agents.RetentionPolicy, error) {
	return readRetentionPolicy(ctx, store.db)
}

func (store *Store) UpdateRetentionPolicy(ctx context.Context, archiveDays, deleteDays *int) (agents.RetentionPolicy, error) {
	var policy agents.RetentionPolicy
	err := store.messageTransaction(ctx, func(conn *sql.Conn) error {
		var err error
		policy, err = readRetentionPolicy(ctx, conn)
		if err != nil {
			return err
		}
		if archiveDays != nil {
			policy.ArchiveAfterDays = *archiveDays
		}
		if deleteDays != nil {
			policy.DeleteAfterDays = *deleteDays
		}
		if err := policy.Validate(); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, "UPDATE local_settings SET archive_after_days = ?, delete_after_days = ? WHERE id = 1", policy.ArchiveAfterDays, policy.DeleteAfterDays)
		return err
	})
	return policy, err
}

func (store *Store) RetainAgents(ctx context.Context, nodeID string, now time.Time) (agents.RetentionResult, error) {
	var result agents.RetentionResult
	err := store.messageTransaction(ctx, func(conn *sql.Conn) error {
		policy, err := readRetentionPolicy(ctx, conn)
		if err != nil {
			return err
		}
		deleteCutoff := now.UTC().Add(-time.Duration(policy.DeleteAfterDays) * 24 * time.Hour).Format(agentTimeFormat)
		archiveCutoff := now.UTC().Add(-time.Duration(policy.ArchiveAfterDays) * 24 * time.Hour).Format(agentTimeFormat)
		oldAgents := "SELECT id FROM agents WHERE node_id = ? AND status = 'offline' AND last_seen_at <= ?"
		oldConversations := "SELECT id FROM conversations WHERE local_agent_id IN (" + oldAgents + ")"
		oldMessages := "SELECT id FROM messages WHERE conversation_id IN (" + oldConversations + ")"
		deleteQueries := []string{
			"DELETE FROM outbox WHERE message_id IN (" + oldMessages + ")",
			"DELETE FROM processed_messages WHERE message_id IN (" + oldMessages + ")",
			"DELETE FROM messages WHERE conversation_id IN (" + oldConversations + ")",
			"DELETE FROM conversation_peers WHERE conversation_id IN (" + oldConversations + ")",
			"DELETE FROM conversations WHERE local_agent_id IN (" + oldAgents + ")",
		}
		for _, query := range deleteQueries {
			if _, err := conn.ExecContext(ctx, query, nodeID, deleteCutoff); err != nil {
				return err
			}
		}
		deleted, err := conn.ExecContext(ctx, "DELETE FROM agents WHERE id IN ("+oldAgents+")", nodeID, deleteCutoff)
		if err != nil {
			return err
		}
		result.Deleted, err = deleted.RowsAffected()
		if err != nil {
			return err
		}
		archived, err := conn.ExecContext(ctx, "UPDATE agents SET archived = 1 WHERE node_id = ? AND status = 'offline' AND last_seen_at <= ? AND archived = 0", nodeID, archiveCutoff)
		if err != nil {
			return err
		}
		result.Archived, err = archived.RowsAffected()
		if err != nil {
			return err
		}
		restored, err := conn.ExecContext(ctx, "UPDATE agents SET archived = 0 WHERE node_id = ? AND archived = 1 AND (status != 'offline' OR last_seen_at > ?)", nodeID, archiveCutoff)
		if err != nil {
			return err
		}
		result.Restored, err = restored.RowsAffected()
		return err
	})
	if err != nil {
		return agents.RetentionResult{}, err
	}
	return result, nil
}
