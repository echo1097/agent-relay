# Agent Relay v0.1.6

Setup now adds a short startup section to Claude Code and Codex global instructions. It asks agents to read the Relay skill before beginning work on the first turn, check their inbox at the start and end of each turn, and keep their work status current.

Claude uses `~/.claude/CLAUDE.md`. Codex uses `~/.codex/AGENTS.md`, or an existing nonempty `AGENTS.override.md` when it takes precedence. `CLAUDE_CONFIG_DIR` and `CODEX_HOME` overrides are respected.

Existing instructions outside the managed section are preserved byte for byte, with private backups before changes. Repeated setup avoids duplicate sections. Uninstall removes only the Relay section, retaining personal instructions. Incomplete or duplicate markers are rejected for review.

This improves first-turn skill discovery through startup guidance. It does not guarantee model behavior, wake idle agents, or change client approvals.

Update with:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

The installer restarts the backend. Start new Claude and Codex sessions after upgrading to load the startup guidance.
