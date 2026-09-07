# Agent Relay V0.1 Product Requirements Document

## Implementation status

Last reviewed: September 6, 2026, against implementation commit `ae0a5f6`.

The repository currently implements the project foundation, local agent registry and presence, and the HTTP protocol foundation. The full V0.1 product and its end-to-end acceptance criteria are not complete. Requirements below remain the target unless explicitly identified as current implementation behavior.

| Area | Current status |
| --- | --- |
| Phase 1: Foundation | Implemented. |
| Phase 2: Local agent registry | Implemented. Busy and idle are explicitly set by callers; automatic idle detection is not implemented. |
| Phase 3: Inter-node transport | HTTP foundation implemented. Message serialization and message transport remain deferred. |
| Phase 4: Tailscale integration | Not implemented. |
| Phases 5 and 6: Messaging and responses | Not implemented. |
| Phase 7: Trust | Peer trust management and enforcement are not implemented. |
| Phase 8: MCP | Not implemented. |
| Phases 9 and 10: Services and installer | Not implemented. |
| Phase 11: Hardening | Some foundational validation, timeouts, and shutdown handling exist; the full phase remains pending. |

### Built: project foundation

* Go module and `cmd/agent-relay/main.go`, producing the `agent-relay` binary.
* CLI commands for help, version, daemon operation, status, and local agents.
* TOML configuration with defaults, validation, and an optional configuration file.
* Structured text logging to stderr and the application log file.
* Application directories under `~/.agent-relay`, with a `--home` override.
* SQLite initialization with WAL, foreign keys, full synchronization, and a busy timeout.
* Ordered transactional migrations, failure rollback, and rejection of newer database schemas.
* Persistent UUIDv7 node identity and a saved node display name.
* A foreground daemon with per-directory process locking and signal-driven shutdown.
* Basic local daemon/database status. OS service installation is still pending.

### Built: local agents and presence

* Internal Go APIs for registration, lookup, listing, heartbeat, metadata replacement, status updates, disconnect, and presence expiration.
* Persistent `agent_` UUIDv7 IDs, node association, display name, provider, registration time, and last-seen time.
* Local metadata for task, project, repository, branch, cwd, and files.
* The four states: online, busy, idle, and offline.
* Each registration without an ID creates a separate session, even when names match. Reconnecting with an existing ID updates that session and preserves its ID and original registration time. Unknown supplied IDs are rejected.
* Heartbeats preserve active online, busy, or idle status. An offline or timed-out agent returns online when it sends a heartbeat or reconnects.
* Default offline timeout of 30 seconds. The daemon checks every second; registry reads also expire stale presence. Timeout preserves the actual last-seen time.
* Immediate offline status on disconnect and offline marking during graceful daemon shutdown.
* SQLite persistence through migration 2, including an agents table, status constraints, and a presence index.
* Local CLI actions: `agents list`, `get`, `register`, `heartbeat`, `set-status`, `update-metadata`, and `disconnect`.

Registration and metadata replacement clear omitted metadata fields. Callers must send their own heartbeats; the daemon does not generate heartbeats for inactive agents. Agent commands use SQLite directly and work without a running daemon. See [local agent API behavior](docs/agents.md).

### Built: HTTP protocol foundation

* A daemon HTTP server using Go's standard library.
* Configurable `network.bind_address` and `network.port`, currently defaulting to `127.0.0.1:47832`.
* `GET /v1/health` for process liveness, `GET /v1/hello` for public node and version information, and `GET /v1/agents` for local agents only.
* Protocol version 1 in application response bodies and the `X-Agent-Relay-Protocol-Version` header. The `/v1/` URL supplies the request version; an optional request header is checked for compatibility.
* Validated public response objects, consistent JSON application errors, and rejection of unsupported methods, request bodies, and query parameters.
* Separate public agent types containing only ID, display name, provider, status, task, project, repository, and branch. Working directories and file lists are never serialized by HTTP handlers.
* Conservative filtering of unsafe metadata values and normalization of HTTP(S) repository URLs. Free-form metadata must still be suitable for publication; filtering is not a general detector for every possible secret.
* Five-second request contexts and header-read timeouts, ten-second read/write timeouts, a thirty-second idle timeout, and a 16 KiB header limit.
* Graceful HTTP shutdown with up to five seconds for active requests before remaining connections are closed, followed by local presence cleanup.

Loopback binding is an interim development default. Tailscale-only production binding, peer authentication, and trust enforcement remain requirements for later phases. No agent mutation endpoint, outgoing peer transport, discovery, conversation, message, or MCP implementation exists yet. See [HTTP protocol behavior and examples](docs/protocol.md).

### Current schema and verification

Migration 1 creates `nodes` and the singleton `local_node` reference. Migration 2 creates `agents`. The migration runner records applied versions in `schema_migrations`. Conversation, message, and processed-message tables described later in this PRD are not implemented yet.

The implementation has passed `go fmt ./...`, `go vet ./...`, `go test ./...`, `go build ./...`, and `go test -race ./...`. Tests cover foundation persistence and migration behavior, registration and updates, heartbeat and timeout boundaries, restart persistence, localhost HTTP endpoints, public metadata filtering, protocol errors, request deadlines, and graceful shutdown. Manual CLI and curl checks also passed, and test daemons were stopped afterward.

The two-node agent messaging test in section 56 has not been implemented or passed. The project is not yet a complete V0.1 release.

---

## 1. Product Overview

### Product name

Agent Relay

### One-line description

Agent Relay is an open-source, local-first communication layer that allows AI coding agents running on different computers within the same Tailscale network to discover each other, ask questions, respond using their own local context, and maintain short peer-to-peer conversations.

