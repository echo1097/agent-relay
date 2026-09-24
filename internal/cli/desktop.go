package cli

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"agent-relay/internal/config"
	"agent-relay/internal/daemon"
	"agent-relay/internal/relay"
	"agent-relay/internal/storage"
)

func showDesktop(ctx context.Context, store *storage.Store, directory *relay.Directory, paths config.Paths, conversationID string, output io.Writer) error {
	if conversationID != "" {
		history, err := store.ConversationHistory(ctx, conversationID)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(history)
	}
	conversations, err := store.DesktopConversations(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	listing, err := directory.ListAll(ctx)
	if err != nil {
		return err
	}
	running, err := daemon.Running(paths.Lock)
	if err != nil {
		return err
	}
	dnd, err := store.Dnd(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]any{
		"agents":           listing.Agents,
		"unavailableNodes": listing.Unavailable,
		"conversations":    conversations,
		"node":             directory.Node,
		"daemonRunning":    running,
		"dnd":              dnd,
		"home":             paths.Home,
		"updatedAt":        time.Now().UTC(),
	})
}
