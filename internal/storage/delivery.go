package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"agent-relay/internal/messaging"
)

type Delivery struct {
	MessageID     string
	NodeID        string
	Attempts      int
	NextAttemptAt time.Time
	Deadline      time.Time
	LastError     string
}

func prepareDelivery(ctx context.Context, conn *sql.Conn, message messaging.Message, incoming bool, peerID string) error {
	localID, remoteID := message.SenderAgentID, message.RecipientAgentID
	if incoming {
		localID, remoteID = remoteID, localID
	}
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents JOIN local_node ON agents.node_id = local_node.node_id WHERE agents.id = ?", localID).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return messaging.ErrNotFound
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents WHERE id = ?", remoteID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return messaging.ErrInvalid
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO remote_agents (agent_id, node_id) VALUES (?, ?) ON CONFLICT DO NOTHING", remoteID, peerID); err != nil {
		return err
	}
	var ownerID string
	if err := conn.QueryRowContext(ctx, "SELECT node_id FROM remote_agents WHERE agent_id = ?", remoteID).Scan(&ownerID); err != nil {
		return err
	}
	if ownerID != peerID {
		return messaging.ErrConflict
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO conversations (id, local_agent_id, remote_agent_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING", message.ConversationID, localID, remoteID, messageTime(message.CreatedAt), messageTime(message.CreatedAt)); err != nil {
		return err
	}
	conversation, err := scanConversation(conn.QueryRowContext(ctx, "SELECT "+conversationColumns+" FROM conversations WHERE id = ?", message.ConversationID))
	if err != nil {
		return err
	}
	if conversation.LocalAgentID != localID || conversation.RemoteAgentID != remoteID {
		return messaging.ErrConflict
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO conversation_peers (conversation_id, node_id) VALUES (?, ?) ON CONFLICT DO NOTHING", message.ConversationID, peerID); err != nil {
		return err
	}
	if err := conn.QueryRowContext(ctx, "SELECT node_id FROM conversation_peers WHERE conversation_id = ?", message.ConversationID).Scan(&ownerID); err != nil {
		return err
	}
	if ownerID != peerID {
		return messaging.ErrConflict
	}
	return nil
}

func (store *Store) ReceiveRemote(ctx context.Context, message messaging.Message, peerID string, now time.Time) (messaging.Message, bool, error) {
	if peerID == "" {
		return messaging.Message{}, false, messaging.ErrInvalid
	}
	return store.insertMessage(ctx, message, now, true, peerID, time.Time{})
}

func (store *Store) QueueMessage(ctx context.Context, message messaging.Message, peerID string, now, deadline time.Time) (messaging.Message, bool, error) {
	if peerID == "" || !validTime(deadline) || !deadline.After(now) {
		return messaging.Message{}, false, messaging.ErrInvalid
	}
	return store.insertMessage(ctx, message, now, false, peerID, deadline)
}

func (store *Store) DueDeliveries(ctx context.Context, now time.Time) ([]Delivery, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT message_id, node_id, attempts, next_attempt_at, deadline, last_error FROM outbox JOIN messages ON messages.id = outbox.message_id WHERE next_attempt_at <= ? AND status IN ('created', 'sending', 'pending_delivery') ORDER BY next_attempt_at, message_id LIMIT 100`, messageTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Delivery{}
	for rows.Next() {
		var item Delivery
		var nextAt, deadline string
		if err := rows.Scan(&item.MessageID, &item.NodeID, &item.Attempts, &nextAt, &deadline, &item.LastError); err != nil {
			return nil, err
		}
		item.NextAttemptAt, err = time.Parse(time.RFC3339Nano, nextAt)
		if err != nil {
			return nil, err
		}
		item.Deadline, err = time.Parse(time.RFC3339Nano, deadline)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) FinishDelivery(ctx context.Context, messageID string, status messaging.Status, nextAt, now time.Time, reason string) error {
	if status != messaging.Delivered && status != messaging.Failed && status != messaging.PendingDelivery {
		return messaging.ErrInvalid
	}
	return store.messageTransaction(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "UPDATE outbox SET attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE message_id = ?", messageTime(nextAt), reason, messageID); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx, "UPDATE messages SET status = ?, delivered_at = CASE WHEN ? = 'delivered' THEN COALESCE(delivered_at, ?) ELSE delivered_at END WHERE id = ? AND received_at IS NULL AND status IN ('created', 'sending', 'pending_delivery')", status, status, messageTime(now), messageID)
		return err
	})
}

func (store *Store) ExpireDeliveries(ctx context.Context, now time.Time) error {
	if _, err := store.ExpireRequests(ctx, now); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `UPDATE messages SET status = 'failed' WHERE type IN ('message', 'response') AND status IN ('created', 'sending', 'pending_delivery') AND id IN (SELECT message_id FROM outbox WHERE deadline <= ?)`, messageTime(now))
	return err
}

func (store *Store) ConversationPeer(ctx context.Context, conversationID string) (string, error) {
	var peerID string
	err := store.db.QueryRowContext(ctx, "SELECT node_id FROM conversation_peers WHERE conversation_id = ?", conversationID).Scan(&peerID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", messaging.ErrNotFound
	}
	return peerID, err
}
