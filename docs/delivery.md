# Peer message delivery

`POST /v1/messages` delivers regular messages and questions to a registered local agent. It creates the two-participant conversation on first receipt. SQLite commits the conversation, peer association, immutable message, and processed-message marker before returning HTTP 200. Exact retries return the same acknowledgment, even after reading or expiration. Conflicting IDs or participants return 409. Registered offline agents still receive durable inbox entries.

The local `messages send` command persists a message and its outbox entry atomically, then returns immediately. A running daemon picks it up within approximately 250 ms when the worker is idle. The command also works while the daemon is stopped. It never calls an AI provider. `messages get`, `inbox`, and `history` inspect local storage. These commands currently return internal Go field names; the network uses the versioned JSON representation below.

## Temporary explicit trust

Configure each node with its peer's stable Relay node ID, Tailscale IPv4 address, and listening port:

```toml
[[trusted_peers]]
node_id = "node_019a84fc-1b72-7000-8000-000000000001"
address = "100.64.0.2"
port = 47832

[messages]
request_expiration_hours = 24
retry_interval_seconds = 900
```

An empty allowlist rejects all message traffic. The receiver checks the claimed node ID against the connection's actual source IP. Forwarding headers are ignored. The sender binds its connection to its configured local listening IP and sends only to configured peer addresses. No DNS, HTTP proxy, or redirect routing is used. A peer acknowledgment must name the expected node and message.

This is temporary address-pinned trust over Tailscale's authenticated network, with stable node IDs stored alongside conversations. The first exchange pins the remote agent ID to the trusted node; another trusted node cannot take over that agent or conversation. Changing a peer's address requires a config change and daemon restart. Discovery does not grant trust or change message routes. Full trust commands, blocking management, and Tailscale identity lookup remain deferred. Existing public hello and agent-list endpoints retain their discovery behavior.

Development mode accepts loopback addresses only. Loopback trust is for testing: other local processes can claim configured node IDs. It does not authenticate individual local processes. Production accepts only Tailscale IPv4 peer addresses. Do not put a forwarding proxy between nodes.

## Wire format

Requests require `Content-Type: application/json` and `X-Agent-Relay-Node-ID`. The optional `X-Agent-Relay-Protocol-Version` header must be `1`. The body must include version 1:

```json
{
  "protocol_version": 1,
  "id": "msg_019a8618-4110-7000-8000-000000000001",
  "conversation_id": "conv_019a8612-ab11-7000-8000-000000000001",
  "sender_agent_id": "agent_019a8502-3f22-7000-8000-000000000001",
  "recipient_agent_id": "agent_019a8502-3f22-7000-8000-000000000002",
  "type": "question",
  "created_at": "2026-09-07T03:00:00Z",
  "expires_at": "2026-09-08T03:00:00Z",
  "content": { "text": "Did refresh validation change?" }
}
```

IDs must be canonical prefixed UUIDv7 values. Questions require an explicit deadline; the outbound service supplies the configured default. Ordinary messages omit `expires_at`. Text is limited to 64 KiB, and encoded requests to 512 KiB. Unknown fields, multiple JSON values, invalid participants, unsupported versions, and response messages are rejected. Response-specific APIs and MCP are not implemented in this chunk.

Acknowledgment:

```json
{
  "protocol_version": 1,
  "node_id": "node_019a84fc-1b72-7000-8000-000000000001",
  "message_id": "msg_019a8618-4110-7000-8000-000000000001",
  "status": "delivered",
  "received_at": "2026-09-07T03:00:01Z"
}
```

The acknowledgment means durable receipt, not that the agent read or answered the message. New expired questions return 410 without creating an inbox item. Other errors include 403 `NODE_NOT_TRUSTED`, 404 `AGENT_NOT_FOUND`, 409 `MESSAGE_CONFLICT`, and the existing versioned validation/storage/timeout errors. A failed transaction never yields a delivery acknowledgment.

## Retry and expiration behavior

The initial attempt runs as soon as the daemon finds queued work. After temporary failures, wait 5 seconds, 15 seconds, 30 seconds, 1 minute, then 5 minutes between attempts. Further attempts use `messages.retry_interval_seconds` (default 900, minimum 300). Delays are measured from completion of the preceding attempt. The single worker processes up to 100 due items per batch; a busy queue can delay attempts.