### Core problem

Software engineers increasingly run AI coding agents such as Claude Code and Codex alongside their development environments.

Within a team, multiple engineers may simultaneously have agents working on the same repository, subsystem, bug, feature, or adjacent areas of the codebase.

These agents currently operate in isolation.

For example:

* Engineer A has Agent A investigating an authentication bug.
* Engineer B has Agent B refactoring authentication middleware.
* Agent A encounters behavior it does not understand.
* Agent B may already know the answer because of the work it is performing.
* Today, the engineers themselves must notice the overlap, communicate manually, and copy information between agent sessions.

Agent Relay should allow Agent A to discover Agent B, recognize that Agent B is working on related code, ask Agent B a question, receive an answer generated using Agent B's existing context, and continue the conversation if needed.

The human engineers should not need to copy and paste messages between agents.

---

# 2. V0.1 Scope

Agent Relay V0.1 should solve one problem extremely well:

> Two agents on two different computers within the same Tailscale tailnet can communicate bidirectionally.

The supported communication model is:

```text
Agent A
   ↓
Agent Relay A
   ↓
Tailscale
   ↓
Agent Relay B
   ↓
Agent B
   ↓
Agent Relay B
   ↓
Tailscale
   ↓
Agent Relay A
   ↓
Agent A
```

V0.1 should support:

* peer discovery
* agent discovery
* agent registration
* agent presence
* direct Agent A → Agent B messaging
* questions that expect responses
* Agent B → Agent A responses
* follow-up messages within the same conversation
* durable message delivery
* local inboxes
* conversation history
* basic peer trust
* MCP integration
* human-readable CLI tooling
* simple installation
* daemon operation
* Tailscale-only network communication

V0.1 should not attempt to solve:

* multi-agent group conversations
* broadcasts
* autonomous task delegation
* remote shell execution
* remote file editing
* shared filesystems
* shared memory
* vector databases
* automatic context synchronization
* full conversation transfer
* arbitrary internet communication
* cloud accounts
* centralized control planes
* team dashboards
* GitHub issue assignment
* automatic merging
* automatic conflict resolution
* agent orchestration
* agent swarms

The primary design philosophy is:

> Build a reliable communication primitive before building distributed agent orchestration.

---

# 3. Product Principles

Agent Relay should follow these principles throughout implementation.

## 3.1 Local-first

Agent Relay should operate without a hosted Agent Relay service.

The product should continue working if the public internet is unavailable, provided the participating machines can still communicate through Tailscale.

## 3.2 Peer-to-peer

Communication should flow directly between Agent Relay nodes.

There should be no central Agent Relay message broker.

## 3.3 Tailscale-native

Agent Relay assumes the user already has Tailscale installed and connected.

Tailscale is responsible for:

* encrypted transport
* NAT traversal
* machine connectivity
* tailnet membership

Agent Relay should not reimplement networking problems Tailscale already solves.

## 3.4 Agent-agnostic

Agent Relay should not be tightly coupled to Codex, Claude Code, or a specific model provider.

The primary agent-facing interface should be MCP.

Agent-specific adapters may exist later.

## 3.5 Minimal context sharing

Agents should not automatically send their full prompts, transcripts, token context, environment, or source files to other agents.

Agent Relay should exchange only explicit structured metadata and messages.

## 3.6 Human-debuggable

The protocol should be easy to inspect.

Use simple JSON over HTTP for V0.1.

Avoid introducing gRPC, protobuf, custom binary protocols, or distributed databases.

## 3.7 One binary

The user should install one executable:

```text
agent-relay
```

That executable should provide:

* daemon functionality
* CLI functionality
* MCP server functionality
* setup utilities
* diagnostics

---

# 4. Primary User Story

Two engineers are working in the same repository.

Engineer A is running Codex.

Engineer B is running Claude Code.

Agent A encounters a problem involving authentication sessions.

Agent Relay knows that Agent B is currently working on authentication middleware.

Agent A calls:

```text
relay.list_agents
```

Agent Relay returns Agent B.

Agent A calls:

```text
relay.ask_agent
```

with:

```text
target: agent-b
question: Did you change refresh token validation? I'm getting a 401 after refresh.
```

Agent Relay A sends the request over Tailscale to Agent Relay B.

Agent Relay B stores the request in Agent B's inbox.

Agent B receives the request through MCP.

Agent B reasons using its existing local coding session and responds:

```text
Yes. I changed refresh validation to require a session_id claim. Check auth/refresh.ts.
```

Agent Relay B sends the response back.

Agent A receives the answer.

Agent A may then ask:

```text
Was that change intentional, or can I remove the session_id requirement?
```

The follow-up is attached to the same Agent Relay conversation.

Agent B responds.

Both agents now share only the relevant information needed to coordinate their work.

---

# 5. System Architecture

Each participating machine runs one Agent Relay daemon.

Example:

```text
Machine A                                      Machine B

┌─────────────────────┐                       ┌─────────────────────┐
│      Agent A        │                       │      Agent B        │
│                     │                       │                     │
│ Codex / Claude /    │                       │ Codex / Claude /    │
│ other MCP client    │                       │ other MCP client    │
└─────────┬───────────┘                       └─────────┬───────────┘
          │                                             │
          │ MCP                                         │ MCP
          ▼                                             ▼
┌─────────────────────┐                       ┌─────────────────────┐
│ Agent Relay daemon  │                       │ Agent Relay daemon  │
│                     │                       │                     │
│ MCP server          │                       │ MCP server          │
│ Agent registry      │                       │ Agent registry      │
│ Peer manager        │                       │ Peer manager        │
│ Inbox               │                       │ Inbox               │
│ Conversation store  │                       │ Conversation store  │
│ SQLite              │                       │ SQLite              │
└─────────┬───────────┘                       └─────────┬───────────┘
          │                                             │
          └──────────── Tailscale network ──────────────┘
```

