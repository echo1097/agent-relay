package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"agent-relay/internal/messaging"
)

const conversationColumns = `id, local_agent_id, remote_agent_id, created_at, updated_at`
const messageColumns = `id, conversation_id, sender_agent_id, recipient_agent_id, type, text, COALESCE(reply_to, ''), status, created_at, expires_at, delivered_at, received_at, read_at, answered_at`

func messageTime(value time.Time) string {
	return value.UTC().Format(agentTimeFormat)
}

func optionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return messageTime(*value)
}

func validTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

func (store *Store) messageTransaction(ctx context.Context, action func(*sql.Conn) error) (returnErr error) {
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, err := conn.ExecContext(context.Background(), "ROLLBACK")
			returnErr = errors.Join(returnErr, err)
		}
	}()
	if err := action(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func scanConversation(row interface{ Scan(...any) error }) (messaging.Conversation, error) {
	var conversation messaging.Conversation
	var createdAt, updatedAt string
	err := row.Scan(&conversation.ID, &conversation.LocalAgentID, &conversation.RemoteAgentID, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return conversation, messaging.ErrNotFound
	}
	if err != nil {
		return conversation, err
	}
	conversation.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return conversation, err
	}
	conversation.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return conversation, err
}

func (store *Store) CreateConversation(ctx context.Context, conversation messaging.Conversation) (messaging.Conversation, error) {
	if strings.TrimSpace(conversation.ID) == "" || strings.TrimSpace(conversation.RemoteAgentID) == "" || conversation.LocalAgentID == conversation.RemoteAgentID || !validTime(conversation.CreatedAt) {
		return messaging.Conversation{}, messaging.ErrInvalid
	}
	result, err := scanConversation(store.db.QueryRowContext(ctx, `INSERT INTO conversations (`+conversationColumns+`)
 SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM agents JOIN local_node ON agents.node_id = local_node.node_id WHERE agents.id = ?)
 RETURNING `+conversationColumns, conversation.ID, conversation.LocalAgentID, conversation.RemoteAgentID, messageTime(conversation.CreatedAt), messageTime(conversation.CreatedAt), conversation.LocalAgentID))
	return result, err
}

func (store *Store) GetConversation(ctx context.Context, conversationID string) (messaging.Conversation, error) {
	return scanConversation(store.db.QueryRowContext(ctx, "SELECT "+conversationColumns+" FROM conversations WHERE id = ?", conversationID))
}

func (store *Store) ListConversations(ctx context.Context, agentID string) ([]messaging.Conversation, error) {
	rows, err := store.db.QueryContext(ctx, "SELECT "+conversationColumns+" FROM conversations WHERE local_agent_id = ? OR remote_agent_id = ? ORDER BY created_at, id", agentID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []messaging.Conversation{}
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}

func scanMessage(row interface{ Scan(...any) error }) (messaging.Message, error) {
	var message messaging.Message
	var createdAt string
	var expiresAt, deliveredAt, receivedAt, readAt, answeredAt sql.NullString
	err := row.Scan(&message.ID, &message.ConversationID, &message.SenderAgentID, &message.RecipientAgentID, &message.Type, &message.Text, &message.ReplyTo, &message.Status, &createdAt, &expiresAt, &deliveredAt, &receivedAt, &readAt, &answeredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return message, messaging.ErrNotFound
	}
	if err != nil {
		return message, err
	}
	message.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return message, err
	}
	for index, value := range []sql.NullString{expiresAt, deliveredAt, receivedAt, readAt, answeredAt} {
		if !value.Valid {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, value.String)
		if err != nil {
			return message, err
		}
		targets := []**time.Time{&message.ExpiresAt, &message.DeliveredAt, &message.ReceivedAt, &message.ReadAt, &message.AnsweredAt}
		*targets[index] = &parsed
	}
	return message, nil
}

