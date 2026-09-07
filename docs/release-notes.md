# Agent Relay v0.1.2

Installation now shows short progress lines instead of full status and diagnostic reports. Errors still include the failed command's output.

- Added terminal colors, with plain output when redirected or when `NO_COLOR` is set.
- Installation ends with this computer's node ID and a pairing command to run on the other computer.
- Added `agent-relay nodeid` to print the persistent local node ID, including support for `--home`.
- Uninstall uses concise progress output and retains its existing data preservation behavior.

Install or update with Tailscale connected:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

Reconnect Codex or Claude after updating. The installer restarts the background service. Existing identities, messages, and trust are preserved.

Compiled binaries are included for macOS and Linux on arm64 and amd64. The release workflow runs tests and vet, validates shell syntax, and publishes SHA-256 checksums alongside the binaries.
