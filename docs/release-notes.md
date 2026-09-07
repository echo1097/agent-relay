Compiled Agent Relay binaries for macOS and Linux, on arm64 and amd64.

Install as your normal login user with Tailscale already installed and connected:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

The installer verifies SHA-256, initializes local storage, starts a login service, configures detected Codex and Claude Code clients, and checks installation health. No Python, Node, Docker, or Go is needed.

This is an early release. Reconnect MCP clients to register agents. Install on another tailnet device and explicitly trust the verified peer on both devices before exchanging messages. There is no automatic trust enrollment. macOS requires a desktop login; Linux requires a systemd user manager. See the repository's installation and operational documentation for current limits and recovery.
