# Background services

Agent Relay supports macOS launchd LaunchAgents and Linux systemd user services. Windows service support is future work. No sudo, system daemon, root account, model provider, or hosted component is required.

```sh
agent-relay service install --home /absolute/path/to/.agent-relay
agent-relay service status
agent-relay service stop
agent-relay service start
agent-relay service restart
agent-relay service uninstall
```

Install copies the current executable to a stable per-user location, writes the service definition and installation record, enables startup at login, starts the daemon, and waits up to 12 seconds for the OS to report it running, a Relay HTTP response matching the saved node identity and runtime version/address, and its daemon lock to be held. Failure to confirm startup returns an error with troubleshooting guidance; the installed supervisor can continue retrying. Use `agent-relay doctor --home PATH` to check peer connectivity.

Keep Tailscale connected. The daemon retries every 10 seconds under supervision when Tailscale is unavailable at startup. Installation does not grant peer trust or change the Relay network configuration. Existing data and node identity are reused with the supplied `--home`. If a foreground daemon already owns that home, stop it gracefully before installing. The service does not kill or replace unowned processes.

Run [MCP setup](setup.md) with that same home. MCP clients run independent local stdio subprocesses; stopping the background service pauses network delivery, not those client processes. Their queues remain durable.

## Names and paths

The default service name is `agent-relay`. Use `--name relay-test` on every service command for a separate instance. Names accept lowercase letters, digits, and hyphens and must start with a letter. Separate instances need separate homes and network ports. `--home` is accepted only by `install`; other commands read the installed home from the record. Reinstalling without `--home` reuses the recorded home. To change an installed service's home, uninstall first or choose another name.

| File | macOS | Linux |
| --- | --- | --- |
| Definition | `~/Library/LaunchAgents/com.agent-relay.agent-relay.plist` | `~/.config/systemd/user/agent-relay.service` |
| Installed directory | `~/Library/Application Support/agent-relay/services/agent-relay/` | `~/.local/share/agent-relay/services/agent-relay/` |
| Executable | `<installed-directory>/agent-relay` | `<installed-directory>/agent-relay` |
| Record | `<installed-directory>/install.json` | `<installed-directory>/install.json` |
| Runtime log | `<relay-home>/logs/agent-relay.log` | `<relay-home>/logs/agent-relay.log` |
| Startup stdout/stderr | `<relay-home>/logs/service.log` | `journalctl --user -u agent-relay.service` |

Linux respects absolute `XDG_CONFIG_HOME` and `XDG_DATA_HOME`. Use the same XDG configuration conventions as the running systemd user manager. Changing these environment variables later selects a different installation location. A custom `--name` replaces the final `agent-relay` name in these paths and the launchd label suffix.

Installation creates directories with mode 0700, definitions/records with 0600, and the installed executable with 0700. Existing config file modes remain intact. It creates persistent sibling `.agent-relay.lock` files for safe file updates and `service.lock` in the installed directory to serialize lifecycle commands. Modified definitions and records are backed up with the same private backup scheme as MCP setup. Binary upgrades keep one previous executable at `agent-relay.previous`. Backups and lock files remain after uninstall for recovery; uninstall removes the active definition, record, and managed executable. It does not recursively delete directories, databases, messages, configuration, identities, or logs.

Definitions are rendered from the formats in [launchd.plist.tmpl](../templates/launchd.plist.tmpl) and [systemd.service.tmpl](../templates/systemd.service.tmpl). Their placeholders are escaped by the Go renderer, with tests ensuring the published templates match. Do not substitute unescaped paths manually. The service supplies a predictable PATH containing Homebrew and standard system binary directories so Tailscale can be found without an interactive shell. It does not copy the shell environment or credentials into its definition.

## Restart and upgrade behavior

A repeated install of the same executable and definition does not stop a running daemon or make new backups. It ensures login startup is enabled and starts a stopped instance. To deploy a new build:

```sh
source .venv/bin/activate
export GOMODCACHE="$PWD/.cache/go-mod"
export GOCACHE="$PWD/.cache/go-build"
go build -o bin/agent-relay-new ./cmd/agent-relay
./bin/agent-relay-new service install
```

The service installer validates the Relay config and source executable, gracefully stops the managed daemon, waits for the daemon lock to be released, atomically replaces its private executable, updates its managed definition, and starts it again. `service restart` restarts the already installed private copy; it does not copy a newly built binary. If you move the executable configured in an MCP client, rerun setup with the new path and `--replace`, then reconnect that client. Backend changes require a daemon restart or service reinstall.