Connection failures, request timeouts, HTTP 408/429/5xx, truncated responses, and invalid acknowledgments leave the message `pending_delivery`. Other HTTP rejections and removed trust mark it `failed` without automatic retries. Each attempt has a five-second client timeout and cannot run past its persisted delivery deadline. The HTTP server also bounds body reading and request processing. Response bodies and headers are bounded, and redirects are rejected.

The outbox persists attempts, next attempt time, deadline, and a generic failure reason. Restart resumes due work, including an interrupted `sending` attempt. Lost acknowledgments can cause redelivery, but cannot duplicate inbox entries. Successful acknowledgments set `delivered` and record the sender's delivery timestamp.

Questions expire at their original deadline, including while waiting for delivery. The daemon sweeps once a second and CLI reads sweep before returning. Ordinary messages retain the existing message lifecycle: their outbox deadline uses the configured request lifetime, and undelivered ordinary messages become `failed` when that deadline passes. Delivered ordinary messages remain delivered. Expiration preserves history and never deletes messages.

## Two-terminal procedure

Build from the repository root with `source .venv/bin/activate` and `go build -o bin/agent-relay ./cmd/agent-relay`. Use two fresh directories, one per terminal. In terminal A:

```sh
relayHome=/tmp/relay-demo-a
agentA=$(./bin/agent-relay agents register --home "$relayHome" --name agent-a)
./bin/agent-relay agents get --home "$relayHome" --id "$agentA"
```

In terminal B:

```sh
relayHome=/tmp/relay-demo-b
agentB=$(./bin/agent-relay agents register --home "$relayHome" --name agent-b)
./bin/agent-relay agents get --home "$relayHome" --id "$agentB"
```

Record both `node_id` values and agent IDs. Write each directory's `config.toml`. For A, use port 47931 and B's node ID with peer port 47932. For B, use port 47932 and A's node ID with peer port 47931:

```toml
[network]
development = true
bind_address = "127.0.0.1"
port = 47931

[[trusted_peers]]
node_id = "REPLACE_WITH_OTHER_NODE_ID"
address = "127.0.0.1"
port = 47932
```

In each terminal, start that directory's daemon:

```sh
./bin/agent-relay daemon --home "$relayHome" &
relayPid=$!
```

In terminal A, substitute B's IDs and queue a question:

```sh
./bin/agent-relay messages send --home "$relayHome" \
  --from "$agentA" --to AGENT_B_ID --peer NODE_B_ID \
  --type question --text "Did you change refresh token validation?"
./bin/agent-relay messages get --home "$relayHome" --id MESSAGE_ID
```

Get should change from `created` or `sending` to `delivered`. In terminal B:

```sh
./bin/agent-relay messages inbox --home "$relayHome" --agent "$agentB"
./bin/agent-relay messages send --home "$relayHome" \
  --from "$agentB" --to AGENT_A_ID --peer NODE_A_ID \
  --conversation CONVERSATION_ID --text "I am checking the validation change."
```

Check A's inbox. Stop B, send another message from A, and verify A reports `pending_delivery`. Restart B and verify eventual `delivered` and exactly one new inbox entry. Restart either daemon to verify history remains available. Finally, in both terminals:

```sh
kill -TERM "$relayPid"
wait "$relayPid"
```

Automated localhost coverage lives in `internal/daemon/delivery_test.go`. It includes bidirectional delivery, immutable duplicate acknowledgments, restart recovery, missing recipients, expiration, lost acknowledgments, and terminal failures. Transport unit tests cover request timeouts, retry delays, response limits, and rejected redirects.

## Recorded verification

On September 6, 2026, two foreground daemon processes were exercised in separate terminal sessions at `127.0.0.1:47931` and `127.0.0.1:47932`, using separate application directories and databases. The CLI procedure verified:

- A's question became `delivered` and appeared in B's persisted inbox.
- B's regular message reached A in the same conversation after restarting B.
- A's follow-up while B was stopped became `pending_delivery` and was automatically delivered at the scheduled retry after B restarted.
- B's history contained exactly three ordered messages, including one copy of the retried follow-up.
- After stopping both daemons, a fresh CLI process read the same three messages from B's database.

Both test daemons were stopped. This validates localhost delivery; it is not a live two-machine Tailscale test.
