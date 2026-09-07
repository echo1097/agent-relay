package messaging

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, prefix := range []string{"msg", "conv"} {
		for range 100 {
			id, err := NewID(prefix)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := uuid.Parse(strings.TrimPrefix(id, prefix+"_"))
			if err != nil || parsed.Version() != 7 || seen[id] {
				t.Fatalf("invalid or duplicate ID: %s %v", id, err)
			}
			seen[id] = true
		}
	}
}

func TestValidation(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	base := Message{ID: "msg_test", ConversationID: "conv_test", SenderAgentID: "a", RecipientAgentID: "b", Type: MessageType, Text: "text", CreatedAt: now}
	for _, messageType := range []Type{MessageType, Question, Response} {
		message := base
		message.Type = messageType
		if messageType == Response {
			message.ReplyTo = "question"
		}
		if err := message.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for name, change := range map[string]func(*Message){
		"id":                    func(message *Message) { message.ID = " " },
		"conversation":          func(message *Message) { message.ConversationID = "" },
		"sender":                func(message *Message) { message.SenderAgentID = "" },
		"recipient":             func(message *Message) { message.RecipientAgentID = "" },
		"self":                  func(message *Message) { message.RecipientAgentID = message.SenderAgentID },
		"text":                  func(message *Message) { message.Text = "\t\n" },
		"time":                  func(message *Message) { message.CreatedAt = time.Time{} },
		"type":                  func(message *Message) { message.Type = "bad" },
		"response without link": func(message *Message) { message.Type = Response },
		"unexpected link":       func(message *Message) { message.ReplyTo = "other" },
		"unexpected expiration": func(message *Message) { deadline := now.Add(time.Hour); message.ExpiresAt = &deadline },
		"early expiration":      func(message *Message) { message.Type = Question; message.ExpiresAt = &now },
	} {
		t.Run(name, func(t *testing.T) {
			message := base
			change(&message)
			if err := message.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid message: %v", err)
			}
		})
	}
}

func TestStatusTransitions(t *testing.T) {
	allowed := map[Status][]Status{
		Created:         {Sending, PendingDelivery, Failed, Expired},
		Sending:         {Delivered, Failed, PendingDelivery, Expired},
		Failed:          {Sending, Expired},
		PendingDelivery: {Sending, Expired},
		Delivered:       {Pending, Answered, Expired},
		Pending:         {Answered, Expired},
	}
	statuses := []Status{Created, Sending, Delivered, Pending, Answered, Failed, Expired, PendingDelivery, "invalid"}
	for _, messageType := range []Type{MessageType, Question, Response} {
		for _, from := range statuses {
			for _, to := range statuses {
				expected := false
				for _, next := range allowed[from] {
					expected = expected || next == to
				}
				if messageType != Question && (to == Pending || to == Answered || to == Expired) {
					expected = false
				}
				if from.CanTransition(to, messageType) != expected {
					t.Fatalf("%s: %s -> %s", messageType, from, to)
				}
			}
		}
	}
}
