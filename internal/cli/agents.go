package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"agent-relay/internal/agents"
)

const agentUsage = `Usage: agent-relay agents [action] [options]

Actions:
  list                       List all local agents (default)
  get --id ID                Show one agent as JSON
  register --name NAME       Register a session and print its ID
  heartbeat --id ID          Refresh presence
  set-status --id ID --state online|busy|idle|offline
  update-metadata --id ID    Replace all metadata fields
  disconnect --id ID         Mark a session offline immediately

Every action accepts --home PATH.
Register also accepts --id ID to reconnect an existing session and --provider NAME.
Register and update-metadata accept --task, --project, --repository, --branch,
--cwd, and repeated --file flags. Omitted metadata fields are cleared.
`

type agentOptions struct {
	action   string
	id       string
	name     string
	provider string
	state    string
	metadata agents.Metadata
}

func agentArguments(flags *flag.FlagSet, args []string) (*agentOptions, []string, error) {
	options := &agentOptions{action: "list"}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		options.action = args[0]
		args = args[1:]
	}
	switch options.action {
	case "list":
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

func runAgents(ctx context.Context, registry *agents.Registry, options *agentOptions, output io.Writer) error {
	if options.action == "list" {
		return listAgents(ctx, registry, output)
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

func listAgents(ctx context.Context, registry *agents.Registry, output io.Writer) error {
	localAgents, err := registry.List(ctx)
	if err != nil {
		return err
	}
	if len(localAgents) == 0 {
		_, err := fmt.Fprintln(output, "No local agents.")
		return err
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tAGENT\tSTATUS\tTASK"); err != nil {
		return err
	}
	for _, agent := range localAgents {
		if _, err := fmt.Fprintf(writer, "%s\t%q\t%s\t%q\n", agent.ID, agent.DisplayName, agent.Status, agent.Task); err != nil {
			return err
		}
	}
	return writer.Flush()
}