---

# 6. Technical Stack

Preferred implementation language:

```text
Go
```

Reasons:

* single compiled binary
* simple cross-compilation
* good networking ecosystem
* strong concurrency primitives
* straightforward HTTP servers
* easy SQLite integration
* good CLI tooling
* well suited for long-running daemons

Preferred storage:

```text
SQLite
```

Preferred transport:

```text
HTTP + JSON over Tailscale
```

Optional event transport:

```text
Server-Sent Events or WebSockets
```

V0.1 should not require an event socket if polling is sufficient.

Preferred agent protocol:

```text
MCP
```

---

# 7. Process Model

The `agent-relay` executable should support multiple modes.

```bash
agent-relay daemon
agent-relay status
agent-relay peers
agent-relay agents
agent-relay inbox
agent-relay conversations
agent-relay setup
agent-relay doctor
agent-relay trust
agent-relay version
```

The daemon should normally be launched automatically by the OS.

Users should not need to manually keep a terminal open.

---

# 8. Network Model

## 8.1 Preconditions

Agent Relay requires:

1. Tailscale is installed.
2. Tailscale is running.
3. The machine is connected to a tailnet.
4. At least one other Agent Relay node is reachable on the same tailnet.

Agent Relay should detect these conditions automatically.

## 8.2 Listening interface

Current implementation: the HTTP foundation defaults to loopback (`127.0.0.1`) and accepts a configured IP address. The Tailscale-only production behavior below is pending Phase 4.

The daemon should listen only on the machine's Tailscale network interface.

Example:

```text
100.75.42.18:47832
```

It must not listen publicly on:

```text
0.0.0.0
```

unless explicitly configured by the user.

Default port:

```text
47832
```

The port should be configurable.

## 8.3 Protocol prefix

All inter-node APIs should be namespaced:

```text
/v1/*
```

Example:

```text
GET /v1/hello
GET /v1/agents
POST /v1/messages
POST /v1/requests
POST /v1/responses
```

---

# 9. Node Identity

A node represents one computer running Agent Relay.

Each node should have a persistent Agent Relay node ID.

Example:

```text
node_019a84fc1b72
```

The node ID should be generated on first launch.

Recommended ID format:

```text
UUIDv7
```

Each node should expose:

```json
{
  "id": "node_019a84fc1b72",
  "name": "noah-macbook",
  "tailscale_ip": "100.75.42.18",
  "protocol_version": 1,
  "agent_relay_version": "0.1.0"
}
```

Display name should not be treated as the security identity.

---

# 10. Agent Identity

An agent represents one active coding-agent session.

Examples:

```text
Codex working on auth
Claude Code working on websocket tests
```

Each agent registration should receive a unique ID.

Example:

```text
agent_019a85023f22
```

An agent record should contain:

```json
{
  "id": "agent_019a85023f22",
  "display_name": "codex-auth",
  "provider": "codex",
  "node_id": "node_019a84fc1b72",
  "status": "busy",
  "registered_at": "2026-09-06T23:00:00Z",
  "last_seen_at": "2026-09-06T23:02:04Z"
}
```

---

# 11. Agent Metadata

Agents should be able to publish lightweight metadata so other agents can determine whether they are relevant.

Initial supported fields:

```json
{
  "task": "Investigating refresh token 401 failures",
  "project": "backend",
  "repository": "github.com/company/backend",
  "branch": "fix/refresh-token",
  "cwd": "/Users/noah/code/backend"
}
```

Optional:

```json
{
  "files": [
    "src/auth/refresh.ts",
    "src/auth/session.ts"
  ]
}
```

Important:

Absolute local paths should not be transmitted to peers by default.

The daemon may store:

```text
/Users/noah/code/backend
```

locally.

Peers should instead receive something normalized such as:

```text
repository: github.com/company/backend
branch: fix/refresh-token
```

---

# 12. Agent Registration

An MCP client registers itself with the local Agent Relay daemon.

Proposed MCP tool:

```text
relay.register_agent
```

Input:

```json
{
  "display_name": "codex-auth",
  "provider": "codex",
  "task": "Investigating refresh token 401 failures",
  "repository": "github.com/company/backend",
  "branch": "fix/refresh-token"
}
```

Output:

```json
{
  "agent_id": "agent_019a85023f22",
  "node_id": "node_019a84fc1b72"
}
```

Registration should either:

* be performed automatically by the MCP integration
* or be handled during MCP connection initialization

The model should not normally need to explicitly think about registration.

---

# 13. Presence

Agent Relay needs a lightweight presence mechanism.

Supported states:

```text
online
busy
idle
offline
```

A registered agent should send heartbeats to the local daemon.

Suggested values:

```text
heartbeat interval: 10 seconds
idle threshold: configurable
offline threshold: 30 seconds
```

When an agent disconnects gracefully, mark it offline immediately.

If heartbeats stop, mark it offline after the timeout.

Peer nodes should not consider presence data permanently authoritative.

Presence is ephemeral.

---

# 14. Peer Discovery

Agent Relay should automatically discover other Agent Relay nodes on the tailnet.

V0.1 discovery strategy:

1. Query the local Tailscale installation for visible tailnet peers.
2. Obtain their Tailscale IP addresses.
3. Probe the Agent Relay port.
4. Request:

```text
GET /v1/hello
```

5. If the response identifies a compatible Agent Relay node, register it as a peer.
6. Cache it locally.
7. Periodically refresh.

Suggested refresh interval:

