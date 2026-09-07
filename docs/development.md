# Development

Agent Relay is written in Go and uses embedded SQLite. Source builds require Go 1.25 or newer. End users should use the compiled binaries through the [installer](install.md).

## Build and check

The repository workflow uses a Python virtual environment; the application itself does not require Python. Create the environment once if it is missing:

```sh
python3 -m venv .venv
```

From the repository root:

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

Dependency downloads require access to the public Go module proxy. Tests use fake Tailscale clients and local HTTP servers; they do not call model APIs. Run `go test -race ./...` when changing concurrent behavior. Real service integration tests are opt-in through `AGENT_RELAY_SERVICE_TEST=1` and affect the host's service manager.

## Run locally

Use `./bin/agent-relay help` to explore commands. Select an isolated data directory with `--home PATH` when testing so existing identities, trust, and messages are preserved. A daemon also needs an available port. Production mode requires connected Tailscale; explicit loopback development configuration is described in the [HTTP protocol guide](protocol.md).

Restart the backend after changing its executable or configuration. Installed services use a private executable copy: rerun `service install` from the new binary to update that copy. Reconnect MCP clients to load changes to their executable or configuration. Stop temporary daemons and services when testing is complete.

## Reference

- [Product requirements](../PRD.md)
- [Architecture and storage](architecture.md)
- [Agent registration and presence](agents.md)
- [HTTP protocol](protocol.md)
- [MCP integration](mcp.md)
- [Two-machine conversation testing](conversation-test.md)
- [Installer and release maintenance](install.md#maintaining-the-hosted-installer)
- [Installer verification](installer-verification.md)
