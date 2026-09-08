# Session retention

The daemon archives offline sessions after 7 days and permanently deletes them after 30 days by default. Both ages are measured from the session's last-seen timestamp. Thirty days means 30 consecutive 24-hour days, not a calendar month or 30 additional days after archiving.

Archiving keeps the session, its inbox, and its conversation history available. Reconnecting with the existing `--agent-id`, sending a heartbeat, or setting an active status restores the session and refreshes its last-seen time. A connected MCP process continues to send heartbeats even when its model is not working, so it does not age out merely because its chat is idle.

## Find a session

Normal session lists hide archived records, show active sessions before offline ones, and sort each group by most recent last seen. The `LAST SEEN` column shows a relative age; an older peer that does not report a timestamp displays `unknown`.

```sh
agent-relay agents
agent-relay agents --local
agent-relay agents --all
agent-relay agents --local --all
```

`--all` includes archived sessions on local and reachable remote computers, marked `offline (archived)`. Recent offline sessions remain visible by default. Looking up an archived session by ID still works. MCP clients can pass `include_archived: true` to `relay.list_agents`; the default hides archived sessions.

## Change the ages

Inspect the saved policy:

```sh
agent-relay agents retention
```

Change both ages, or supply just the setting you want to change:

```sh
agent-relay agents set-retention --archive-days 7 --delete-days 30
agent-relay agents set-retention --archive-days 14
agent-relay agents set-retention --delete-days 60
```

Archive days must be at least 1. Delete days must be greater than archive days and at most 106751. Invalid updates leave both existing settings unchanged. These commands also accept `--home PATH` for an alternate Relay data directory.

Settings are stored in that computer's Relay database and apply to all its local sessions. They do not change other computers' policies. The daemon checks at startup and once a minute, reloading the saved settings on each pass. No daemon restart is needed for a policy change. While the daemon is stopped, automatic archiving and deletion are paused. Shortening an age makes already-old sessions eligible on the next pass. Lengthening the archive age restores records that no longer meet it, but cannot recover deleted data.

## What deletion removes

Deleting a session removes its local agent record, its conversations and messages, delivery queue entries, processed-message markers, and conversation-to-peer associations in one transaction. This includes unread messages and pending deliveries belonging to that session. New incoming messages do not refresh the session's last-seen timestamp or postpone deletion.

Other local sessions and their histories remain intact. Remote computers keep their own copies according to their own retention policies. Computer identity, peer trust, and remote-agent ownership bindings are preserved.

A deleted session cannot be resumed with its old ID, and later deliveries addressed to it are rejected. Reconnect without `--agent-id` to create a new session and inbox. Deletion has no built-in undo. A failed cleanup transaction rolls back the session and all its history together.

An outgoing request already in flight when cleanup runs may still reach the remote computer. Its completion does not recreate local history or produce a local delivered event for the deleted record. Pending items removed before sending are skipped.

Installing the updated executable still requires updating and restarting the backend service, and reconnecting MCP clients. See [development and service update instructions](development.md).
