# Agent Relay

Local foundation, agent registry, and HTTP protocol for the [Agent Relay PRD](PRD.md), implemented in Go. This provides configuration, logging, SQLite migrations, persistent node and agent identities, metadata, presence, and a foreground HTTP daemon. It includes Tailscale detection, peer discovery, and [local conversation and messaging storage](docs/messaging.md) with durable inboxes. It now includes [peer message delivery](docs/delivery.md), explicit configured peer trust, durable outgoing retries, and CLI messaging with linked responses and follow-up conversations. MCP, full trust management, and service installation remain pending.

Requires Go 1.25 or newer. The current daemon lock supports macOS and Linux. SQLite is compiled into the executable without CGO or a separate database service.

## Build and check

```sh
source .venv/bin/activate
export GOMODCACHE="$PWD/.cache/go-mod"
export GOCACHE="$PWD/.cache/go-build"
go fmt ./...
go vet ./...
go test ./...
go build ./...
go build -o bin/agent-relay ./cmd/agent-relay
```

The Python virtual environment is only a repository workflow requirement; the application itself uses Go. If working from a fresh checkout, create it with `python3 -m venv .venv` before activation. Dependency downloads require access to the public Go module proxy. Tests use fake Tailscale clients and local HTTP servers. The daemon and doctor probe visible tailnet peers; no model APIs are used.

## Manual checks

```sh
./bin/agent-relay help
./bin/agent-relay version
relayHome=$(mktemp -d)
./bin/agent-relay status --home "$relayHome"
./bin/agent-relay status --home "$relayHome"
./bin/agent-relay daemon --home "$relayHome" &
relayPid=$!
sleep 1
./bin/agent-relay status --home "$relayHome"
kill -TERM "$relayPid"
wait "$relayPid"
./bin/agent-relay status --home "$relayHome"
cat "$relayHome/logs/agent-relay.log"
```

The node ID remains the same on each invocation. Status reports `Stopped`, then `Running`, then `Stopped`. The daemon requires connected Tailscale and serves HTTP on its detected IPv4 address at port `47832` by default, checks local presence, and handles SIGINT or SIGTERM. A second daemon using the same directory exits with an error. The OS releases the lock even if the process crashes; the lock file itself remains and must not be deleted while a daemon is running.

## Configuration and data

The default application directory is `~/.agent-relay`. The `daemon`, `status`, `peers`, `doctor`, and `agents` actions accept `--home PATH`. Each application directory has an independent database and node identity.

- `config.toml`: optional user configuration; missing fields use PRD defaults.
- `relay.db`: SQLite database, with SQLite-managed WAL and shared-memory files when needed.
- `logs/agent-relay.log`: append-only structured text logs.
- `peers.json`: atomically replaced discovery cache with last-seen timestamps.
- `daemon.lock`: OS-managed exclusive daemon lock.

`status`, `daemon`, `peers`, `doctor`, message actions, and agent actions create the local directories, migrate the database, and initialize identity on first use. Version and help commands do not initialize application state. Directories are created with mode 0700; database, peer cache, log, and lock files with mode 0600. Existing directory permissions are left unchanged.

Create `config.toml` if you want to override defaults:

```toml
[network]
bind_address = "tailscale"
port = 47832

[discovery]
interval_seconds = 15

[presence]
heartbeat_seconds = 10
offline_after_seconds = 30

[messages]
request_expiration_hours = 24

[logging]
level = "info"
```

Discovery and messaging settings are active. Messages retry using the schedule in the PRD; `messages.retry_interval_seconds` controls the slower interval after the initial retries (default 900, minimum 300). Unknown fields, invalid TOML, invalid log levels, and invalid numeric settings return an error. Configuration is loaded at startup; restart the daemon after changing it. Logs use Go's structured text handler, written to stderr and the log file. Levels are `debug`, `info`, `warn`, and `error`. Log rotation is not implemented in this chunk.

The default version is `0.1.0-dev`. To set a release version:

```sh
go build -ldflags '-X main.version=0.1.0' -o bin/agent-relay ./cmd/agent-relay
```

See [architecture decisions](docs/architecture.md) for foundation boundaries and storage details.

## Local agent registry

Phase 2 adds durable local agent registration and presence. The daemon checks for timed-out agents every second and marks local agents offline when it shuts down. Restart a running daemon after rebuilding to use this behavior.