```text
15 seconds
```

A peer should be considered unreachable after repeated failed probes.

Discovery should require no central registry.

---

# 15. Hello Endpoint

Endpoint:

```text
GET /v1/hello
```

Response:

```json
{
  "protocol": "agent-relay",
  "protocol_version": 1,
  "node": {
    "id": "node_019a84fc1b72",
    "name": "alex-macbook"
  },
  "version": "0.1.0"
}
```

The hello endpoint must not expose:

* conversation history
* filesystem paths
* message contents
* secrets
* environment variables

---

# 16. Agent Discovery

MCP tool:

```text
relay.list_agents
```

Optional filters:

```json
{
  "repository": "github.com/company/backend",
  "project": "backend",
  "status": "online"
}
```

Return:

```json
[
  {
    "id": "agent_019a85023f22",
    "display_name": "claude-auth",
    "node": "alex-macbook",
    "provider": "claude-code",
    "task": "Refactoring authentication middleware",
    "repository": "github.com/company/backend",
    "branch": "refactor/auth",
    "status": "busy"
  }
]
```

The requesting agent should never need to know a remote machine's raw Tailscale IP to communicate.

The Agent Relay daemon handles routing.

---

# 17. Core Communication Types

V0.1 should support two fundamental operations.

## 17.1 Message

A message does not inherently require a response.

Example:

```text
FYI, I renamed validateUser() to validateSession().
```

MCP tool:

```text
relay.send_message
```

## 17.2 Question

A question creates a request that expects a response.

Example:

```text
Did you change refresh token validation?
```

MCP tool:

```text
relay.ask_agent
```

This distinction must exist at the protocol layer.

---

# 18. Conversation Model

All exchanges belong to conversations.

Conversation ID:

```text
conv_019a8612ab11
```

A conversation contains exactly two participating agents in V0.1.

Example:

```text
agent_a
agent_b
```

A conversation contains ordered messages.

Example:

```text
Conversation conv_123

1. A → B
2. B → A
3. A → B
4. B → A
```

Group conversations are explicitly out of scope.

---

# 19. Conversation Creation

The first call to:

```text
relay.ask_agent
```

without a `conversation_id` creates a conversation.

Input:

```json
{
  "agent_id": "agent_b",
  "question": "Did you change refresh token validation?"
}
```

Return:

```json
{
  "conversation_id": "conv_019a8612ab11",
  "message_id": "msg_019a86184110",
  "status": "delivered"
}
```

A follow-up may include:

```json
{
  "agent_id": "agent_b",
  "conversation_id": "conv_019a8612ab11",
  "question": "Was that intentional?"
}
```

The daemon must verify that the provided target is actually a participant in that conversation.

---

# 20. Message Object

Canonical protocol representation:

```json
{
  "id": "msg_019a86184110",
  "conversation_id": "conv_019a8612ab11",
  "type": "question",
  "sender_agent_id": "agent_a",
  "recipient_agent_id": "agent_b",
  "created_at": "2026-09-06T23:10:04Z",
  "content": {
    "text": "Did you change refresh token validation?"
  }
}
```

Supported message types for V0.1:

```text
message
question
response
```

---

# 21. Request Lifecycle

A question should have a lifecycle.

Statuses:

```text
created
sending
delivered
pending
answered
failed
expired
```

Normal flow:

```text
created
   ↓
sending
   ↓
delivered
   ↓
pending
   ↓
answered
```

If the remote node cannot be reached:

```text
sending
   ↓
failed
```

If Agent B does not answer within the configured request lifetime:

```text
pending
   ↓
expired
```

Default request expiration:

```text
24 hours
```

This should be configurable.

---

# 22. Delivery Semantics

Agent Relay should implement at-least-once network delivery with idempotent processing.

Every message has a globally unique message ID.

If the sender retries a request because it did not receive confirmation, the receiver must not create duplicate messages.

Pseudo behavior:

```text
receive msg_123

if msg_123 already exists:
    return existing delivery acknowledgment

otherwise:
    store msg_123
    return acknowledgment
```

This is important for unreliable or temporarily disconnected peers.

---

# 23. Durable Inboxes

Incoming messages should be persisted before acknowledging delivery.

Correct order:

```text
receive request
      ↓
validate request
      ↓
write to SQLite
      ↓
commit transaction
      ↓
acknowledge sender
```

Do not acknowledge before persistence.

This prevents messages from disappearing if the daemon crashes.

---

# 24. Agent Inbox

Each local agent has an inbox.

MCP tool:

```text
relay.check_inbox
```

Input:

```json
{
  "agent_id": "agent_b"
}
```

Output:

```json
[
  {
    "message_id": "msg_019a86184110",
    "conversation_id": "conv_019a8612ab11",
    "type": "question",
    "from": {
      "agent_id": "agent_a",
      "display_name": "codex-auth",
      "node": "noah-macbook"
    },
    "text": "Did you change refresh token validation?",
    "received_at": "2026-09-06T23:10:04Z"
  }
]
```

Only unread or pending items should be returned by default.

Optional:

```json
{
  "include_read": true
}
```

---

# 25. Question Handling

When Agent B receives a question, it should use its current local coding context to reason about the answer.

Agent Relay itself should not generate an answer.

Agent Relay is responsible only for:

```text
transport
identity
persistence
routing
conversation state
```

The coding agent is responsible for:

```text
reasoning
answer generation
deciding whether it has enough information
```

Agent B may respond with:

```text
I don't know.
```

That is valid.

Agent Relay must not fabricate a response.

---

# 26. Respond Tool

MCP tool:

```text
relay.respond
```

Input:

