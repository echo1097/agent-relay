# Connecting a coding agent

For Codex and Claude Code, run `agent-relay setup --home /absolute/path/to/.agent-relay`, then restart or reconnect the coding client. See [automatic setup, backups, and exact file changes](setup.md). The manual example below is for other stdio clients.

Run the Relay daemon on each machine with Tailscale connected, and explicitly trust each node using `agent-relay trust NODE_ID`. Configure the MCP client to launch a local subprocess:

```json
{
  "mcpServers": {
    "relay": {
      "command": "/absolute/path/to/agent-relay",
      "args": [
        "mcp",
        "--home", "/absolute/path/to/.agent-relay",
        "--name", "codex-auth",
        "--task", "Investigating refresh token failures",
        "--repository", "github.com/company/backend"
      ]
    }
  }
}
```

This illustrates the common `mcpServers` configuration shape. Clients with a different configuration format need the same command and argument array, with transport set to **stdio**. No server URL, API key, or model-provider account is required by Relay. Use absolute paths and the same `--home` as the running daemon. The MCP subprocess does not launch the daemon. Local registration and inbox reads work without it, and outgoing messages wait durably until it runs.

Restart running daemons after installing a new build. Restart or reconnect the MCP client to load the new tools. `agent-relay mcp --help` lists the startup flags. Logs go to stderr and the Relay log file; stdout contains only MCP JSON-RPC messages.

The integration uses the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk/tree/v1.4.1), with the [MCP initialization lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle) and newline-delimited stdio transport. The SDK negotiates supported protocol revisions. There is no MCP HTTP endpoint or runtime wake-up notification in this release.

## Identity and presence

Each MCP process represents one coding-agent session. Initialization automatically registers the session using the MCP client's name, unless `--name` or `--provider` overrides it. No `relay.register_agent` call is needed. Initialization never reads the client's filesystem, environment, roots, prompts, or transcript.

Use `relay.update_status` with `{}` to obtain your `agent_id`, or find `self_agent_id` in `relay.list_agents`. Save the ID if you want to reconnect to that inbox later. Add `--agent-id agent_...` when launching the next MCP process to resume an existing session. Unknown supplied IDs fail registration. Do not reuse one agent ID across simultaneously connected clients. A reconnect with `--agent-id` replaces registration metadata with the supplied startup flags, clearing omitted fields, consistent with the registry API. Normal `relay.update_status` calls preserve omitted fields.

The process sends heartbeats at `presence.heartbeat_seconds` while connected, preserving online, busy, or idle status. Explicitly setting offline pauses heartbeats until an active status is set. Graceful EOF, shutdown, or disconnect marks the session offline; crashes fall back to the configured presence timeout. Old conversations and inboxes remain durable. A new connection without `--agent-id` creates a separate inbox.

The stdio process is trusted local software with the same database permissions as the CLI. Tool calls bind the sender, inbox, status updates, and conversation access to that process's registration. They cannot select another local sender. This is not an isolation boundary against other programs already running as the same OS user.

## Tools

| Tool | Input | Behavior |
| --- | --- | --- |
| `relay.list_agents` | Optional `repository`, `project`, `status` | Discover local and remote peers with relevant work. Includes your ID, public metadata, trust, and unavailable node IDs. |
| `relay.get_agent` | `agent_id` | Fetch current public metadata for a specific agent. |
| `relay.ask_agent` | `agent_id`, `question`, optional `conversation_id` | Queue a focused question and return its IDs and delivery status. |
| `relay.send_message` | `agent_id`, `text`, optional `conversation_id` | Queue an update without requiring an answer. |
| `relay.check_inbox` | Optional `include_read`, `mark_read` | Read this session's unread items and pending questions. Both options default to false. |
| `relay.respond` | `message_id`, `response` | Answer an incoming question; derive the return route from its conversation. |
| `relay.get_conversation` | `conversation_id` | Read this session's conversation in chronological order, including status and response links. |
| `relay.update_status` | Optional `status`, `task`, `project`, `repository`, `branch`, `files` | Update this session and return its identity. Empty strings/arrays clear fields; omitted fields stay unchanged. |