The internal Go API and its update rules are described in [local agents and presence](docs/agents.md).

Build the binary using the commands above, then try the following with an isolated application directory:

```sh
relayHome=$(mktemp -d)
cat > "$relayHome/config.toml" <<'TOML'
[presence]
heartbeat_seconds = 1
offline_after_seconds = 3
TOML

agentID=$(./bin/agent-relay agents register --home "$relayHome" --name codex-auth --provider codex --task "Investigating auth")
./bin/agent-relay agents list --home "$relayHome"
./bin/agent-relay agents get --home "$relayHome" --id "$agentID"
./bin/agent-relay agents set-status --home "$relayHome" --id "$agentID" --state busy
./bin/agent-relay agents heartbeat --home "$relayHome" --id "$agentID"
sleep 4
./bin/agent-relay agents get --home "$relayHome" --id "$agentID"
./bin/agent-relay agents register --home "$relayHome" --id "$agentID" --name codex-auth --task "Resumed work"
./bin/agent-relay agents update-metadata --home "$relayHome" --id "$agentID" --task "Checking sessions" --project backend --file auth.go
./bin/agent-relay agents disconnect --home "$relayHome" --id "$agentID"
./bin/agent-relay agents list --home "$relayHome"
```

Registration prints the ID to stdout for shell capture. Get, heartbeat, status, and metadata updates print JSON. Logs go to stderr and the local log file. The get after the four-second pause reports `offline`; reconnecting preserves the ID. Reconnection and metadata replacement clear any omitted metadata fields. Multiple sessions can share the same display name.

`agent-relay agents help` shows all actions and flags. Agent commands access the local database directly, so they work with or without a running daemon. They do not start a coding agent or communicate with any model provider.

## HTTP protocol

Phase 3 adds `GET /v1/health`, `GET /v1/hello`, and `GET /v1/agents`. The production listener binds only to the detected Tailscale IPv4 address. Configure `network.port` consistently on all nodes. For local development only, set `network.development = true` and `network.bind_address = "127.0.0.1"`. Wildcard addresses are rejected in every mode.

The HTTP API uses validated public response types, protocol version 1, JSON errors, bounded request times, and graceful shutdown. Working directories and file lists are never included in public agent responses. See [HTTP protocol](docs/protocol.md) for response formats, metadata rules, timeouts, and curl examples.

## Tailscale and peer discovery

Run `agent-relay peers` to read the daemon's peer cache, `agent-relay status` for local Tailscale and cached peer state, and `agent-relay doctor` for live local-listener and peer checks. Doctor exits unsuccessfully when Tailscale, the local listener, or compatible remote peers are unavailable, and prints remediation hints.

Discovery runs immediately and every 15 seconds by default. Eight workers probe visible Tailscale IPv4 peers on the configured port. Tailscale commands have a five-second timeout, probes have a three-second timeout, and each discovery round has a ten-second budget. Probes bypass HTTP proxies, reject redirects, and limit response sizes. Large tailnets may need future scheduling improvements if a round cannot probe every device within its budget.

A successful compatible hello records the peer identity and last-seen time. Previously reachable peers become suspect after failures and unreachable after three consecutive failed probes. Invalid responses and incompatible versions are reported immediately. Disappeared peers remain in the cache with their last-seen time. Local Tailscale failures mark cached peers unknown. A stopped daemon or overdue refresh is displayed as stale. Cache entries are discovery observations and do not grant trust.

Tailscale must be available through `tailscale` on PATH or the macOS application CLI. Production startup fails if Tailscale is unavailable or disconnected. If its IPv4 address changes, discovery reports that a daemon restart is required. Restart the daemon after installing this build. Background discovery is cancelled and joined during shutdown.

## Peer messaging

Use `agent-relay messages help` for send, respond, get, inbox, and history commands. Configure trusted peer node IDs and addresses before sending. The daemon delivers queued regular messages, questions, and responses, persists incoming messages before acknowledgment, and retries temporary failures after restart. See [delivery behavior and the two-terminal test procedure](docs/delivery.md). Restart running daemons after installing this build.

For the full A → B → A → B → A flow, follow the [two-terminal or two-machine conversation test](docs/conversation-test.md), including installation on another test machine. The mandatory PRD end-to-end test now covers all four messages and durable history on both nodes without MCP.
