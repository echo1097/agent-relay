# Agent Relay v0.1.7

Agent Relay now includes `agent-relay update` and `agent-relay uninstall` for installations created by the installer.

`agent-relay update` installs the latest release using the recorded installation paths and service name, verifies the download, and restarts the background service. Use `agent-relay update --version TAG` to choose a release.

`agent-relay uninstall` removes the managed executable, background service, matching MCP client entries, Relay startup instructions, and unchanged managed skills. It works without a network connection and retains your database, identity, messages, trust settings, logs, and backups. Personal client instructions and unrelated settings are preserved.

The installer also shows download progress, and interrupted update or uninstall commands stop their child processes cleanly.

Session lists now show active sessions first and include last-seen ages. Automatic cleanup archives offline sessions after 7 days and permanently deletes them after 30 days by default, measured from their last-seen time. Deletion includes those sessions' messages and conversations. Use `agent-relay agents retention` to inspect the policy, `agent-relay agents set-retention --archive-days 14 --delete-days 60` to change it, and `agent-relay agents --all` to include archived sessions.

To upgrade from v0.1.6 or earlier, run the installer once:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

After that, future updates can use `agent-relay update` directly. The installer restarts the backend; reconnect Claude and Codex MCP clients after upgrading.

The final health check can still report an unreachable saved peer even when the new version is installed and running. Completed installation steps are retained.
