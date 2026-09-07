# Local architecture

The PRD is the source of truth. Phases 1 through 3 are implemented. See [local agents and presence](agents.md) for registry behavior and [HTTP protocol](protocol.md) for the protocol foundation.

`cmd/agent-relay` establishes signal cancellation and invokes `internal/cli`. The CLI composes configuration, directories, structured logging, storage, the agent registry, and the local daemon lifecycle. The registry uses a small storage interface implemented by SQLite; no transport interfaces exist yet.

## Decisions beyond the PRD

- The module path is `agent-relay` because the repository does not specify a canonical hosting URL. It can be updated once that URL is chosen.
- Standard-library `flag` and `log/slog` avoid CLI and logging dependencies. BurntSushi TOML supplies strict TOML parsing. The pure Go `modernc.org/sqlite` driver keeps the binary self-contained and avoids a CGO toolchain, at the cost of its transitive dependencies. The already-present Google UUID library now supplies agent UUIDv7 generation as a direct dependency.
- The current platform scope is macOS and Linux. A standard-library Unix file lock supplies crash-safe, per-directory daemon exclusion and a basic local running indicator. Status does not imply a responsive network service. Windows support needs a platform-specific lock implementation later.
- A missing configuration file means all defaults apply. It is not generated automatically. `--home` is the only path override; there are no environment-based configuration overrides.
- The first status, daemon, or agent action initializes local state. Help and version remain side-effect free. Status and agent reads also persist expired presence and are consequently not read-only inspection commands.
- The first migration includes the PRD's `nodes` table and a singleton `local_node` reference. This distinguishes the durable local identity from future peer records. Migration 2 adds agents. Migration 3 adds conversations, messages, and processed receipt markers. Migration 4 adds remote peer bindings and the durable outbox.
- Node IDs use `node_` followed by a full UUIDv7 generated with cryptographic randomness. The OS hostname is captured at initial creation and remains stable with the identity. The local node's trust state is `trusted`; no peer trust behavior is implemented.
- Ordered migration SQL lives in the `migrations` Go package and is compiled into the binary. Each new schema change adds a consecutive migration. An immediate SQLite transaction serializes migration application, records versions and timestamps, and rolls back failures. Newer schemas are rejected. Existing migrations must not be edited after release.
- SQLite uses WAL, full synchronization, foreign key enforcement, a five-second busy timeout, and one pooled connection per store. Initial identity creation is transactional so concurrent launches share one identity.
- The foreground daemon owns the process lock, serves the three HTTP endpoints, and checks local presence every second. HTTP shutdown drains active requests before local agents are marked offline and the lock, database, and logs are closed. OS service registration remains a later phase.
- The production HTTP listener resolves and binds only to the connected local Tailscale IPv4 address. Explicit development mode allows loopback. `internal/tailscale` wraps the local CLI behind an interface; `internal/discovery` probes compatible hello responses and atomically persists an ephemeral JSON cache, separate from durable SQLite identities. `internal/protocol` defines validated public objects separate from local storage models. The daemon uses only the standard library for HTTP and does not expose agent mutation endpoints.

Tests cover configuration validation, local directory creation, logging levels, command handling, identity persistence, concurrent initialization, migration rollback, future-schema rejection, upgrades from the foundation schema, agent registry operations, and daemon lifecycle.