```json
{
  "message_id": "msg_019a86184110",
  "response": "Yes. Refresh tokens now require the session_id claim."
}
```

Agent Relay determines:

* original sender
* conversation ID
* remote node
* recipient agent

The agent should not need to manually specify those again.

Return:

```json
{
  "message_id": "msg_019a862201fa",
  "conversation_id": "conv_019a8612ab11",
  "status": "delivered"
}
```

---

# 27. Getting Conversation History

MCP tool:

```text
relay.get_conversation
```

Input:

```json
{
  "conversation_id": "conv_019a8612ab11"
}
```

Output:

```json
{
  "id": "conv_019a8612ab11",
  "participants": [
    "agent_a",
    "agent_b"
  ],
  "messages": [
    {
      "sender": "agent_a",
      "type": "question",
      "text": "Did you change refresh token validation?"
    },
    {
      "sender": "agent_b",
      "type": "response",
      "text": "Yes. Refresh tokens now require the session_id claim."
    },
    {
      "sender": "agent_a",
      "type": "question",
      "text": "Was that intentional?"
    }
  ]
}
```

Conversations should be sorted chronologically.

---

# 28. Agent Wake-Up Problem

One significant technical constraint is that an inactive LLM agent may not automatically execute work simply because a network message arrived.

Agent Relay V0.1 must not pretend this problem does not exist.

The daemon can always receive and persist messages.

Whether Agent B immediately processes the message depends on the capabilities of the local agent runtime.

V0.1 should support two processing modes.

## Mode A: Polling

The agent periodically calls:

```text
relay.check_inbox
```

This is the guaranteed baseline.

## Mode B: Runtime notification

Where supported, Agent Relay may notify the local MCP client that new work exists.

This should be treated as an optimization.

Do not make V0.1 dependent on runtime wake-up APIs that do not exist consistently across agent platforms.

---

# 29. Suggested Agent Behavior

The MCP tool descriptions should teach participating agents when Agent Relay is useful.

For example, the description for `relay.list_agents` should tell the model:

```text
Use this tool when another active agent may have relevant information about the repository, feature, bug, subsystem, or files you are currently working on.
```

The description for `relay.ask_agent` should say:

```text
Use this tool to ask another agent a focused question when that agent's current work appears relevant. Prefer concise questions and do not send unnecessary private context.
```

The description for `relay.check_inbox` should say:

```text
Check for incoming questions or messages from other active agents. When a question is relevant and answerable from your current context, respond using relay.respond.
```

Good tool descriptions are part of product behavior.

---

# 30. MCP Tool Set for V0.1

Required tools:

```text
relay.list_agents
relay.get_agent
relay.ask_agent
relay.send_message
relay.check_inbox
relay.respond
relay.get_conversation
relay.update_status
```

Internal registration may be exposed as:

```text
relay.register_agent
```

but ideally happens automatically.

---

# 31. `relay.get_agent`

Input:

```json
{
  "agent_id": "agent_b"
}
```

Output:

```json
{
  "id": "agent_b",
  "display_name": "claude-auth",
  "provider": "claude-code",
  "status": "busy",
  "task": "Refactoring authentication middleware",
  "repository": "github.com/company/backend",
  "branch": "refactor/auth",
  "node": {
    "name": "alex-macbook"
  }
}
```

---

# 32. `relay.update_status`

Input:

```json
{
  "status": "busy",
  "task": "Debugging refresh token validation",
  "repository": "github.com/company/backend",
  "branch": "fix/refresh-token",
  "files": [
    "src/auth/refresh.ts"
  ]
}
```

This information should become visible to peers.

Agents should update task metadata whenever the nature of their work materially changes.

Do not require constant updates for every small action.

---

# 33. Trust Model

Membership in the same tailnet should not automatically imply unlimited Agent Relay access.

V0.1 should implement basic explicit trust.

Possible peer states:

```text
unknown
trusted
blocked
```

Default behavior for unknown nodes:

```text
visible through discovery
cannot send agent messages until trusted
```

Alternative stricter default:

```text
unknown nodes are hidden until trusted
```

For V0.1, use the first behavior unless implementation complexity makes it significantly harder.

CLI:

```bash
agent-relay trust alex-macbook
```

Optional:

```bash
agent-relay block alex-macbook
```

Trusted peer identity should be stored using stable node identity, not IP address.

---

# 34. Authentication

The daemon must ensure incoming network traffic originates from the Tailscale interface.

Do not accept external internet traffic.

Where practical, resolve incoming remote IPs against Tailscale peer information and verify that the node matches the expected known peer.

Application-level signing is not required for V0.1 unless the Tailscale identity mechanism proves insufficient during implementation.

Do not introduce:

```text
username/password authentication
OAuth
JWT login accounts
Agent Relay cloud identities
```

---

# 35. Privacy Rules

Agent Relay should never automatically expose:

```text
full agent transcript
system prompts
API keys
environment variables
filesystem contents
SSH keys
Git credentials
shell history
arbitrary files
complete model context
```

Agent metadata should be intentionally minimal.

Agent messages should contain only text explicitly sent by the agent.

---

# 36. SQLite Schema

Current implementation: only node identity and agent persistence are present, through migrations 1 and 2. The conversation and messaging tables below remain planned. See the implementation status section and [migration definitions](migrations/migrations.go).

A reasonable initial schema:

