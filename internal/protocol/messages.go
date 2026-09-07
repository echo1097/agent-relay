package protocol

import (
	"errors"
	"time"
	"unicode/utf8"

	"agent-relay/internal/messaging"
)

const NodeHeader = "X-Agent-Relay-Node-ID"
const NodeNotTrusted = "NODE_NOT_TRUSTED"
const AgentNotFound = "AGENT_NOT_FOUND"
const MessageConflict = "MESSAGE_CONFLICT"
const MessageExpired = "MESSAGE_EXPIRED"
const MaxMessageBytes = 64 * 1024

type Message struct {
	ProtocolVersion  int            `json:"protocol_version"`
	ID               string         `json:"id"`
	ConversationID   string         `json:"conversation_id"`
	SenderAgentID    string         `json:"sender_agent_id"`
	RecipientAgentID string         `json:"recipient_agent_id"`
	Type             messaging.Type `json:"type"`
	CreatedAt        time.Time      `json:"created_at"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	Content          Content        `json:"content"`
}

type Content struct {
	Text string `json:"text"`
}

type DeliveryAck struct {
	ProtocolVersion int       `json:"protocol_version"`
	NodeID          string    `json:"node_id"`
	MessageID       string    `json:"message_id"`
	Status          string    `json:"status"`
	ReceivedAt      time.Time `json:"received_at"`
}

func WireMessage(message messaging.Message) Message {
	return Message{ProtocolVersion: Version, ID: message.ID, ConversationID: message.ConversationID, SenderAgentID: message.SenderAgentID, RecipientAgentID: message.RecipientAgentID, Type: message.Type, CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, Content: Content{Text: message.Text}}
}

func (message Message) Local() messaging.Message {
	return messaging.Message{ID: message.ID, ConversationID: message.ConversationID, SenderAgentID: message.SenderAgentID, RecipientAgentID: message.RecipientAgentID, Type: message.Type, CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt, Text: message.Content.Text}
}

func (message Message) Validate() error {
	if message.ProtocolVersion != Version || !validID(message.ID, "msg_") || !validID(message.ConversationID, "conv_") || !validID(message.SenderAgentID, "agent_") || !validID(message.RecipientAgentID, "agent_") {
		return messaging.ErrInvalid
	}
	if message.Type != messaging.MessageType && message.Type != messaging.Question || !utf8.ValidString(message.Content.Text) || len(message.Content.Text) > MaxMessageBytes {
		return messaging.ErrInvalid
	}
	if message.Type == messaging.Question && message.ExpiresAt == nil {
		return messaging.ErrInvalid
	}
	return message.Local().Validate()
}

func (ack DeliveryAck) Validate() error {
	if ack.ProtocolVersion != Version || !validID(ack.NodeID, "node_") || !validID(ack.MessageID, "msg_") || ack.Status != "delivered" || ack.ReceivedAt.IsZero() {
		return errors.New("invalid delivery acknowledgment")
	}
	return nil
}
