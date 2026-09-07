# Installer verification

Verified September 6, 2026 (Pacific time) against public release `v0.1.0`, built from commit `ba0c7c3`. No model-provider APIs were invoked.

The Pages deployment succeeded and the public `install.sh` matched the committed source byte for byte. GitHub Release workflow 34087066008 passed tests and vet on Linux, built all four compiled binaries, generated SHA256SUMS, and published the complete release. Assets are available without authentication at [v0.1.0](https://github.com/echo1097/agent-relay/releases/tag/v0.1.0).

## Automated tests

`go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, `sh -n installers/install.sh`, and `git diff --check` passed locally. Tests that bind loopback sockets require a test environment that permits local listeners.

Installer tests use temporary directories, fake release downloads, and fake service/Tailscale commands. They cover all four target mappings, successful and repeated installation, manifest validation, checksum mismatch, failed downloads, unsupported platforms, unavailable services, disconnected Tailscale, invalid tags, unmanaged binaries, symlinks, changed binaries, mismatched homes, failures at each post-install stage, retries, uninstall without network/Tailscale, repeated uninstall, and preservation of data. CLI tests cover first-time installation diagnostics without hiding broken installations and matching-only MCP removal with backups and preservation of unrelated settings.

## Public-command checks on real hosts

| Target | Host and service manager | Result |
| --- | --- | --- |
| macOS arm64 | Machine B, launchd GUI user session | Passed |
| macOS amd64 | Machine A under Rosetta, launchd GUI user session | Passed |
| Linux arm64 | Jetson Thor, systemd user manager | Passed |
| Linux amd64 | GitHub Linux runner build and native Go tests | Built; no full installer/service test on an amd64 Linux host |

The three full checks executed the actual public curl-to-sh command and downloaded release assets. They used isolated data directories, temporary Codex and Claude configuration locations, service `installer-check-20260907`, and Tailscale port 47913. Each check verified:

1. Correct target asset, checksum verification, local database initialization, and actual supervisor startup.
2. Codex and Claude MCP configuration, with passing doctor checks for both entries, Tailscale, database, service, listener, and binary version.
3. Repeated installation with unchanged node identity.
4. Explicit NEXT steps for first-time agent registration and peer trust, without automatically enrolling trust or starting a coding agent.
5. Uninstall removed the managed binary, receipt, service definition, and matching MCP entries, while preserving the SQLite database and unrelated client settings.
6. A second uninstall succeeded without changing retained data.

All temporary services were removed after testing. Machine A and B's pre-existing daemons remained running on port 47832 and returned identical original hello responses before and after the checks. Their production identities and data were not replaced. No login/logout or reboot was performed; startup-at-login is configured by the existing launchd/systemd integration.

Verification logs and retained test data are in ignored cache directories: Machine B's checkout `.cache/installer-verification-20260907`, Machine A's checkout `.cache/installer-amd64-verification-20260907`, and Jetson's `~/.cache/agent-relay-installer-verification-20260907`. These are intentionally separate from the normal Relay homes. Test service and client backup/lock files follow the documented retention policy.
