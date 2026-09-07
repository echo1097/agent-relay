# Peer message delivery

`POST /v1/messages` delivers regular messages and questions, and `POST /v1/responses` delivers responses to a registered local agent. It creates the two-participant conversation on first receipt. SQLite commits the conversation, peer association, immutable message, and processed-message marker before returning HTTP 200. Exact retries return the same acknowledgment, even after reading or expiration. Conflicting IDs or participants return 409. Registered offline agents still receive durable inbox entries.

The local `messages send` command persists a message and its outbox entry atomically, then returns immediately. A running daemon picks it up within approximately 250 ms when the worker is idle. The command also works while the daemon is stopped. It never calls an AI provider. `messages get`, `inbox`, and `history` inspect local storage. These commands currently return internal Go field names; the network uses the versioned JSON representation below.

## Persistent explicit trust

Run `agent-relay trust NODE_ID` on both nodes before communicating. Unknown and blocked peers receive HTTP 403 `NODE_NOT_TRUSTED`. Trust decisions live in SQLite and bind stable Relay IDs to Tailscale device identities. They update without a daemon restart. Discovery never grants trust.

Existing `[[trusted_peers]]` entries provide optional route hints only. They no longer authorize communication. Start both daemons, then enroll each peer with the trust command. See [trust setup, enforcement, development mode, and upgrade behavior](trust.md).

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

IDs must be canonical prefixed UUIDv7 values. Questions require an explicit deadline; the outbound service supplies the configured default. Ordinary messages omit `expires_at`. Text is limited to 64 KiB, and encoded requests to 512 KiB. Responses use the same envelope with `type: "response"`, a required `reply_to` question ID, and no `expires_at`. Send them to `/v1/responses`; each endpoint rejects mismatched message types. Unknown fields, multiple JSON values, invalid participants, and unsupported versions are rejected. MCP is not implemented.

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

The acknowledgment means durable receipt, not that the agent read or answered the message. New expired questions return 410 without creating an inbox item. Other errors include 403 `NODE_NOT_TRUSTED`, 404 `AGENT_NOT_FOUND` (or `MESSAGE_NOT_FOUND` for a response with a missing original or recipient), 409 `MESSAGE_CONFLICT`, and the existing versioned validation/storage/timeout errors. A failed transaction never yields a delivery acknowledgment.

## Retry and expiration behavior

The initial attempt runs as soon as the daemon finds queued work. After temporary failures, wait 5 seconds, 15 seconds, 30 seconds, 1 minute, then 5 minutes between attempts. Further attempts use `messages.retry_interval_seconds` (default 900, minimum 300). Delays are measured from completion of the preceding attempt. The single worker processes up to 100 due items per batch; a busy queue can delay attempts.

Connection failures, request timeouts, HTTP 408/429/5xx, truncated responses, and invalid acknowledgments leave the message `pending_delivery`. Other HTTP rejections and removed trust mark it `failed` without automatic retries. Each attempt has a five-second client timeout and cannot run past its persisted delivery deadline. The HTTP server also bounds body reading and request processing. Response bodies and headers are bounded, and redirects are rejected.

The outbox persists attempts, next attempt time, deadline, and a generic failure reason. Restart resumes due work, including an interrupted `sending` attempt. Lost acknowledgments can cause redelivery, but cannot duplicate inbox entries. Successful acknowledgments set `delivered` and record the sender's delivery timestamp.

Questions expire at their original deadline, including while waiting for delivery. The daemon sweeps once a second and CLI reads sweep before returning. Ordinary messages and responses retain the existing message lifecycle: their outbox deadline uses the configured request lifetime, and undelivered ordinary messages and responses become `failed` when that deadline passes. Delivered ordinary messages and responses remain delivered. Expiration preserves history and never deletes messages.

## Responses and follow-ups

Use `messages respond --from LOCAL_AGENT_ID --id QUESTION_ID --text TEXT`. The service looks up the received question and derives its original sender, conversation ID, and pinned peer node. Only the original recipient can answer; the target must be a received question. The response and its outbox entry commit atomically with the local question's `answered` state and `AnsweredAt`. The original sender marks its copy answered when it receives the response. The response's own delivery status remains independently visible.

A distinct second response is rejected, even if its text matches. Exact delivery retries return the original acknowledgment and preserve both receipt and answered timestamps. Invalid originals, wrong participants or conversations, expired questions, and conflicting message IDs cannot append history. Rejections roll back the entire transaction. A response cannot answer another response or a regular message.

Use `messages send --conversation EXISTING_ID --type question` for a follow-up. A supplied conversation ID must already exist locally, and its two participants and peer binding must match. Omit the ID to start a new conversation. The first remote receipt creates the matching conversation on the other node. New messages advance `UpdatedAt` to the maximum message creation time; exact retries do not change it. History sorts by creation time and message ID.

Responses have their own outbox deadline using the configured request lifetime. A receiving node still rejects a new response once the original question expires. A locally queued answer remains recorded even if delivery later fails; inspect the response status to distinguish local answering from return delivery.

## Two-terminal and two-machine procedure

Follow the [complete conversation test](conversation-test.md) for installation on another machine, explicit trust setup, both question/answer rounds, duplicate checks, persistence checks, and shutdown. It includes localhost and Tailscale configurations.

Automated localhost coverage lives in `internal/daemon/conversation_test.go` and `internal/daemon/delivery_test.go`. The PRD section 56 test is `TestAgentRelayEndToEndConversation`. It exercises real HTTP discovery probes and agent listing with a fake tailnet, then the full four-message conversation. Additional tests cover response validation, participant checks, lost acknowledgments and database restart recovery. No MCP or paid APIs are used.

## Recorded verification

On September 6, 2026, two foreground daemon processes were exercised in separate terminal sessions at `127.0.0.1:47931` and `127.0.0.1:47932`, using separate application directories and databases. The CLI procedure verified:

- A's question became `delivered` and appeared in B's persisted inbox.
- B's regular message reached A in the same conversation after restarting B.
- A's follow-up while B was stopped became `pending_delivery` and was automatically delivered at the scheduled retry after B restarted.
- B's history contained exactly three ordered messages, including one copy of the retried follow-up.
- After stopping both daemons, a fresh CLI process read the same three messages from B's database.

Both test daemons were stopped. This validates localhost delivery; it is not a live two-machine Tailscale test.

The full four-message CLI conversation also passed on September 6, 2026 using two localhost daemon processes and fresh databases. Both nodes preserved matching history after shutdown; duplicate responses were rejected. Formatting, vet, all tests, builds, and race checks passed. The complete procedure is recorded in [conversation-test.md](conversation-test.md).