```sql
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    tailscale_ip TEXT,
    trust_state TEXT NOT NULL,
    last_seen_at TEXT
);

CREATE TABLE agents (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    provider TEXT,
    status TEXT NOT NULL,
    task TEXT,
    repository TEXT,
    branch TEXT,
    registered_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);

CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    local_agent_id TEXT NOT NULL,
    remote_agent_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    sender_agent_id TEXT NOT NULL,
    recipient_agent_id TEXT NOT NULL,
    type TEXT NOT NULL,
    text TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    delivered_at TEXT,
    read_at TEXT,
    answered_at TEXT
);

CREATE TABLE processed_messages (
    message_id TEXT PRIMARY KEY,
    processed_at TEXT NOT NULL
);
```

Migrations should be versioned.

Never mutate production schemas ad hoc.

---

# 37. Inter-Node HTTP API

Required endpoints:

```text
GET  /v1/hello
GET  /v1/agents

POST /v1/messages
POST /v1/responses

GET  /v1/health
```

Potential future endpoints should not be implemented prematurely.

---

# 38. `GET /v1/agents`

Return only agents on the current node.

Example:

```json
{
  "agents": [
    {
      "id": "agent_b",
      "display_name": "claude-auth",
      "provider": "claude-code",
      "status": "busy",
      "task": "Refactoring authentication middleware",
      "repository": "github.com/company/backend",
      "branch": "refactor/auth"
    }
  ]
}
```

---

# 39. `POST /v1/messages`

Request:

```json
{
  "protocol_version": 1,
  "message": {
    "id": "msg_123",
    "conversation_id": "conv_123",
    "type": "question",
    "sender": {
      "node_id": "node_a",
      "agent_id": "agent_a"
    },
    "recipient": {
      "node_id": "node_b",
      "agent_id": "agent_b"
    },
    "created_at": "2026-09-06T23:10:04Z",
    "content": {
      "text": "Did you change refresh token validation?"
    }
  }
}
```

Response:

```json
{
  "message_id": "msg_123",
  "status": "delivered"
}
```

---

# 40. Error Format

Use a consistent JSON error format.

Example:

```json
{
  "error": {
    "code": "AGENT_NOT_FOUND",
    "message": "The requested agent is no longer available."
  }
}
```

Possible V0.1 codes:

```text
INVALID_REQUEST
UNSUPPORTED_PROTOCOL
NODE_NOT_TRUSTED
AGENT_NOT_FOUND
AGENT_OFFLINE
CONVERSATION_NOT_FOUND
MESSAGE_NOT_FOUND
PEER_UNREACHABLE
DELIVERY_FAILED
INTERNAL_ERROR
```

---

# 41. Offline Behavior

If a peer node is temporarily offline:

For V0.1, the sender should retain the outgoing message locally.

Status:

```text
pending_delivery
```

The daemon may retry periodically.

Suggested retry schedule:

```text
immediate
5 seconds
15 seconds
30 seconds
1 minute
5 minutes
```

After that, continue at a slower configurable interval until expiration.

Do not block the agent indefinitely waiting on a network request.

---

# 42. Response Waiting

`relay.ask_agent` should not require the MCP call itself to remain open until the human-scale answer arrives.

Recommended behavior:

```text
ask_agent
      ↓
request accepted
      ↓
return conversation/message identifiers
```

Then the agent may poll:

```text
relay.get_conversation
```

or:

```text
relay.check_inbox
```

for the response.

If the local agent framework supports asynchronous notifications later, Agent Relay may surface responses immediately.

The network request itself should remain short-lived.

---

# 43. CLI UX

## Status

```bash
agent-relay status
```

Example:

```text
Agent Relay

Daemon
  Running

Node
  noah-macbook
  100.75.42.18

Local agents
  codex-auth        busy

Peers
  alex-macbook      online

Remote agents
  claude-auth       busy
```

## Agents

```bash
agent-relay agents
```

Example:

```text
AGENT          MACHINE          STATUS   TASK
codex-auth     noah-macbook     busy     Debugging refresh tokens
claude-auth    alex-macbook     busy     Refactoring auth middleware
```

## Inbox

```bash
agent-relay inbox
```

Example:

```text
1 pending question

From: alex-macbook/claude-auth
Conversation: conv_019a8612ab11

Did you change refresh token validation?
```

## Conversations

```bash
agent-relay conversations
```

Should display recent conversations.

---

# 44. Doctor Command

`agent-relay doctor` is required.

It should diagnose common setup failures.

Example:

```text
Agent Relay Doctor

✓ Agent Relay version 0.1.0
✓ Tailscale installed
✓ Tailscale daemon reachable
✓ Connected to tailnet
✓ Tailscale IP: 100.75.42.18
✓ Agent Relay daemon running
✓ Listening on 100.75.42.18:47832
✓ SQLite database writable
✓ MCP configuration detected
✓ 1 Agent Relay peer discovered
✓ Remote peer reachable
```

Failure example:

```text
✗ Tailscale is not connected

Run:
tailscale up
```

Diagnostics should always provide a useful remediation when possible.

---

# 45. Installation Experience

Canonical Unix installation:

```bash
curl -fsSL https://agentrelay.dev/install.sh | sh
```

Installer responsibilities:

1. Detect OS.
2. Detect architecture.
3. Download correct binary.
4. Verify checksum.
5. Install binary.
6. Detect Tailscale.
7. Confirm Tailscale is running.
8. Create local config directory.
9. Initialize database.
10. Install background daemon.
11. Start daemon.
12. Configure supported MCP clients.
13. Run `agent-relay doctor`.
14. Print success message.

Target output:

```text
Installing Agent Relay...

✓ macOS arm64 detected
✓ Agent Relay installed
✓ Tailscale connected
✓ Background daemon started
✓ Codex configured
✓ Claude Code configured
✓ Agent Relay ready

1 peer discovered.
```

---

# 46. Data Locations

Suggested Unix layout:

```text
~/.agent-relay/
```

Contents:

