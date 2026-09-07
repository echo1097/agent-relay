# Agent Relay v0.1.1

This patch release includes the V0.1 audit fixes and compiled binaries for macOS and Linux on arm64 and amd64.

- Discover local and remote agents with `agent-relay agents`; use `--local` for local-only inspection. Status now includes remote agents.
- Read unread messages and pending questions with `agent-relay inbox`, and recent conversations with `agent-relay conversations`.
- Service installation refuses unowned or symlinked private binaries and verifies the running node identity before reporting success.
- Production discovery verifies connection sources against Tailscale, including requests for health and public agent metadata.
- Added missing lifecycle events and updated testing documentation. The completed PRD was removed; the audit and operational guides remain.

Install or update as your normal login user with Tailscale connected:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

The installer verifies SHA-256, preserves existing identities and data, updates and starts the service, configures detected Codex and Claude Code clients, and runs installation diagnostics. No Python, Node, Docker, or Go is required. Reconnect MCP clients after updating.

Validation passed: full tests, vet, build, race tests, four compiled targets, real macOS/Linux service lifecycle checks, and a two-Mac Tailscale MCP conversation with durable replies, restart recovery, follow-ups, and retained histories.

The two-machine MCP verification used scripted replies. A live Codex-to-Claude exchange using each agent's own coding context remains an acceptance check. Agents must poll their inboxes while active; Relay does not wake inactive models or generate answers. macOS requires a desktop login; Linux requires a systemd user manager. Explicit peer trust is required on both computers.

See the repository's V0.1 audit and acceptance guide for evidence and exact test steps.
