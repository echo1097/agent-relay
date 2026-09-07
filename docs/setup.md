# Local MCP setup

Build or install Agent Relay at a stable absolute path, then run:

```sh
agent-relay setup
agent-relay setup codex
agent-relay setup claude
agent-relay setup codex --home /absolute/path/to/.agent-relay
```

The first command detects both clients from their executable on PATH or an existing user config. Codex is also detected at `/Applications/Codex.app` or `~/Applications/Codex.app`. Explicit client commands can prepare config before the client is installed. If nothing is detected, setup exits with an explanation. Detection never starts a coding agent or calls a model API.

Use the same Relay `--home` as your daemon. Setup configures the executable that is running setup, using its absolute path. Keep that binary there. It does not copy binaries, start the daemon, create a node identity, grant peer trust, or change client approval rules. Restart/reconnect the coding client after setup to load all eight Relay MCP tools. Organization policies and project overrides still apply.

## Exact configuration changes

| Client | Default file | Override | Entry |
| --- | --- | --- | --- |
| Codex | `~/.codex/config.toml` | `$CODEX_HOME/config.toml` | `mcp_servers.agent-relay` |
| Claude Code | `~/.claude.json` | `$CLAUDE_CONFIG_DIR/.claude.json` | `mcpServers.agent-relay` at user scope |

These follow the official [Codex MCP format](https://developers.openai.com/codex/mcp) and [Claude Code MCP scopes](https://code.claude.com/docs/en/mcp), checked alongside installed client CLI help on September 6, 2026. Claude Desktop is a separate product and is not configured. Project `.mcp.json`, project `.codex/config.toml`, credentials, managed policy, and approval settings are not changed.

For a nonstandard file, specify the client explicitly:

```sh
agent-relay setup claude --config /custom/client/.claude.json
```

Codex receives:

```toml
[mcp_servers.agent-relay]
command = "/absolute/path/to/agent-relay"
args = ["mcp", "--home", "/absolute/path/to/.agent-relay"]
```

Claude Code receives the equivalent JSON entry with `"type": "stdio"`. No fixed agent ID or public task metadata is added, so each connected coding session registers independently. No environment variables, API credentials, shell wrapper, URL, or remote commands are added.

Only the `agent-relay` entry is added or replaced. Other settings and MCP entries retain their parsed values. On modification, the whole file is serialized: JSON indentation and key ordering can change; TOML formatting, key ordering, and comments are not retained. JSON numbers retain their exact representation. An identical entry is a byte-for-byte no-op with no additional backup. A different existing entry is rejected unless the user explicitly runs `--replace`:

```sh
agent-relay setup codex --replace
```

Review an existing entry before using this option. It replaces the entire selected `agent-relay` entry, including any custom arguments or restrictions on that entry. It never replaces other servers.

## File safety and recovery

Close the coding client while editing its config. Setup takes a nonblocking sibling lock, validates the full input, writes and syncs a private backup, writes a temporary file in the same directory, checks the original has not changed, then atomically renames the replacement. Other Relay setup processes use the lock; clients do not necessarily honor it, so a concurrent client write immediately after the final check remains possible.

An existing file is backed up to `<config-path>.agent-relay-backup-<unique-suffix>` with mode 0600 before replacement. Output prints the exact backup path. Backups can contain credentials from the original file; keep them private and remove old backups when no longer needed. Existing file permissions are retained; new files use 0600 and new directories use 0700. A persistent `<config-path>.agent-relay.lock` file is also created. Lock files must not be removed while setup is running.

Malformed TOML/JSON, duplicate JSON keys, non-object server maps, nonregular files, symlink targets, inaccessible files, and files larger than 8 MiB produce errors without replacing the config. Errors never print config contents. When configuring both clients, each file is independent: output identifies any completed client even if the other fails. Fix the reported problem and rerun.

To restore, close the client and copy the printed backup over the config file. To disconnect Relay, remove only its `agent-relay` server entry with the client’s MCP management command or editor. Restart the client afterward. If tools are missing, check the binary still exists, the configured Relay home matches the daemon, and a project or organization policy has not disabled the server.