```text
~/.agent-relay/config.toml
~/.agent-relay/relay.db
~/.agent-relay/logs/
```

Binary:

```text
/usr/local/bin/agent-relay
```

or user-local equivalent.

---

# 47. Configuration

Current implementation also supports `network.bind_address`, defaulting to `127.0.0.1` while Tailscale integration is pending. The configuration file is optional. Network discovery and messaging settings are validated but do not activate features that have not been built.

Example:

```toml
[network]
port = 47832

[discovery]
interval_seconds = 15

[presence]
heartbeat_seconds = 10
offline_after_seconds = 30

[messages]
request_expiration_hours = 24

[logging]
level = "info"
```

The defaults should work without editing this file.

---

# 48. Logging

Logs should be structured internally but readable.

Required events:

```text
daemon started
Tailscale detected
peer discovered
peer lost
agent registered
agent offline
message queued
message delivered
message received
response delivered
trust changed
delivery failed
```

Never log:

```text
API keys
environment variables
credentials
full filesystem contents
```

Message body logging should be disabled by default.

Log metadata such as message ID instead.

---

# 49. Repository Structure

Recommended layout:

```text
agent-relay/
├── cmd/
│   └── agent-relay/
│       └── main.go
│
├── internal/
│   ├── daemon/
│   ├── tailscale/
│   ├── discovery/
│   ├── peers/
│   ├── agents/
│   ├── messaging/
│   ├── conversations/
│   ├── protocol/
│   ├── auth/
│   ├── storage/
│   ├── mcp/
│   ├── service/
│   └── config/
│
├── migrations/
│
├── installers/
│   ├── install.sh
│   └── install.ps1
│
├── docs/
│   ├── architecture.md
│   ├── protocol.md
│   ├── security.md
│   └── mcp.md
│
├── go.mod
├── go.sum
├── README.md
├── LICENSE
└── CONTRIBUTING.md
```

Avoid premature package fragmentation.

---

# 50. Internal Interfaces

Codex should implement clear internal boundaries.

Examples:

```go
type PeerDiscovery interface {
    Discover(ctx context.Context) ([]Peer, error)
}
```

```go
type MessageStore interface {
    SaveMessage(ctx context.Context, msg Message) error
    GetMessage(ctx context.Context, id string) (Message, error)
    ListInbox(ctx context.Context, agentID string) ([]Message, error)
}
```

```go
type Transport interface {
    Send(ctx context.Context, peer Peer, msg Message) error
}
```

```go
type AgentRegistry interface {
    Register(ctx context.Context, agent Agent) error
    Heartbeat(ctx context.Context, agentID string) error
    List(ctx context.Context) ([]Agent, error)
}
```

The networking, persistence, MCP, and business logic layers should not be tightly coupled.

---

# 51. Concurrency Requirements

The daemon must safely handle:

* peer discovery
* incoming HTTP requests
* outgoing messages
* message retries
* agent heartbeats
* MCP requests
* SQLite operations

Do not spawn unbounded goroutines.

Use contexts for cancellation.

All daemon-owned background loops should terminate cleanly during shutdown.

---

# 52. Graceful Shutdown

On daemon shutdown:

1. Stop accepting new MCP work.
2. Stop accepting new HTTP traffic.
3. Allow in-flight writes to finish.
4. Mark local agents offline if practical.
5. Close SQLite.
6. Exit.

Avoid database corruption during forced restarts.

---

# 53. Protocol Versioning

Every inter-node request should communicate protocol version.

Initial:

```text
1
```

If a peer uses an incompatible major protocol version:

```json
{
  "error": {
    "code": "UNSUPPORTED_PROTOCOL",
    "message": "Peer uses Agent Relay protocol version 2; this node supports version 1."
  }
}
```

Do not silently interpret incompatible requests.

---

# 54. Security Boundaries

Agent Relay V0.1 must not expose an endpoint capable of:

```text
running shell commands
reading arbitrary files
writing files
executing Git commands remotely
starting agents
stopping agents
installing software
running tests remotely
```

Agent communication is text-only.

This boundary should remain explicit.

---

# 55. Testing Strategy

## Unit tests

Required for:

* message validation
* conversation membership
* trust rules
* duplicate message handling
* state transitions
* timeout handling
* serialization
* SQLite queries

## Integration tests

Run two Agent Relay daemon instances using loopback addresses.

Simulate:

```text
Node A
Node B
```

Test:

```text
A discovers B
B discovers A
A discovers B's agent
A asks B
B receives question
B responds
A receives response
A sends follow-up
B receives follow-up
```

## Failure tests

Test:

```text
B disappears before delivery
B disappears after delivery
duplicate request received
daemon restart
SQLite restart
agent unregisters
invalid conversation ID
untrusted peer
malformed message
unsupported protocol version
```

---

# 56. Required End-to-End Test

There must be a single automated test representing the fundamental product behavior.

Pseudo-test:

```text
start node A
start node B

register Agent A on A
register Agent B on B

wait for peer discovery

Agent A calls list_agents

assert Agent B is returned

Agent A asks:
"Did you change refresh token validation?"

assert B inbox receives question

Agent B responds:
"Yes, I changed the validation logic."

assert A receives response

Agent A asks follow-up:
"Was session_id added?"

assert B receives follow-up in same conversation

Agent B responds:
"Yes."

assert conversation contains four ordered messages
```

If this test does not pass, the release should not ship.

---

# 57. MVP Acceptance Criteria

Agent Relay V0.1 is complete when all of the following are true.

### Installation

A user can install Agent Relay with one command on a supported system.

### Daemon

Agent Relay runs as a background service.

### Tailscale

The daemon detects the local Tailscale network and binds exclusively to it.

