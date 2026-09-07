# Agent Relay v0.1.3

Fixed macOS installation when launchd registers the background service but defers its automatic launch.

- Explicitly starts newly registered services with `launchctl kickstart`, including in on-demand-only GUI domains.
- Leaves already running services running without an extra start request.
- Startup readiness failures now include the service status command and log directory while preserving the underlying error.
- Added regression tests for deferred startup and readiness status timeouts.

Validation: the full unit test suite, vet, build, and installer syntax checks passed. A real macOS service test confirmed installation and readiness, but its later automatic crash-recovery check failed on the test machine's on-demand-only GUI domain. This release does not resolve automatic recovery in that environment.

Install or update with Tailscale connected:

```sh
curl -fsSL https://echo1097.github.io/agent-relay/install.sh | sh
```

Reconnect Codex or Claude after updating. The installer restarts the background service. Existing identities, messages, and trust are preserved.

Compiled binaries are included for macOS and Linux on arm64 and amd64. The release workflow runs tests and vet, validates shell syntax, and publishes SHA-256 checksums alongside the binaries.
