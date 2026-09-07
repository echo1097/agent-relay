# Local agents and presence

`internal/agents` is the local API boundary. Construct a registry with the SQLite store, local node ID, and an `agents.Options` value. `OfflineAfter` is required; the CLI supplies `presence.offline_after_seconds` from configuration. `Now` is an optional clock for deterministic tests, and `Logger` is an optional structured logger.

```go
registry, err := agents.New(store, node.ID, agents.Options{
    OfflineAfter: 30 * time.Second,
    Logger: logger,
})
```

Handle constructor errors before using the registry. The store and local node are initialized through `storage.Open` and `store.Node` as in the CLI.

## API behavior

| Method | Behavior |
| --- | --- |
| `Register(ctx, Registration)` | Creates an online session with an `agent_` UUIDv7 ID. |
| `Get(ctx, agentID)` | Returns the local agent after expiring stale presence. |
| `List(ctx)` | Returns all local agents, including offline records, in registration order. |
| `Heartbeat(ctx, agentID)` | Refreshes last-seen time, retaining online, busy, or idle status. Revives an offline or timed-out agent as online. |
| `UpdateStatus(ctx, agentID, status)` | Sets online, busy, idle, or offline and refreshes last-seen time. |
| `UpdateMetadata(ctx, agentID, metadata)` | Replaces all metadata, refreshes last-seen time, and revives offline presence. |
| `Disconnect(ctx, agentID)` | Marks the agent offline immediately. |
| `Expire(ctx)` | Persists offline status for agents at or beyond the configured timeout. |
| `OfflineAll(ctx)` | Marks local agents offline during daemon shutdown without changing last-seen times. |

Each method accepts a context and returns errors. Missing IDs, including IDs belonging to another node, return `agents.ErrNotFound`. The registry has a small storage interface implemented by `storage.Store`; it does not depend on CLI, transport, or MCP code.

## Identity and updates

Every registration without an ID creates a distinct session. Matching names, providers, or repositories do not imply the same session.

To reconnect a saved session, pass its existing ID in `Registration.ID`. This updates the name, provider, and complete metadata, sets online status, and refreshes last-seen time. The ID and original registration timestamp remain unchanged. Unknown supplied IDs are rejected rather than inserted. Callers should save the ID returned from their first registration and use it on subsequent calls.

Metadata consists of task, project, repository, branch, cwd, and optional files. Replacement clears omitted fields; status and heartbeat operations leave metadata intact. Files and working directories are stored locally. No metadata is transmitted, and logs contain agent IDs or counts rather than metadata contents.

## Presence

The PRD defaults are a client heartbeat every 10 seconds and offline after 30 seconds. Heartbeats must come from the caller; the daemon does not keep abandoned agents alive on their behalf.

A timestamp at or before `now - offline timeout` expires. Timeout changes status only and preserves the actual last-seen timestamp. Last-seen writes cannot move backward if overlapping requests reach SQLite out of order. SQLite timestamps use fixed-width UTC text for accurate ordering.

The daemon performs a sweep at startup and every second. Consequently background expiration may appear up to one sweep interval after the threshold. Registry reads also sweep, so inspection handles elapsed time even when the daemon was stopped or crashed. Graceful daemon shutdown marks all local agents offline. A subsequent heartbeat or reconnect makes them online again.

Busy and idle are explicit caller-supplied states. Automatic idle detection is deferred because heartbeat activity alone does not establish whether an agent is working. There is no agent deletion or automatic history cleanup in this chunk.

## Persistence

Migration 2 adds the agents table and a presence index without changing migration 1. It includes a node foreign key and constraints for display names and status values. Registration, reconnection, and updates use atomic SQLite statements. Heartbeats and status updates do not replace metadata, which avoids lost metadata when calls overlap.

Tests cover registration, same-name sessions, reconnect updates, metadata replacement, validation, missing IDs, node scoping, heartbeat and timeout boundaries, disconnect, concurrent updates, database reopen, migration from the foundation schema, and daemon expiration/shutdown.