Shutdown uses SIGTERM with a 45-second supervisor timeout. The existing daemon cancels and joins discovery and delivery workers, drains HTTP requests, marks local agents offline, releases its lock, and closes SQLite through the CLI. A new process resumes durable retries and preserves conversations. No `launchctl kickstart -k` or forced SIGKILL is used for ordinary restarts. Supervisors can force termination after the timeout if graceful shutdown hangs.

If an upgrade fails after stopping the daemon, inspect `service status` and the logs, correct the error, and rerun install. Installation does not automatically downgrade a potentially migrated database. The previous binary is available for deliberate recovery only when compatible with the current schema. A system crash or filesystem failure between writing a definition and its record can leave a mismatched pair. Commands refuse to overwrite that mismatch: restore the matching definition/record backup pair, then reinstall. The database is unaffected by configuration-file recovery. Back up SQLite separately using a SQLite-aware backup before a schema-changing upgrade.

## macOS troubleshooting

A logged-in GUI session is required for `gui/<uid>` LaunchAgents. SSH works when that user already has a GUI login, as on machine B. The service starts on login, not at boot before login. Stop unloads it for the current session; its installed definition remains enabled for the next login. Uninstall removes that definition.

```sh
launchctl print gui/$(id -u)/com.agent-relay.agent-relay
launchctl print-disabled gui/$(id -u)
plutil -lint "$HOME/Library/LaunchAgents/com.agent-relay.agent-relay.plist"
tail -n 100 /absolute/path/to/.agent-relay/logs/service.log
agent-relay service restart
```

If launchctl cannot find the GUI domain, log into the Mac's desktop as the same user. If it reports an execution error, check the installed binary's executable bit, directory permissions, and macOS security/quarantine policy. If the daemon exits repeatedly, inspect the startup log and run `tailscale status`. Manual emergency unload is `launchctl bootout gui/$(id -u)/com.agent-relay.agent-relay`.

## Linux troubleshooting

The host must run systemd with a usable user manager. Containers, minimal distributions, or plain `su` sessions may lack one. The service normally starts when the user logs in. Running after logout or before login requires systemd lingering, which Agent Relay never enables automatically. If wanted, ask the machine administrator to enable lingering for your account; authorization requirements vary by distribution.

```sh
systemctl --user status agent-relay.service
systemctl --user show agent-relay.service
journalctl --user -u agent-relay.service -n 100 --no-pager
systemctl --user daemon-reload
systemctl --user reset-failed agent-relay.service
agent-relay service start
```

A user-bus error means the login environment or user manager needs attention. Check `XDG_RUNTIME_DIR`, your normal login session, and `systemctl --user show --property=Version`. An executable or working-directory error usually means a path was moved, permissions changed, or the home is unavailable. A port-in-use error means another daemon owns the configured network port; stop that daemon or choose a different port and restart. The user service does not bypass network or filesystem permissions.

Runtime file logs are append-only. Review their size and rotate them with your usual log maintenance; after renaming a file log, restart the service to reopen it. systemd journals follow the host's journal retention policy.

The platform behavior follows the local macOS `launchctl(1)` and `launchd.plist(5)` manuals and the official [systemd service](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html) and [user unit path](https://www.freedesktop.org/software/systemd/man/latest/systemd.unit.html) documentation.

## Optional real-manager test

Normal tests use fake service managers. An explicit integration test creates a unique test service, an isolated temporary data directory, a free loopback port, and real MCP subprocesses launched from both generated client configurations. It checks service lifecycle, graceful shutdown, recovery after forcibly crashing only its test daemon, node persistence, and all eight tools, then uninstalls the test service. It never invokes a model API or touches an existing Relay home. Optionally set `AGENT_RELAY_UPGRADE_BINARY` to a second binary built with a different version to test a real binary upgrade and preservation of the previous executable.

```sh
source .venv/bin/activate
export GOMODCACHE="$PWD/.cache/go-mod"
export GOCACHE="$PWD/.cache/go-build"
go build -o bin/agent-relay ./cmd/agent-relay
AGENT_RELAY_SERVICE_TEST=1 AGENT_RELAY_TEST_BINARY="$PWD/bin/agent-relay" go test -v ./internal/service -run '^TestOSServiceLifecycle$' -count=1
```

Run this only in a login session where temporary user service installation is appropriate. The test reports its generated name for manual cleanup if the test process itself is forcibly interrupted.

A fresh installation refuses an existing unowned private executable, and upgrades refuse symlinked or nonregular private executables before stopping the service. Review and move the conflicting file aside explicitly before retrying. A crash before an installation record is committed can leave such a file; it is retained for review rather than silently adopted.
