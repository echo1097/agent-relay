package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/internal/messaging"
	"agent-relay/internal/relay"
	"agent-relay/internal/storage"
)

type overviewOptions struct {
	agentID     string
	includeRead bool
	limit       int
}

func showAgentDirectory(ctx context.Context, directory *relay.Directory, remoteOnly, includeArchived bool, output io.Writer) error {
	var listing relay.AgentList
	var err error
	if includeArchived {
		listing, err = directory.ListAll(ctx)
	} else {
		listing, err = directory.List(ctx)
	}
	if err != nil {
		return err
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tAGENT\tMACHINE\tSTATUS\tLAST SEEN\tTRUST\tTASK"); err != nil {
		return err
	}
	count := 0
	for _, agent := range listing.Agents {
		if remoteOnly && agent.Node.ID == directory.Node.ID {
			continue
		}
		count++
		status := string(agent.Status)
		if agent.Archived {
			status += " (archived)"
		}
		if _, err := fmt.Fprintf(writer, "%s\t%q\t%q\t%s\t%s\t%s\t%q\n", agent.ID, agent.DisplayName, agent.Node.Name, status, formatRelativeTime(pointerTime(agent.LastSeenAt)), agent.Trust, agent.Task); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if count == 0 {
		message := "No agents discovered. Connect an MCP client and check agent-relay peers."
		if !includeArchived {
			message += " Use --all to include archived sessions."
		}
		if _, err := fmt.Fprintln(output, message); err != nil {
			return err
		}
	}
	for _, nodeID := range listing.Unavailable {
		if _, err := fmt.Fprintf(output, "Unavailable node: %s (check Tailscale, the remote service, and agent-relay doctor).\n", nodeID); err != nil {
			return err
		}
	}
	return nil
}

func pointerTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func showOverview(ctx context.Context, store *storage.Store, registry *agents.Registry, command string, options overviewOptions, output io.Writer) error {
	if options.agentID != "" {
		if _, err := registry.Get(ctx, options.agentID); err != nil {
			return err
		}
	}
	if command == "conversations" {
		conversations, err := store.RecentConversations(ctx, options.agentID, options.limit)
		if err != nil {
			return err
		}
		if len(conversations) == 0 {
			_, err := fmt.Fprintln(output, "No conversations yet.")
			return err
		}
		writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(writer, "CONVERSATION\tLOCAL AGENT\tREMOTE AGENT\tUPDATED"); err != nil {
			return err
		}
		for _, conversation := range conversations {
			if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", conversation.ID, conversation.LocalAgentID, conversation.RemoteAgentID, formatTime(conversation.UpdatedAt)); err != nil {
				return err
			}
		}
		return writer.Flush()
	}
	if err := store.ExpireDeliveries(ctx, time.Now().UTC()); err != nil {
		return err
	}
	localAgents, err := registry.List(ctx)
	if err != nil {
		return err
	}
	messages := []messaging.Message{}
	for _, agent := range localAgents {
		if options.agentID != "" && options.agentID != agent.ID {
			continue
		}
		inbox, err := store.ListInbox(ctx, agent.ID, options.includeRead)
		if err != nil {
			return err
		}
		messages = append(messages, inbox...)
	}
	sort.Slice(messages, func(left, right int) bool {
		if messages[left].CreatedAt.Equal(messages[right].CreatedAt) {
			return messages[left].ID < messages[right].ID
		}
		return messages[left].CreatedAt.Before(messages[right].CreatedAt)
	})
	if len(messages) == 0 {
		_, err := fmt.Fprintln(output, "Inbox is empty.")
		return err
	}
	for _, message := range messages {
		if _, err := fmt.Fprintf(output, "%s (%s)\nFrom: %s\nTo: %s\nConversation: %s\nMessage: %s\nText: %q\n\n", message.Type, message.Status, message.SenderAgentID, message.RecipientAgentID, message.ConversationID, message.ID, message.Text); err != nil {
			return err
		}
	}
	return nil
}
