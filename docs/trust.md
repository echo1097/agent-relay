# Peer trust

Relay stores peer decisions in SQLite (`peer_trust`). Starting with v0.1.4, production discovery automatically trusts compatible Relay nodes verified at addresses supplied by the connected Tailscale client. Each node is bound to a stable Tailscale device identity. Failed probes, local nodes, development mode, and devices outside the current Tailscale peer list do not receive automatic trust. Existing blocks and different device bindings are preserved.

| State | Discovery | Messages, questions, responses |
| --- | --- | --- |
| unknown | Visible | Rejected |
| trusted | Visible | Allowed from the bound device |
| blocked | Visible | Rejected |

Hello, health, and public agent listing remain discovery endpoints. They do not expose inboxes or conversation history. Trust controls message communication, including replies and duplicate deliveries.

## Commands

```sh
agent-relay peers
agent-relay trust NODE_ID
agent-relay trust-state NODE_ID
agent-relay block NODE_ID
agent-relay untrust NODE_ID
agent-relay trust-state
```

Commands accept `--home PATH` before or after the peer argument. A unique discovered or saved peer name can replace `NODE_ID`. Ambiguous names and raw IP addresses are rejected. A full node ID can be blocked while the peer is offline. Trusting requires a reachable, compatible hello response matching that ID. The local node cannot be trusted, blocked, or reset as a peer.

`trust` without a peer lists decisions. `untrust` resets a decision to unknown. Decisions survive restarts and take effect on subsequent requests and delivery attempts without restarting the daemon. Trust changes are logged without message bodies. Trust is directional: run `trust` on both machines for bidirectional communication.

## Device verification

Production trust pins the Relay node ID to the stable Tailscale device ID returned by the local Tailscale CLI. Incoming requests must supply exactly one `X-Agent-Relay-Node-ID` header. The receiver checks SQLite, resolves the bound Tailscale device through current local Tailscale status, and compares its address with the actual TCP source. Forwarding headers have no authority. A different device cannot gain access by claiming a trusted Relay ID or inheriting its old IP address. Trust follows the original device when its IP changes.

Outgoing attempts resolve the bound Tailscale device and verify the destination Relay hello before sending message content. A changed Relay identity is rejected. Missing device information or unavailable Tailscale fails closed. Each attempt, including verification, has a five-second deadline.

This relies on Tailscale's authenticated device network and local ownership of Relay's database. It does not authenticate individual processes on the same trusted computer. Application signatures, passwords, OAuth, JWT login, and cloud identities are not part of V0.1.

Explicit development mode uses a saved loopback address plus Relay ID for isolated tests. It cannot authenticate individual local processes. Development bindings are rejected in production, and production bindings are rejected in development.

## Existing configuration

`[[trusted_peers]]` remains supported as an optional address and port hint for enrollment, including nondefault ports and loopback tests. Its name is retained for compatibility, but it no longer grants permission. Existing installations automatically enroll verified tailnet peers after upgrading and restarting the daemon. Both computers need this version to avoid manual trust on the older side. Discovery supplies the route for normal same-port Tailscale installations, so configuration is not required.

Start both daemons before discovery so their hello endpoints are available. Rebuild and restart existing daemons to load this implementation. Later trust changes do not require a restart. Node identities, agents, conversations, and existing messages are preserved by the migration.

## Rejections and diagnostics

Unknown or blocked incoming messages return HTTP 403 with `NODE_NOT_TRUSTED` and a reason. Missing or mismatched sender identities are rejected. State is checked again inside the message transaction, so a block completed while a body is being read prevents persistence, including duplicate acknowledgments.

Local queue requests reject unknown and blocked peers. Pending deliveries recheck trust before each attempt. Revoked trust and remote `NODE_NOT_TRUSTED` rejections become permanent delivery failures; trusting later does not automatically resend failed work. Already committed messages remain in history. `messages get --id MESSAGE_ID` includes outbox attempts and `Delivery.LastError`, preserving the remote rejection reason.

`status`, `peers`, and `doctor` show SQLite trust states separately from discovery reachability. Unknown and intentionally blocked peers are not connectivity failures. `doctor` includes enrollment guidance; a reachable peer does not imply reciprocal trust.

## Verification

The automated tests cover persistent trust transitions, discovery preserving decisions, source identity spoofing, Tailscale address reassignment, network mode isolation, unavailable identity verification, changed destination identities, unknown and blocked requests on both delivery endpoints, duplicate rejection after revocation, revocation during body reading, queued delivery revocation, CLI enrollment and inspection, ambiguous names, and offline blocking.

On September 6, 2026, isolated daemons on machine A and machine B exchanged a question and response over the actual Tailscale network on port 47839. Unknown and blocked incoming requests returned HTTP 403 `NODE_NOT_TRUSTED`; resetting and restoring trust took effect without restart. A follow-up delivered after trust restoration, SQLite inspection showed the stable Tailscale device binding, and doctor passed. Both temporary daemons were stopped. Existing machine identities, data, and daemons were preserved. No model-provider APIs were used.

## Automatic trust and DND

Automatic trust is always enabled in production. It grants messaging access to reachable, verified Relay devices visible through Tailscale. `block NODE_ID` is the persistent opt-out for that Relay node. `untrust NODE_ID` resets the decision to unknown, which discovery can trust again. A block applies to the Relay node ID; use Tailscale access controls to deny a device regardless of its Relay identity. A different device cannot automatically take over an existing node binding; explicit `trust NODE_ID` is required after reviewing that change.

`agent-relay dnd` toggles receiving new messages and questions. SQLite migration 6 stores this setting, and the receive transaction checks it before saving a request. Replies to existing outgoing questions are allowed. DND returns HTTP 403 with `DO_NOT_DISTURB`; senders record a permanent rejection and must send again when the recipient is available. No restart is required to toggle DND. Existing queued inbox items are retained.
