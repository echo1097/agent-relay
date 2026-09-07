# Setup and service verification

Verified September 6, 2026. No model-provider APIs were called.

## Automated checks

`go test -race ./...`, `go vet ./...`, and `go build ./...` passed. Native macOS arm64 and cross-compiled Linux arm64 executables built successfully.

Configuration tests cover client detection and environment overrides, both formats, preservation of unrelated settings and MCP entries, exact large JSON numbers, backup contents and permissions, unchanged repeated setup, explicit conflict replacement, malformed inputs including empty JSON and duplicate keys, symlinks, inaccessible paths, concurrent edits, and configuration locking.

Service tests cover lifecycle commands, idempotent install, stable private binary copies, previous-binary preservation, home-change refusal, unowned/edited definitions, unavailable service managers, failed startup recovery, HTTP readiness, missing data directories during uninstall, path escaping, XDG paths, and published template consistency.

## Installed client checks

Used isolated configuration directories under the checkout's ignored `.cache` directory. Existing user configurations were not edited.

* `codex mcp get agent-relay --json` read the setup-generated TOML through `CODEX_HOME`, reporting an enabled stdio server with the correct absolute binary path and Relay home.
* `claude mcp get agent-relay` read the setup-generated JSON through `CLAUDE_CONFIG_DIR`, reported user scope, and successfully connected to the Relay MCP subprocess.
* The real-manager integration test parsed both generated configuration files and used their actual command and argument arrays to connect MCP clients. Both exposed all eight tools and successfully called `relay.update_status`.

## Real service managers

| Host | Supervisor | Result |
| --- | --- | --- |
| Machine B, macOS arm64 | launchd GUI user domain | Full test passed in 12.88 seconds. |
| Jetson test host, Linux arm64 | systemd 255 user manager | Full test passed in 13.71 seconds. |

The opt-in `TestOSServiceLifecycle` test uses a unique service name, temporary Relay home containing spaces and special characters, and an available loopback port. It installs and starts the service, repeats installation, checks status and HTTP identity, connects MCP clients, upgrades to a differently versioned binary, checks the previous executable, crashes only the test daemon, waits for supervisor recovery, restarts, stops twice, starts, and uninstalls. Node identity remains stable throughout. Graceful stops are verified in the log, and the database remains after uninstall. Cleanup removes test services and temporary files.

Machine B's pre-existing daemon and data directory were preserved. Its original Tailscale HTTP hello still reported node `node_01a07a0e-978a-7c36-a80f-9c1441a25ffc` after testing. No test launchd jobs remained.

Production Tailscale messaging has separate existing two-machine coverage. These new service-manager tests intentionally use isolated loopback configurations so they do not replace the user's running daemons or alter peer trust. Login/reboot persistence is configured by launchd RunAtLoad and systemd enablement; physical reboot and logout were not performed. Linux lingering is not enabled automatically.