### Discovery

Two machines running Agent Relay automatically discover each other.

### Registration

An MCP-connected coding agent can appear in the local agent registry.

### Agent discovery

Agent A can see Agent B and its basic task metadata.

### Ask

Agent A can send Agent B a question.

### Delivery

The question is durably stored on Agent B's machine.

### Inbox

Agent B can retrieve the question through MCP.

### Response

Agent B can respond.

### Return delivery

Agent A receives Agent B's response.

### Follow-up

Agent A can send another message in the same conversation.

### Persistence

Restarting either daemon does not destroy previously stored conversations.

### Trust

An untrusted node cannot freely communicate with local agents.

### Diagnostics

`agent-relay doctor` identifies common installation and connectivity problems.

---

# 58. First Demo Scenario

The first public demo should involve exactly two machines.

Machine A:

```text
Engineer Noah
Codex
repository: backend
task: Debugging refresh token 401s
```

Machine B:

```text
Engineer Alex
Claude Code
repository: backend
task: Refactoring authentication middleware
```

On Machine A:

```text
User:
Figure out why refresh requests are suddenly returning 401s.
```

Codex investigates.

Codex queries:

```text
relay.list_agents
```

It sees:

```text
alex/claude-auth
Refactoring authentication middleware
```

Codex determines the work may be related.

Codex calls:

```text
relay.ask_agent(
  alex/claude-auth,
  "I'm investigating new 401 responses after token refresh. Did your auth changes affect refresh validation?"
)
```

Machine B receives the question.

Claude Code responds:

```text
Yes. I added validation requiring the session_id claim because refresh tokens now participate in session invalidation.
```

Machine A receives the response.

Codex now understands the behavioral change and continues debugging.

No human copied text between machines.

That is the V0.1 product.

---

# 59. Implementation Order

Codex should implement the project in this order.

## Phase 1: Foundation

Status: implemented. See the implementation status section for delivered behavior and verification.

Build:

```text
CLI skeleton
configuration
logging
SQLite setup
migration system
node identity
```

Do not implement MCP yet.

## Phase 2: Local agent registry

Status: implemented through internal Go APIs, SQLite persistence, and local CLI commands. Automatic idle detection remains deferred; idle is currently caller-controlled.

Build:

```text
agent registration
agent heartbeat
presence
agent listing
```

Test locally.

## Phase 3: Inter-node transport

Status: HTTP server, hello, health, agent listing, protocol versioning, and localhost integration tests are implemented. Message serialization is intentionally deferred with messaging. This phase does not yet provide agent-to-agent communication.

Build:

```text
HTTP server
/v1/hello
/v1/health
/v1/agents
message serialization
```

Test two daemon instances locally.

## Phase 4: Tailscale integration

Status: not started. This is the next planned implementation phase.

Build:

```text
detect Tailscale
obtain local Tailscale address
discover peers
probe Agent Relay nodes
```

Do not require actual Tailscale in unit tests.

Abstract the discovery provider.

## Phase 5: Messaging

Build:

```text
conversations
messages
durable inbox
POST /v1/messages
delivery acknowledgment
idempotency
```

## Phase 6: Responses

Build:

```text
question state
responses
conversation history
follow-ups
```

At this stage, the complete A ↔ B transport should work through CLI tests.

## Phase 7: Trust

Build:

```text
trusted peers
blocked peers
request enforcement
CLI trust commands
```

## Phase 8: MCP

Expose:

```text
relay.list_agents
relay.get_agent
relay.ask_agent
relay.send_message
relay.check_inbox
relay.respond
relay.get_conversation
relay.update_status
```

## Phase 9: Service installation

Implement:

```text
launchd
systemd user service
Windows service later if necessary
```

## Phase 10: Installer

Build:

```text
install.sh
binary download
checksum verification
daemon setup
MCP setup
doctor
```

## Phase 11: Hardening

Add:

```text
timeouts
retry logic
malformed request handling
protocol mismatch handling
database recovery
graceful shutdown
logging cleanup
```

---

# 60. Explicit Codex Constraints

When implementing this PRD, Codex should obey the following constraints.

Do not introduce:

```text
Docker
Redis
PostgreSQL
Kafka
NATS
RabbitMQ
gRPC
Kubernetes
cloud infrastructure
web dashboards
React
Electron
hosted authentication
central Agent Relay servers
```

unless a later PRD explicitly requires them.

Do not over-engineer abstractions for hypothetical future features.

Do not implement group messaging.

Do not implement agent task delegation.

Do not allow remote code execution.

Do not automatically transfer agent context.

Do not expose arbitrary files.

Do not create hidden external network dependencies.

Prefer standard-library Go functionality where reasonable.

Keep dependencies minimal.

Every network request should have a timeout.

Every database mutation should handle errors.

Every persistent schema change should use migrations.

Every externally exposed protocol object should be versioned.

Every incoming message should be validated.

Every message should have a stable unique ID.

Every message delivery should be idempotent.

---

# 61. Definition of Success

Agent Relay succeeds at V0.1 when this interaction feels normal:

```text
Agent A:
I'm confused about this authentication behavior.

Agent A:
Another agent is working on auth. I'll ask it.

Agent A → Agent B:
Did you change how refresh tokens are validated?

Agent B:
Yes. I added session ID validation for session invalidation.

Agent A:
Was that intentional?

Agent B:
Yes.

Agent A:
Understood. I'll preserve the requirement and fix the caller instead.
```

The engineers did not mediate the conversation.

The agents did not share their entire contexts.

No cloud service was involved.

No custom networking setup was required beyond the team's existing Tailscale network.

Agent Relay simply allowed two independently operating coding agents to communicate.