func (store *Store) GetMessage(ctx context.Context, messageID string) (messaging.Message, error) {
	return scanMessage(store.db.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id = ?", messageID))
}

func (store *Store) SaveMessage(ctx context.Context, message messaging.Message, now time.Time) (messaging.Message, bool, error) {
	return store.insertMessage(ctx, message, now, false)
}

func (store *Store) ReceiveMessage(ctx context.Context, message messaging.Message, now time.Time) (messaging.Message, bool, error) {
	return store.insertMessage(ctx, message, now, true)
}

func sameMessage(first, second messaging.Message) bool {
	sameExpiration := first.ExpiresAt == nil && second.ExpiresAt == nil || first.ExpiresAt != nil && second.ExpiresAt != nil && first.ExpiresAt.Equal(*second.ExpiresAt)
	return first.ID == second.ID && first.ConversationID == second.ConversationID && first.SenderAgentID == second.SenderAgentID && first.RecipientAgentID == second.RecipientAgentID && first.Type == second.Type && first.Text == second.Text && first.ReplyTo == second.ReplyTo && first.CreatedAt.Equal(second.CreatedAt) && sameExpiration
}

func (store *Store) insertMessage(ctx context.Context, message messaging.Message, now time.Time, incoming bool) (messaging.Message, bool, error) {
	if !validTime(now) {
		return messaging.Message{}, false, messaging.ErrInvalid
	}
	if message.Type == messaging.Question && message.ExpiresAt == nil {
		expiresAt := message.CreatedAt.Add(messaging.DefaultRequestLifetime)
		message.ExpiresAt = &expiresAt
	}
	if err := message.Validate(); err != nil {
		return messaging.Message{}, false, err
	}
	var saved messaging.Message
	inserted := false
	err := store.messageTransaction(ctx, func(conn *sql.Conn) error {
		conversation, err := scanConversation(conn.QueryRowContext(ctx, "SELECT "+conversationColumns+" FROM conversations WHERE id = ?", message.ConversationID))
		if err != nil {
			return err
		}
		localID := message.SenderAgentID
		if incoming {
			localID = message.RecipientAgentID
		}
		if localID != conversation.LocalAgentID {
			return messaging.ErrInvalid
		}
		existing, err := scanMessage(conn.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id = ?", message.ID))
		if err == nil {
			if !sameMessage(existing, message) {
				return messaging.ErrConflict
			}
			saved = existing
			return nil
		}
		if !errors.Is(err, messaging.ErrNotFound) {
			return err
		}
		status := messaging.Created
		var receivedAt, deliveredAt any
		if incoming {
			status = messaging.Delivered
			receivedAt, deliveredAt = messageTime(now), messageTime(now)
		}
		if message.ExpiresAt != nil && !message.ExpiresAt.After(now) {
			status = messaging.Expired
		}
		if message.Type == messaging.Response {
			question, err := scanMessage(conn.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id = ?", message.ReplyTo))
			if err != nil {
				return err
			}
			if question.Type != messaging.Question || question.ConversationID != message.ConversationID || question.SenderAgentID != message.RecipientAgentID || question.RecipientAgentID != message.SenderAgentID {
				return messaging.ErrInvalid
			}
			if question.Status == messaging.Answered || question.Status == messaging.Expired || question.ExpiresAt != nil && !question.ExpiresAt.After(now) {
				return messaging.ErrTransition
			}
			if _, err := conn.ExecContext(ctx, "UPDATE messages SET status = 'answered', answered_at = ? WHERE id = ?", messageTime(now), question.ID); err != nil {
				return err
			}
		}
		var replyTo any
		if message.ReplyTo != "" {
			replyTo = message.ReplyTo
		}
		_, err = conn.ExecContext(ctx, `INSERT INTO messages (id, conversation_id, sender_agent_id, recipient_agent_id, type, text, reply_to, status, created_at, expires_at, delivered_at, received_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, message.ID, message.ConversationID, message.SenderAgentID, message.RecipientAgentID, message.Type, message.Text, replyTo, status, messageTime(message.CreatedAt), optionalTime(message.ExpiresAt), deliveredAt, receivedAt)
		if err != nil {
			return err
		}
		if incoming {
			if _, err := conn.ExecContext(ctx, "INSERT INTO processed_messages (message_id, processed_at) VALUES (?, ?)", message.ID, messageTime(now)); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, "UPDATE conversations SET updated_at = MAX(updated_at, ?) WHERE id = ?", messageTime(message.CreatedAt), message.ConversationID); err != nil {
			return err
		}
		saved, err = scanMessage(conn.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id = ?", message.ID))
		inserted = err == nil
		return err
	})
	if err != nil {
		return messaging.Message{}, false, err
	}
	return saved, inserted, nil
}

func (store *Store) listMessages(ctx context.Context, query string, args ...any) ([]messaging.Message, error) {
	rows, err := store.db.QueryContext(ctx, "SELECT "+messageColumns+" FROM messages "+query+" ORDER BY created_at, id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []messaging.Message{}
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

func (store *Store) ConversationHistory(ctx context.Context, conversationID string) ([]messaging.Message, error) {
	if _, err := store.GetConversation(ctx, conversationID); err != nil {
		return nil, err
	}
	return store.listMessages(ctx, "WHERE conversation_id = ?", conversationID)
}

func (store *Store) ListInbox(ctx context.Context, agentID string, includeRead bool) ([]messaging.Message, error) {
	return store.listMessages(ctx, "WHERE recipient_agent_id = ? AND received_at IS NOT NULL AND (? OR read_at IS NULL OR status IN ('delivered', 'pending') AND type = 'question')", agentID, includeRead)
}

func (store *Store) MarkMessageRead(ctx context.Context, agentID, messageID string, now time.Time) (messaging.Message, error) {
	if !validTime(now) {
		return messaging.Message{}, messaging.ErrInvalid
	}
	return scanMessage(store.db.QueryRowContext(ctx, `UPDATE messages SET read_at = COALESCE(read_at, ?), status = CASE WHEN type = 'question' AND status = 'delivered' THEN 'pending' ELSE status END WHERE id = ? AND recipient_agent_id = ? AND received_at IS NOT NULL RETURNING `+messageColumns, messageTime(now), messageID, agentID))
}

func (store *Store) ProcessedMessage(ctx context.Context, messageID string) (*time.Time, error) {
	var value string
	err := store.db.QueryRowContext(ctx, "SELECT processed_at FROM processed_messages WHERE message_id = ?", messageID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return &parsed, err
}

func (store *Store) UpdateMessageStatus(ctx context.Context, messageID string, next messaging.Status, now time.Time) (messaging.Message, error) {
	if !validTime(now) {
		return messaging.Message{}, messaging.ErrInvalid
	}
	var saved messaging.Message
	err := store.messageTransaction(ctx, func(conn *sql.Conn) error {
		message, err := scanMessage(conn.QueryRowContext(ctx, "SELECT "+messageColumns+" FROM messages WHERE id = ?", messageID))
		if err != nil {
			return err
		}
		if message.Status == next {
			saved = message
			return nil
		}
		if next == messaging.Answered || next == messaging.Expired || !message.Status.CanTransition(next, message.Type) {
			return messaging.ErrTransition
		}
		if message.ExpiresAt != nil && !message.ExpiresAt.After(now) {
			return messaging.ErrTransition
		}
		saved, err = scanMessage(conn.QueryRowContext(ctx, `UPDATE messages SET status = ?, delivered_at = CASE WHEN ? = 'delivered' THEN COALESCE(delivered_at, ?) ELSE delivered_at END WHERE id = ? RETURNING `+messageColumns, next, next, messageTime(now), messageID))
		return err
	})
	if err != nil {
		return messaging.Message{}, err
	}
	return saved, nil
}

func (store *Store) ExpireRequests(ctx context.Context, now time.Time) (int64, error) {
	if !validTime(now) {
		return 0, messaging.ErrInvalid
	}
	result, err := store.db.ExecContext(ctx, "UPDATE messages SET status = 'expired' WHERE type = 'question' AND expires_at <= ? AND status NOT IN ('answered', 'expired')", messageTime(now))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
