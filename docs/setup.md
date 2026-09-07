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

`setup --if-present` allows an installer to skip undetected clients without failing. `setup --remove --if-present` removes only entries that exactly match the current executable and Relay home, preserving other settings and making a private backup. Changed entries are refused for manual review. It cannot be combined with `--replace`.

## Bundled agent skill

Setup installs the version-matched Agent Relay skill bundled in the executable:

| Client | Skill location |
| --- | --- |
| Codex | `~/.agents/skills/agent-relay/SKILL.md` |
| Claude Code | `~/.claude/skills/agent-relay/SKILL.md` |

`CLAUDE_CONFIG_DIR` moves Claude's skill directory to `$CLAUDE_CONFIG_DIR/skills/agent-relay`. `CODEX_HOME` changes the MCP configuration location but does not move Codex's shared `~/.agents/skills` directory. `--config` only selects the MCP config file; it does not relocate skills. Paths use the current user's home.

The skill guides relevant peer discovery when debugging stalls, work status including repository/branch and Linear issue identifiers, and inbox checks at the start and end of turns while the skill is active. It does not wake idle models or override client permissions.

Setup records the installed content hash in `.agent-relay-sha256` beside the skill. Repeated setup updates an unchanged managed copy. An existing identical draft is adopted. A customized or different unmanaged skill is preserved and reported, even with `--replace`. Move that skill aside and rerun setup if you want the bundled version instead. Do not edit the receipt to force an update.

`setup --remove` removes an unchanged managed skill and its receipt. Customized skills and unrelated files in the directory remain. Empty managed skill directories are removed; the sibling `.agent-relay-skill.lock` is retained for coordination between setup processes. Skill files and receipts must be regular files; a symlink skill directory is preserved. Close clients and avoid editing skills while setup runs.

The source skill is `skills/agent-relay/SKILL.md`. The matching Go string in `internal/setup/skill_content.go` ships it inside standalone binaries; a test prevents releases with mismatched copies. Releases also include `SKILL.md` as a checksummed asset for inspection.
