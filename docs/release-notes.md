# Agent Relay v0.1.5

Agent Relay now ships a skill for Claude Code and Codex. Curl installation and `agent-relay setup` install the bundled skill in `~/.claude/skills/agent-relay` and `~/.agents/skills/agent-relay`, respectively. Claude's `CLAUDE_CONFIG_DIR` override is respected.

The skill teaches agents to discover relevant peers when debugging stalls, publish repository and branch information plus a confirmed Linear issue identifier, and check their inbox at the start and end of each turn while the skill is active. Peer messaging remains subject to user authorization and client permissions. Skills do not wake idle agents.

Upgrades refresh unchanged managed skills. Customized or different unmanaged skills are preserved with a notice. Uninstall removes unchanged managed copies while retaining custom content and unrelated files. The binary contains the skill, so installation requires no extra skill download; releases also include a checksummed `SKILL.md` asset.

Update with:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

The installer restarts the backend. Restart or reconnect Codex and Claude after upgrading to load the tools and skill.
