package storage

import (
	"context"
	"time"
)

type DesktopConversation struct {
	ID            string `json:"id"`
	LocalAgentID  string `json:"localAgentId"`
	RemoteAgentID string `json:"remoteAgentId"`
	Title         string `json:"title"`
	Preview       string `json:"preview"`
	UpdatedAt     string `json:"updatedAt"`
	Unread        bool   `json:"unread"`
	NeedsReply    bool   `json:"needsReply"`
	Status        string `json:"status"`
	MessageCount  int    `json:"messageCount"`
}

func (store *Store) DesktopConversations(ctx context.Context, now time.Time) ([]DesktopConversation, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT c.id, c.local_agent_id, c.remote_agent_id, c.updated_at,
 COALESCE((SELECT CASE WHEN length(text) > 100 THEN substr(text, 1, 100) || '…' ELSE text END FROM messages WHERE conversation_id = c.id ORDER BY created_at, id LIMIT 1), 'Empty conversation'),
 COALESCE((SELECT substr(text, 1, 240) FROM messages WHERE conversation_id = c.id ORDER BY created_at DESC, id DESC LIMIT 1), ''),
 COALESCE((SELECT CASE WHEN type = 'question' AND expires_at <= ? AND status != 'answered' THEN 'expired' ELSE status END FROM messages WHERE conversation_id = c.id ORDER BY created_at DESC, id DESC LIMIT 1), 'empty'),
 EXISTS(SELECT 1 FROM messages WHERE conversation_id = c.id AND received_at IS NOT NULL AND read_at IS NULL),
 EXISTS(SELECT 1 FROM messages WHERE conversation_id = c.id AND received_at IS NOT NULL AND type = 'question' AND status IN ('delivered', 'pending') AND (expires_at IS NULL OR expires_at > ?)),
 (SELECT COUNT(*) FROM messages WHERE conversation_id = c.id)
 FROM conversations c ORDER BY c.updated_at DESC, c.id DESC`, messageTime(now), messageTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []DesktopConversation{}
	for rows.Next() {
		var conversation DesktopConversation
		if err := rows.Scan(&conversation.ID, &conversation.LocalAgentID, &conversation.RemoteAgentID, &conversation.UpdatedAt, &conversation.Title, &conversation.Preview, &conversation.Status, &conversation.Unread, &conversation.NeedsReply, &conversation.MessageCount); err != nil {
			return nil, err
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}
