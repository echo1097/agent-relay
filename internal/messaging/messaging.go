package messaging

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Type string

type Status string

const (
	MessageType            Type   = "message"
	Question               Type   = "question"
	Response               Type   = "response"
	Created                Status = "created"
	Sending                Status = "sending"
	Delivered              Status = "delivered"
	Pending                Status = "pending"
	Answered               Status = "answered"
	Failed                 Status = "failed"
	Expired                Status = "expired"
	PendingDelivery        Status = "pending_delivery"
	DefaultRequestLifetime        = 24 * time.Hour
)

var (
	ErrExpired    = errors.New("message expired")
	ErrNotFound   = errors.New("conversation or message not found")
	ErrInvalid    = errors.New("invalid conversation or message")
	ErrConflict   = errors.New("message ID already has different content")
	ErrTransition = errors.New("invalid message status transition")
)

type Conversation struct {
	ID            string
	LocalAgentID  string
	RemoteAgentID string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Message struct {
	ID               string
	ConversationID   string
	SenderAgentID    string
	RecipientAgentID string
	Type             Type
	Text             string
	ReplyTo          string
	Status           Status
	CreatedAt        time.Time
	ExpiresAt        *time.Time
	DeliveredAt      *time.Time
	ReceivedAt       *time.Time
	ReadAt           *time.Time
	AnsweredAt       *time.Time
}

func NewID(prefix string) (string, error) {
	value, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return prefix + "_" + value.String(), nil
}

func (message Message) Validate() error {
	for _, value := range []string{message.ID, message.ConversationID, message.SenderAgentID, message.RecipientAgentID, message.Text} {
		if strings.TrimSpace(value) == "" {
			return ErrInvalid
		}
	}
	if message.SenderAgentID == message.RecipientAgentID || message.CreatedAt.IsZero() || message.CreatedAt.Year() < 1 || message.CreatedAt.Year() > 9999 {
		return ErrInvalid
	}
	if message.Type != MessageType && message.Type != Question && message.Type != Response {
		return ErrInvalid
	}
	if (message.Type == Response) != (message.ReplyTo != "") {
		return ErrInvalid
	}
	if message.ExpiresAt != nil && (message.Type != Question || !message.ExpiresAt.After(message.CreatedAt) || message.ExpiresAt.Year() > 9999) {
		return ErrInvalid
	}
	return nil
}

func (status Status) CanTransition(next Status, messageType Type) bool {
	if next == Pending || next == Answered || next == Expired {
		if messageType != Question {
			return false
		}
	}
	switch status {
	case Created:
		return next == Sending || next == PendingDelivery || next == Failed || next == Expired
	case Sending:
		return next == Delivered || next == Failed || next == PendingDelivery || next == Expired
	case Failed, PendingDelivery:
		return next == Sending || next == Expired
	case Delivered:
		return next == Pending || next == Answered || next == Expired
	case Pending:
		return next == Answered || next == Expired
	}
	return false
}
