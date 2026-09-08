package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"agent-relay/internal/agents"
	"agent-relay/internal/storage"
)

const agentUsage = `Usage: agent-relay agents [action] [options]

Actions:
  list                       List local and remote agents (default); --local skips peer lookup
  retention                  Show session archive and deletion settings
  set-retention              Update retention settings; use --archive-days N and/or --delete-days N
  get --id ID                Show one agent as JSON
  register --name NAME       Register a session and print its ID
  heartbeat --id ID          Refresh presence
  set-status --id ID --state online|busy|idle|offline
  update-metadata --id ID    Replace all metadata fields
  disconnect --id ID         Mark a session offline immediately

Every action accepts --home PATH.
Set-retention accepts --archive-days N and --delete-days N; provide at least one.
Register also accepts --id ID to reconnect an existing session and --provider NAME.
Register and update-metadata accept --task, --project, --repository, --branch,
--cwd, and repeated --file flags. Omitted metadata fields are cleared.
`

type agentOptions struct {
	local       bool
	all         bool
	action      string
	id          string
	name        string
	provider    string
	state       string
	archiveDays *int
	deleteDays  *int
	metadata    agents.Metadata
}

func agentArguments(flags *flag.FlagSet, args []string) (*agentOptions, []string, error) {
	options := &agentOptions{action: "list"}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		options.action = args[0]
		args = args[1:]
	}
	switch options.action {
	case "list":
		flags.BoolVar(&options.local, "local", false, "list only local sessions without network requests")
		flags.BoolVar(&options.all, "all", false, "include archived sessions")
		return options, args, nil
	case "retention":
		return options, args, nil
	case "set-retention":
		flags.Func("archive-days", "archive offline sessions after this many days", func(value string) error {
			days, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("archive days must be an integer: %w", err)
			}
			options.archiveDays = &days
			return nil
		})
		flags.Func("delete-days", "delete offline sessions after this many days since last seen", func(value string) error {
			days, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("delete days must be an integer: %w", err)
			}
			options.deleteDays = &days
			return nil
		})
		return options, args, nil
	case "register":
		flags.StringVar(&options.name, "name", "", "agent display name")
		flags.StringVar(&options.provider, "provider", "", "agent provider")
	case "get", "heartbeat", "disconnect", "update-metadata":
	case "set-status":
		flags.StringVar(&options.state, "state", "", "online, busy, idle, or offline")
	default:
		return nil, nil, fmt.Errorf("unknown agent action %q; run agent-relay agents help", options.action)
	}
	flags.StringVar(&options.id, "id", "", "existing agent ID")
	if options.action == "register" || options.action == "update-metadata" {
		flags.StringVar(&options.metadata.Task, "task", "", "current task")
		flags.StringVar(&options.metadata.Project, "project", "", "project name")
		flags.StringVar(&options.metadata.Repository, "repository", "", "repository identifier")
		flags.StringVar(&options.metadata.Branch, "branch", "", "branch name")
		flags.StringVar(&options.metadata.Cwd, "cwd", "", "local working directory")
		flags.Func("file", "related file (repeatable)", func(value string) error {
			options.metadata.Files = append(options.metadata.Files, value)
			return nil
		})
	}
	return options, args, nil
}

func runAgents(ctx context.Context, store *storage.Store, registry *agents.Registry, options *agentOptions, output io.Writer) error {
	if options.action == "list" {
		return listAgents(ctx, registry, options.all, output)
	}
	if options.action == "retention" {
		policy, err := store.RetentionPolicy(ctx)
		if err != nil {
			return err
		}
		return printRetentionPolicy(output, policy)
	}
	if options.action == "set-retention" {
		if options.archiveDays == nil && options.deleteDays == nil {
			return errors.New("at least one of --archive-days or --delete-days is required")
		}
		policy, err := store.UpdateRetentionPolicy(ctx, options.archiveDays, options.deleteDays)
		if err != nil {
			return err
		}
		return printRetentionPolicy(output, policy)
	}
	if options.action != "register" && options.id == "" {
		return errors.New("--id is required")
	}
	var agent agents.Agent
	var err error
	switch options.action {
	case "register":
		agent, err = registry.Register(ctx, agents.Registration{ID: options.id, DisplayName: options.name, Provider: options.provider, Metadata: options.metadata})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(output, agent.ID)
		return err
	case "get":
		agent, err = registry.Get(ctx, options.id)
	case "heartbeat":
		agent, err = registry.Heartbeat(ctx, options.id)
	case "set-status":
		agent, err = registry.UpdateStatus(ctx, options.id, agents.Status(options.state))
	case "update-metadata":
		agent, err = registry.UpdateMetadata(ctx, options.id, options.metadata)
	case "disconnect":
		return registry.Disconnect(ctx, options.id)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(agent)
}

func printRetentionPolicy(output io.Writer, policy agents.RetentionPolicy) error {
	if _, err := fmt.Fprintln(output, "Retention policy"); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "  Archive after: %d days\n  Delete after: %d days\n  Measured from last seen. Deletion also removes Relay history. Changes apply within one minute while daemon runs.\n", policy.ArchiveAfterDays, policy.DeleteAfterDays)
	return err
}

func listAgents(ctx context.Context, registry *agents.Registry, includeArchived bool, output io.Writer) error {
	localAgents, err := registry.List(ctx)
	if err != nil {
		return err
	}
	visibleAgents := make([]agents.Agent, 0, len(localAgents))
	for _, agent := range localAgents {
		if !includeArchived && agent.Archived {
			continue
		}
		visibleAgents = append(visibleAgents, agent)
	}
	if len(visibleAgents) == 0 {
		message := "No local agents."
		if len(localAgents) > 0 && !includeArchived {
			message = "No active agents. Use --all to include archived sessions."
		}
		_, err := fmt.Fprintln(output, message)
		return err
	}
	sort.SliceStable(visibleAgents, func(left, right int) bool {
		leftActive := visibleAgents[left].Status != agents.Offline
		rightActive := visibleAgents[right].Status != agents.Offline
		if leftActive != rightActive {
			return leftActive
		}
		if !visibleAgents[left].LastSeenAt.Equal(visibleAgents[right].LastSeenAt) {
			return visibleAgents[left].LastSeenAt.After(visibleAgents[right].LastSeenAt)
		}
		return visibleAgents[left].ID < visibleAgents[right].ID
	})
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tAGENT\tSTATUS\tLAST SEEN\tTASK"); err != nil {
		return err
	}
	for _, agent := range visibleAgents {
		status := string(agent.Status)
		if agent.Archived {
			status += " (archived)"
		}
		if _, err := fmt.Fprintf(writer, "%s\t%q\t%s\t%s\t%q\n", agent.ID, agent.DisplayName, status, formatRelativeTime(agent.LastSeenAt), agent.Task); err != nil {
			return err
		}
	}
	return writer.Flush()
}
