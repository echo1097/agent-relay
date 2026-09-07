package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"agent-relay/internal/messaging"
	"agent-relay/internal/transport"
)

const messageUsage = `Usage: agent-relay messages <action> [--home PATH]

send --from ID --to ID --peer NODE_ID --text TEXT [--type message|question] [--conversation ID]
respond --from ID --id QUESTION_ID --text TEXT
get --id MESSAGE_ID
inbox --agent ID
history --conversation ID

Send durably queues work for the daemon. Get reports the current local status.
The peer must be explicitly trusted with agent-relay trust. Questions default to
messages.request_expiration_hours. Offline registered recipients retain inboxes.
`

type messageOptions struct {
	action       string
	from         string
	to           string
	peer         string
	text         string
	kind         string
	conversation string
	id           string
	agent        string
}

func messageArguments(flags *flag.FlagSet, args []string) (*messageOptions, []string, error) {
	if len(args) == 0 {
		return nil, nil, errors.New(messageUsage)
	}
	options := &messageOptions{action: args[0]}
	switch options.action {
	case "send":
		flags.StringVar(&options.from, "from", "", "local sender ID")
		flags.StringVar(&options.to, "to", "", "remote recipient ID")
		flags.StringVar(&options.peer, "peer", "", "trusted remote node ID")
		flags.StringVar(&options.text, "text", "", "message text")
		flags.StringVar(&options.kind, "type", "message", "message or question")
		flags.StringVar(&options.conversation, "conversation", "", "existing conversation ID")
	case "respond":
		flags.StringVar(&options.from, "from", "", "local responding agent ID")
		flags.StringVar(&options.id, "id", "", "original question ID")
		flags.StringVar(&options.text, "text", "", "response text")
	case "get":
		flags.StringVar(&options.id, "id", "", "message ID")
	case "inbox":
		flags.StringVar(&options.agent, "agent", "", "local agent ID")
	case "history":
		flags.StringVar(&options.conversation, "conversation", "", "conversation ID")
	default:
		return nil, nil, errors.New(messageUsage)
	}
	return options, args[1:], nil
}

func runMessages(ctx context.Context, service *transport.Service, options *messageOptions, output io.Writer) error {
	if err := service.Store.ExpireDeliveries(ctx, time.Now().UTC()); err != nil {
		return err
	}
	var result any
	var err error
	switch options.action {
	case "send":
		result, err = service.Queue(ctx, messaging.Message{SenderAgentID: options.from, RecipientAgentID: options.to, Text: options.text, Type: messaging.Type(options.kind), ConversationID: options.conversation}, options.peer)
	case "respond":
		if options.from == "" || options.id == "" {
			return errors.New("--from and --id are required")
		}
		result, err = service.Respond(ctx, options.from, options.id, options.text)
	case "get":
		if options.id == "" {
			return errors.New("--id is required")
		}
		result, err = service.Store.GetMessage(ctx, options.id)
	case "inbox":
		if options.agent == "" {
			return errors.New("--agent is required")
		}
		result, err = service.Store.ListInbox(ctx, options.agent, false)
	case "history":
		if options.conversation == "" {
			return errors.New("--conversation is required")
		}
		result, err = service.Store.ConversationHistory(ctx, options.conversation)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