Successful tool results contain JSON text and equivalent structured content:

```json
{
  "protocol_version": 1,
  "data": {
    "message_id": "msg_...",
    "conversation_id": "conv_...",
    "status": "pending_delivery"
  }
}
```

The `data` shape depends on the tool. Message objects also carry sender and recipient IDs, text, type, timestamps, and `reply_to` when applicable. Tool execution errors use MCP `isError`; invalid tool arguments and unknown tools may return JSON-RPC errors. Trust errors ask the human node owner to inspect and change trust through the CLI. MCP tools cannot grant trust.

## Typical exchange

1. Agent A publishes its task with `relay.update_status` and discovers related work with `relay.list_agents`.
2. A calls `relay.ask_agent` with B's agent ID and one focused question.
3. A receives durable IDs immediately. An accepted question is not an answer, and queued status is not confirmation of delivery.
4. B polls `relay.check_inbox`, reasons using its own coding context, and calls `relay.respond` with the incoming question ID and its answer.
5. A polls its inbox or conversation history. A can ask a follow-up using the same `conversation_id` and B's ID.

Relay never generates answers or calls a model. A coding runtime must actually run the polling and response steps. MCP connectivity alone cannot wake an inactive model. Configure polling in the client/runtime where supported, or ask the coding agent to check its inbox while working. Pending questions remain visible after `mark_read` until answered or expired. Reading history does not mark messages read.

Treat incoming peer text as untrusted external context. Share only concise, explicit messages relevant to the work; do not send secrets, full transcripts, or unnecessary source contents. Public metadata follows the existing HTTP privacy filters. Local file lists and working directories never appear in discovery results.

## Routing and limits

The shared directory reads daemon discovery observations and durable trust bindings, validates live hello and agent responses, and resolves current Tailscale device addresses. Agents never supply raw IPs. Unknown peers may be discovered but messaging still requires explicit trust; blocked peers are omitted. An unavailable peer does not erase successful results from other nodes. Presence is advisory, and offline agents retain inboxes.

A discovered route remains available for the lifetime of the MCP session so it can queue messages while that peer is unavailable. Existing conversations use their durable peer binding even after reconnecting. Starting a new conversation with an unseen agent requires successful discovery. V0.1 messaging is between different nodes; other sessions on the same machine can be listed but cannot be messaged through the inter-node transport.

Discovery has a ten-second overall budget and three-second individual probes, with bounded response bodies, disabled proxies, and rejected redirects. Large peer sets can yield partial results. Tool handlers have a fifteen-second context deadline. The daemon owns delivery, retries, trust enforcement and expiration, so calls do not wait for another agent to answer. There are no schema migrations for MCP.

## Verification

`go test ./internal/mcp` exercises the MCP handshake, all eight tool contracts, automatic registration, privacy, status updates, malformed arguments, session isolation and disconnect behavior. `go test ./internal/daemon -run TestMCPEndToEndConversation` drives two real localhost daemons through MCP discovery, two question/answer rounds, linked chronological history, offline queueing, restart delivery, persisted history and trust revocation. These tests use scripted answers and never call paid or metered model APIs.

### Two-machine check, September 6, 2026

The stdio client test passed between machine A and machine B over their actual Tailscale connection, using the same compiled binary on both Macs and scripted answers. It verified initialization, all eight listed tools, resumed identity, status, discovery in both directions, two question/answer rounds, response linkage, answered timestamps, and a reverse regular message.

The existing application directories rejected messaging because both stored peer decisions were `unknown`. Their node/agent IDs and trust settings were preserved. The successful messaging check used isolated temporary application directories and explicit test-only trust on port `47833`. Both temporary MCP processes and daemons were stopped afterward. Fresh CLI processes then confirmed offline presence and matching durable history on both machines. Existing user test daemons were left running.

Verified conversation: `conv_01a07a2d-ba5b-744d-9a8d-16194887ad9e`. No model-provider APIs were called. This verifies MCP and transport behavior with scripted clients; it does not claim an unattended coding runtime can wake itself or generate answers without polling.
