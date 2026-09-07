package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"agent-relay/internal/messaging"
)

func TestResponseWireRoundTrip(t *testing.T) {
	message := messaging.Message{ID: "msg_019a84fc-1b72-7000-8000-000000000003", ConversationID: "conv_019a84fc-1b72-7000-8000-000000000004", SenderAgentID: testAgentID, RecipientAgentID: "agent_019a84fc-1b72-7000-8000-000000000005", Type: messaging.Response, ReplyTo: "msg_019a84fc-1b72-7000-8000-000000000006", Text: "yes", CreatedAt: time.Now().UTC()}
	wire := WireMessage(message)
	data, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil || !reflect.DeepEqual(decoded.Local(), message) {
		t.Fatalf("response mapping: %+v %v", decoded, err)
	}
	for _, replyTo := range []string{"", "invalid", testAgentID} {
		invalid := wire
		invalid.ReplyTo = replyTo
		if invalid.Validate() == nil {
			t.Fatalf("accepted reply link %q", replyTo)
		}
	}
	wire.Type = messaging.MessageType
	if wire.Validate() == nil {
		t.Fatal("regular message accepted a response link")
	}
}
