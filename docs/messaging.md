# Local conversation and messaging APIs

`internal/messaging` defines the data types, UUIDv7 ID helper, validation, and lifecycle rules. `internal/storage.Store` implements persistence. These APIs perform no network requests and expose no HTTP or MCP endpoints.

## Conversations and identity

Use `messaging.NewID("conv")` and `messaging.NewID("msg")` for new conversation and message IDs. Retain the original message ID and immutable fields on every retry.

`CreateConversation(ctx, conversation)` takes an ID, local agent ID, remote agent ID, and creation time. The local agent must belong to the database's local node. The remote ID does not require registration in the local agents table. A conversation has exactly two distinct, immutable participants. The store initializes `UpdatedAt` to `CreatedAt`.

Create the conversation before inserting its messages. Conversation creation is separate from message insertion and is not idempotent. For an existing conversation, use `GetConversation` and verify the participants. Multiple conversations between the same pair are allowed.

`GetConversation(ctx, id)` returns its metadata. `ListConversations(ctx, agentID)` returns conversations containing that participant, ordered by creation time and then ID. `ConversationHistory(ctx, id)` returns all messages, including failed and expired items. Missing conversations return `messaging.ErrNotFound`; existing empty conversations return an empty slice.

## Insertion and receipt

| API | Behavior |
| --- | --- |
| `SaveMessage(ctx, message, now)` | Persists an outgoing message from the conversation's local agent with status `created`. |
| `ReceiveMessage(ctx, message, now)` | Persists an incoming message addressed to the conversation's local agent with status `delivered`, receipt and delivery timestamps, and a processed-message record. |
| `GetMessage(ctx, id)` | Looks up one message by its globally unique ID. |
| `ProcessedMessage(ctx, id)` | Returns its committed incoming-processing timestamp, or nil if no receipt exists. |

Both insertion methods return `(savedMessage, inserted, error)`. `inserted` is true only for a newly committed message. An exact retry returns the current stored record and false, preserving read state and lifecycle timestamps. Reusing an ID for different immutable content returns `messaging.ErrConflict`. IDs are unique across all conversations in the database.

Insertion validates the participant pair, message type, nonblank text, creation time, response link, and expiration. The supplied status and receipt/read/answer timestamps are ignored; the store owns those fields. `ExpiresAt`, `ReplyTo`, and all message content are immutable.

Incoming message insertion, processed-message insertion, question-answer updates, and conversation updates share one SQLite transaction. Success is returned only after commit. The transport acknowledges delivery only after `ReceiveRemote` returns a nil error, including on an exact retry. A failure or canceled context must not produce an acknowledgment. The processed-message table records durable receipt processing, not whether an LLM has read or answered a message.

SQLite uses immediate write transactions to serialize concurrent retries, including across separate store connections. History sorts by UTC creation timestamp with nanosecond precision, then message ID. Conversation `UpdatedAt` tracks the greatest creation timestamp observed and never moves backward on late delivery.

## Inbox and read state

`ListInbox(ctx, agentID, includeRead)` returns received messages addressed to that local agent. By default it includes unread messages and unanswered questions in `delivered` or `pending` state, even if those questions have been read. `includeRead = true` returns all received items, including read, answered, and expired items. Outgoing messages never enter the inbox.

Listing does not mark anything read. `MarkMessageRead(ctx, agentID, messageID, now)` verifies the recipient, records the first read timestamp, and moves a delivered question to `pending`. Repeated reads preserve the first timestamp. It does not delete messages. An unread expired or answered message remains visible until read.

## Responses and status

Responses have type `response` and a required `ReplyTo` question ID. They must reverse the original question's sender and recipient and use the same conversation. Non-response messages cannot carry `ReplyTo`.

Saving or receiving the first response marks its question `answered` in the same transaction. This also works when the sender has not yet recorded a delivery acknowledgment. An exact response retry succeeds; a distinct second response is rejected. Expired questions reject new responses, including when the expiration sweep has not yet run. An outgoing answer marks the local question answered when saved; the response's own status tracks its eventual delivery separately.

`UpdateMessageStatus(ctx, messageID, next, now)` permits lifecycle transitions and sets the first delivery timestamp when entering `delivered`. Repeating the current status is a no-op. Normal question flow is `created → sending → delivered → pending → answered`. A created message may enter `pending_delivery` or `failed`; sending may fail or enter `pending_delivery`; failed and pending-delivery messages may retry through `sending`. Terminal states cannot reopen. Only questions use `pending`, `answered`, and `expired`.

Callers cannot set `answered` or `expired` through the generic status API. Responses and expiration own those transitions. Invalid transitions return `messaging.ErrTransition`.

## Expiration and integration boundary

Questions default to expiration 24 hours after creation. To use a configured lifetime, supply `ExpiresAt = CreatedAt + lifetime` when creating the question and retain that deadline on retries. The transport service applies `messages.request_expiration_hours` when queueing questions.

`ExpireRequests(ctx, now)` persistently expires unanswered questions whose deadline is at or before `now`, including unsent and failed requests. It is idempotent and does not alter answered questions or ordinary messages. The low-level local receipt API can persist past-deadline questions as expired. The network receipt API rejects new expired questions; exact duplicates still return their original receipt. The daemon runs expiration each second, and CLI message reads run a sweep before returning. Internal callers should also sweep before reads when current expiration state is required. Reads do not run a hidden sweep.

The [delivery service](delivery.md) now provides explicit peer trust checks, routing, delivery acknowledgments, retries, daemon expiration, and CLI messaging. Responses now use the same durable outbox and a dedicated HTTP endpoint, with automatic return routing through `messages respond`. MCP remains future work. These internal methods are not an authorization boundary; transport and MCP must authenticate callers and enforce access before invoking them.

## Schema and verification

Migration 3 adds `conversations`, `messages`, and `processed_messages`, with foreign keys, participant and type constraints, immutable-content triggers, and history/inbox/expiration indexes. Existing node and agent records survive the upgrade.

Unit tests cover validation, lifecycle transitions, UUID generation, migration upgrades, participant constraints, deterministic ordering, unread/pending filtering, response linkage, retry conflicts, concurrent duplicate insertion, canceled operations, transaction rollback, expiration boundaries, and restart persistence. The full repository passes formatting, vet, tests, build, and race tests. Tests use local SQLite databases and existing localhost HTTP fixtures; no paid APIs are called.
