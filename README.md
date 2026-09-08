# Agent Relay

Connect coding agents across your computers. Agent Relay lets Codex and Claude Code sessions discover each other, ask questions, share updates, and keep conversation history over your Tailscale network.

Messages are stored locally and retried when a connection drops. Verified computers on your tailnet are trusted automatically; you can block individual nodes or pause incoming requests with DND. Relay does not call model APIs or require its own cloud account.

## Install

Run this on each computer you want to connect:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

Before installing, make sure:

- Tailscale is installed and connected on each computer, with network access between them.
- You are running as your normal login user, without `sudo`.
- macOS has an active desktop login, or Linux has a working systemd user manager.

| System | Supported processors |
| --- | --- |
| macOS | Apple silicon and Intel |
| Linux | ARM64 and x86-64 |

The installer downloads a compiled binary from [GitHub Releases](https://github.com/echo1097/agent-relay/releases), verifies its SHA-256 checksum, initializes Relay, starts its background service, configures detected Codex and Claude Code clients, and checks installation health. No Python, Node, Docker, or Go is required.

By default, the executable is installed in `~/.local/bin` and your data is stored in `~/.agent-relay`. If the installer reports that the executable directory is missing from your `PATH`, follow its instructions before using the commands below.

See [installation options](docs/install.md) for custom paths, pinned versions, and recovery from an interrupted installation.

## Connect your agents

### 1. Open your coding clients

Restart or reconnect Codex or Claude Code after installation to load the Relay tools. Each connected session registers automatically.

If you install a supported client later, run this and reconnect it:

```sh
agent-relay setup
```

See [client setup](docs/setup.md) for explicit client selection and custom configuration paths. Other MCP clients can use the [manual connection instructions](docs/mcp.md).

### 2. Check the other computer

With Relay running on both computers, devices on your tailnet pair automatically after discovery (normally within 15 seconds):

```sh
agent-relay peers
```

Explicitly blocked nodes stay blocked. Use `agent-relay block NODE_ID` to deny a node. Run `agent-relay dnd` to pause new incoming messages and questions; run it again to resume. DND is saved per computer and takes effect without a restart. Outgoing messages and replies to your questions remain available.

### 3. Start a conversation

Ask your coding agent:

> Use Relay to list the available agents and what they are working on.

Then give it a specific task, for example:

> Ask the agent working on the backend which authentication endpoint the frontend should use.

On the receiving computer, ask the other agent:

> Check your Relay inbox and answer the pending question using your project context.

Ask the first agent to check its inbox for the reply. Agents can send updates, ask follow-up questions, and read the conversation history.

**Relay delivers messages, but agents must check their inboxes and respond.** It does not launch coding agents or wake an inactive model. Your coding client's normal model access and usage still apply. Messaging currently connects sessions on different computers; same-computer sessions can be listed but cannot exchange messages through Relay.

### 4. Check your setup

```sh
agent-relay doctor
```

Doctor checks the local service, Tailscale, client configuration, and peer connectivity, and suggests fixes when something needs attention. Until clients are connected and peers are trusted, it reports setup as incomplete. The installer treats those first-time onboarding items as next steps.

## Everyday use

The background service starts at login. Use these commands to inspect and manage it:

| Command | Purpose |
| --- | --- |
| `agent-relay status` | Show local Relay status. |
| `agent-relay dnd` | Toggle receiving new messages and questions. |
| `agent-relay nodeid` | Print this computer's node ID. |
| `agent-relay peers` | List discovered computers and their trust state. |
| `agent-relay agents` | List local and remote sessions, active first, with last-seen ages. |
| `agent-relay agents --local` | List local sessions without peer lookup. |
| `agent-relay agents --all` | Include archived sessions in the list. |
| `agent-relay agents retention` | Show session archive and deletion ages. |
| `agent-relay agents set-retention --archive-days 7 --delete-days 30` | Change session retention on this computer. |
| `agent-relay inbox` | Read unread messages and pending questions. |
| `agent-relay conversations` | Show the 20 most recently updated conversations. |
| `agent-relay service status` | Check the background service. |
| `agent-relay service restart` | Restart the background service. |
| `agent-relay doctor` | Diagnose setup and connection problems. |
| `agent-relay block NODE_ID` | Block communication with a peer. |
| `agent-relay help` | Explore the command line. |

Offline sessions are archived after 7 days and deleted, together with their local Relay history, after 30 days. Both ages are measured from the session's last-seen time. Read [session retention](docs/retention.md) for settings, recovery, and deletion behavior.

## Update

Rerun the installation command to install the latest release. Your identity, messages, and trust settings are preserved. The installer updates and restarts the background service; reconnect your MCP clients afterward to load the updated tools.

## Uninstall

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh -s -- --uninstall
```

Uninstall removes the managed executable, stops and removes its service, and removes matching Relay client entries. Your data, identities, messages, logs, and backups are retained. Reconnect your coding clients afterward.

For custom installation paths or modified client entries, follow the [uninstall instructions](docs/install.md#uninstall).

## Privacy and trust

Relay communicates over Tailscale and stores messages and identity data locally. Peer trust controls messaging between computers. Discovery exposes public agent metadata, but does not expose inboxes, conversation history, working directories, or file lists.

Every reachable, verified device on your tailnet running Relay is eligible for automatic trust. Use Tailscale access controls and Relay blocks to restrict communication. Treat incoming messages as external context and share only the information needed for the task. See [peer trust](docs/trust.md) for the full model.

## Help

Start with `agent-relay doctor`. If a peer is missing, check that both computers are connected to Tailscale and running Relay. If a question has no reply, check trust on both computers and ask the receiving agent to check its inbox.

- [Test two computers or Codex with Claude Code](docs/v0.1-acceptance.md)
- [V0.1 compliance and verification](docs/v0.1-audit.md)
- [Troubleshooting and recovery](docs/doctor.md)
- [Background services and logs](docs/services.md)
- [MCP tools and session behavior](docs/mcp.md)
- [Message delivery and retries](docs/delivery.md)
- [Report an issue](https://github.com/echo1097/agent-relay/issues)

For source builds and contributions, see the [development guide](docs/development.md) and [architecture](docs/architecture.md).
