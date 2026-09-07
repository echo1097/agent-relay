# Unix installation

Run as your normal login user with Tailscale installed and connected:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

GitHub Pages hosts the script in this repository. No custom domain is used. Downloads come from public GitHub Releases in `echo1097/agent-relay`. The script resolves the latest release once and downloads its binary and SHA256SUMS from that same tag. You receive a compiled executable, with no Python, Node, Docker, Go, or package manager required. Standard Unix utilities, curl, and either sha256sum or shasum must be available.

Supported targets are macOS arm64, macOS amd64, Linux arm64, and Linux amd64. Release assets are named `agent-relay_<darwin|linux>_<arm64|amd64>`. Linux binaries are built with CGO disabled. Unsupported systems fail before downloading. macOS requires a logged-in GUI user session; Linux requires a functioning systemd user manager. Services start at login. Linux lingering is optional and is never enabled automatically.

For an inspectable download, pinned release, or stronger handling of a failed initial curl, download the script before running it:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh -o install.sh &&
AGENT_RELAY_VERSION=v0.1.0 sh install.sh
```

A plain curl-to-sh pipeline cannot propagate curl's exit status through every POSIX shell. The script runs its main function only after it has been fully read. Binary and manifest downloads are independently checked and bounded, and an incomplete or mismatched binary is never executed.

## Paths and options

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `AGENT_RELAY_VERSION` | latest published release | Pin a release tag such as `v0.1.0`. |
| `AGENT_RELAY_BIN_DIR` | `~/.local/bin` | Absolute executable directory. |
| `AGENT_RELAY_HOME` | `~/.agent-relay` | Absolute data directory; preserve this to keep identity. |
| `AGENT_RELAY_SERVICE_NAME` | `agent-relay` | Named service instance for isolated installs. |

For a pipeline, apply environment options to `sh`, not to `curl`. The installer prints the executable directory if it is missing from PATH. Add it to your shell configuration yourself; shell startup files are not edited. The service and MCP clients use absolute paths and work without this PATH change.

The installer checks Tailscale and the service manager, installs the binary, runs `status` to initialize local storage, runs `service install`, configures detected clients, then runs `doctor --installation`. No supported client installed is an explicit NEXT step. Conflicting or malformed detected client configurations remain errors. No provider API is invoked and no coding client is launched.

Installation doctor reports missing first-time agents, missing clients, missing peers, and missing trust as NEXT steps. It still fails for database problems, broken client entries, disconnected Tailscale, listener problems, invalid peer protocols, and unreachable already trusted peers. Plain `agent-relay doctor` remains strict and reports incomplete onboarding as failures. Reconnect clients and allow discovery to automatically trust verified tailnet devices on both computers.

## File safety and upgrades

A sibling `.agent-relay-receipt` records the executable checksum, Relay home, and service name. Repeated installation accepts only a regular executable matching this receipt and the same settings. Unrelated executables, symlinks, changed binaries, mismatched homes, and conflicting service/client entries are refused. Installer operations share an exclusive directory lock. An interrupted process can leave the lock directory; confirm no installer is running before removing that empty directory.

Rerun the command to upgrade. Existing identities, messages, and trust remain in the same data directory. The service installation updates its private binary and restarts the backend. Client configuration uses the stable executable path and keeps its existing safe-backup behavior. Reconnect MCP clients after upgrades to load the new executable.

Downloads require HTTPS and an exact unique SHA-256 manifest entry before execution. Checksums detect corrupt or substituted downloads relative to the manifest; they are not an independent signature if the GitHub release itself is compromised. Releases are prepared as drafts and published only after all four binaries and the manifest are uploaded.

Installation is a sequence of recoverable steps, not a transaction across client files, service managers, and SQLite. Completed steps are retained on failure and the script names the failed stage. It never automatically deletes a database, downgrades migrations, or kills an unowned daemon. If a foreground daemon already uses the target home or port, stop it yourself or select an isolated home with a different configured port. A crash between executable and receipt updates can require manual reconciliation against the release manifest. Keep existing files until reviewed.

## Uninstall

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh -s -- --uninstall
```

Supply the same bin directory, Relay home, service name, and client environment overrides used during installation. Uninstall needs no release download, Tailscale connection, or build tools. It verifies the local receipt before executing the binary, removes matching MCP entries with private backups, stops/uninstalls the named service, and removes only the installer-managed executable and receipt. A modified MCP entry is refused for manual review, preserving the executable and service for recovery. Other servers and client settings remain intact. A repeated uninstall is harmless.

Databases, node identities, messages, trust, logs, previous service binaries, backups, and lock files are deliberately retained. Custom client files configured outside the detected CODEX_HOME/CLAUDE_CONFIG_DIR paths require manual removal of their Relay entry. Reconnect clients after uninstall. See [service locations and recovery](services.md) and [client backup behavior](setup.md).

## Maintaining the hosted installer

Source: `installers/install.sh`. The Pages workflow publishes only that file and these instructions as `README.txt`; it does not publish the entire docs directory. Configure repository Pages to use GitHub Actions. The intended public endpoint is `https://echo1097.github.io/agent-relay/install.sh`.

Push a reviewed `v*` tag to run the release workflow. It runs tests and vet, cross-compiles all four targets, generates SHA256SUMS, uploads a draft release, then publishes it. Do not move a published tag or replace its assets. A failed draft can be inspected and removed before retrying. Keep release tags compatible with the installer's `v` prefix validation.

See [installer verification](installer-verification.md) for automated coverage, real-host results, and test limits.

### Installer output and pairing

Installation shows short progress lines and prints detailed command output only when a step fails. Terminal output uses color unless `NO_COLOR` is set; redirected output stays plain. Run `agent-relay doctor` for the full diagnostic report.

The final lines show this computer's node ID. Verified tailnet devices pair automatically; explicitly blocked nodes stay blocked. Retrieve your ID anytime with `agent-relay nodeid`, or `agent-relay nodeid --home /absolute/path` for a custom data directory. This command prints only the persistent ID and initializes local storage if needed, without requiring a running daemon or Tailscale connection.

### Do not disturb

Run `agent-relay dnd` to toggle new incoming messages and questions off or on. The setting is stored in the local database and takes effect immediately, including for an already running daemon. It survives restarts. `agent-relay status` shows the current state. Use `--home PATH` for a custom installation.

DND leaves discovery, outgoing messages, existing history, and responses to your outgoing questions available. A rejected sender sees `DO_NOT_DISTURB`; rejected messages are not saved on the receiving computer or automatically resent after DND is disabled.
