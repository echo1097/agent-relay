# Agent Relay v0.1.4

Devices on your tailnet now pair automatically. Production discovery verifies each Relay node against a visible Tailscale device and saves its device binding. Manual pairing is no longer required when both computers run this version. Explicitly blocked node IDs stay blocked, and existing bindings cannot be replaced automatically by another device.

Added `agent-relay dnd`: run it once to disable new incoming messages and questions, and again to resume. It takes effect immediately and survives restarts. Outgoing messages, discovery, existing inbox items, and replies to your outgoing questions remain available. Rejected senders see `DO_NOT_DISTURB` and must resend after DND is turned off.

The installer and diagnostics now explain automatic pairing. `agent-relay status` shows DND state. SQLite migration 6 saves the new setting while preserving existing data.

Update both computers:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

The installer restarts the backend. Reconnect Codex or Claude after upgrading. Use `agent-relay block NODE_ID` to deny a node, or Tailscale access controls to restrict an entire device. Resetting a node with `untrust` allows automatic discovery to trust it again.
