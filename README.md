# Agent Relay

Local foundation and agent registry for the [Agent Relay PRD](PRD.md), implemented in Go. This provides configuration, logging, SQLite migrations, persistent node and agent identities, metadata, presence, and a foreground daemon. It does not implement networking, Tailscale, MCP, messaging, or service installation.

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

The Python virtual environment is only a repository workflow requirement; the application itself uses Go. If working from a fresh checkout, create it with `python3 -m venv .venv` before activation. Dependency downloads require access to the public Go module proxy. The application and its tests do not call external services.

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

The node ID remains the same on each invocation. Status reports `Stopped`, then `Running (local registry)`, then `Stopped`. The daemon checks local presence and handles SIGINT or SIGTERM. It opens no listening sockets. A second daemon using the same directory exits with an error. The OS releases the lock even if the process crashes; the lock file itself remains and must not be deleted while a daemon is running.

## Configuration and data

The default application directory is `~/.agent-relay`. The `daemon`, `status`, and `agents` actions accept `--home PATH`. Each application directory has an independent database and node identity.

- `config.toml`: optional user configuration; missing fields use PRD defaults.
- `relay.db`: SQLite database, with SQLite-managed WAL and shared-memory files when needed.
- `logs/agent-relay.log`: append-only structured text logs.
- `daemon.lock`: OS-managed exclusive daemon lock.

`status`, `daemon`, and agent actions create the local directories, migrate the database, and initialize identity on first use. Version and help commands do not initialize application state. Directories are created with mode 0700; database, log, and lock files with mode 0600. Existing directory permissions are left unchanged.

Create `config.toml` if you want to override defaults:

```toml
[network]
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

The settings for later phases are parsed and validated but do not activate those features. Unknown fields, invalid TOML, invalid log levels, and invalid numeric settings return an error. Configuration is loaded at startup; restart the daemon after changing it. Logs use Go's structured text handler, written to stderr and the log file. Levels are `debug`, `info`, `warn`, and `error`. Log rotation is not implemented in this chunk.

The default version is `0.1.0-dev`. To set a release version:

```sh
go build -ldflags '-X main.version=0.1.0' -o bin/agent-relay ./cmd/agent-relay
```

See [architecture decisions](docs/architecture.md) for foundation boundaries and storage details.

## Local agent registry

Phase 2 adds durable local agent registration and presence. No network services are opened. The daemon now checks for timed-out agents every second and marks local agents offline when it shuts down. Restart a running daemon after rebuilding to use this behavior.

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
