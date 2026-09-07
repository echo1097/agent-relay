# HTTP protocol foundation

The daemon serves HTTP and JSON using the Go standard library. Tailscale discovery is supported. Regular-message/question delivery and configured peer trust are implemented; MCP and full trust management remain pending.

## Listener

The default is `127.0.0.1:47832`. Configure a literal IPv4 or IPv6 address and port in `config.toml`:

```toml
[network]
development = true
bind_address = "127.0.0.1"
port = 47833
```

The example explicitly enables loopback development mode. Production defaults to `bind_address = "tailscale"`, resolves the connected local Tailscale IPv4 address, and binds only to that address. Wildcard and other interface addresses are rejected. Restart the daemon to apply configuration or binary changes.

## Versioning and endpoints

All application responses include `protocol_version: 1` and the `X-Agent-Relay-Protocol-Version: 1` header. The `/v1/` URL communicates the request protocol version. Clients can additionally send `X-Agent-Relay-Protocol-Version: 1`. An unsupported version returns HTTP 400 with `UNSUPPORTED_PROTOCOL`; malformed or repeated version headers return `INVALID_REQUEST`.

Only these GET endpoints exist:

| Endpoint | Response |
| --- | --- |
| `/v1/health` | `{"protocol_version":1,"status":"ok"}`. Process liveness only. |
| `/v1/hello` | Protocol name/version, public node ID/name, and binary version. |
| `/v1/agents` | Protocol version and an `agents` array containing agents from the current node only. |

An empty registry produces `agents: []`. Offline agents remain listed with their current status. Agent listing applies the registry's presence expiration rules. GET requests do not accept bodies or query parameters. Other methods return HTTP 405 and `Allow: GET`. Unknown endpoints return HTTP 404. Agent registration and mutation endpoints remain deferred.

Example hello response:

```json
{
  "protocol": "agent-relay",
  "protocol_version": 1,
  "node": {
    "id": "node_019a84fc-1b72-7000-8000-000000000001",
    "name": "my-computer"
  },
  "version": "0.1.0-dev"
}
```

## Public metadata

`internal/protocol` defines dedicated public response types and validates them before serialization. The local SQLite agent model is never serialized directly by HTTP handlers.

The public agent fields are ID, display name, provider, status, task, project, repository, and branch. Working directories, file lists, local timestamps, database paths, configuration, environment variables, and internal error details are excluded. No filesystem or environment values are read to populate metadata.

Public text is length-bounded and checked for control characters, absolute Unix/Windows paths, environment references or assignments, recognizable credential patterns, and credential-bearing URLs. Unsafe optional values are omitted; unsafe display names use a neutral fallback. HTTP(S) repository URLs are normalized to host/path without `.git`; unsafe or unrecognized repository formats are omitted. Local records are unchanged.

Free-form metadata is intended to be explicitly publishable information. These conservative filters cannot identify every arbitrary secret someone might paste into a task or name. Cwd and files have no public fields at all, so they remain local regardless of content.

Validation also checks UUIDv7 identity format, supported agent states, response versions, duplicate agent IDs, and error codes. Invalid public records produce a generic internal error instead of an unvalidated response.

## Errors and limits

Application errors use one structure:

```json
{
  "protocol_version": 1,
  "error": {
    "code": "INVALID_REQUEST",
    "message": "Request bodies and query parameters are not supported."
  }
}
```

Application error codes are `INVALID_REQUEST`, `UNSUPPORTED_PROTOCOL`, `NOT_FOUND`, `METHOD_NOT_ALLOWED`, `INTERNAL_ERROR`, and `REQUEST_TIMEOUT`. Internal failures use HTTP 500 and request deadline failures use HTTP 504. Messages do not echo request values or underlying errors. Responses use JSON content type, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`. Malformed HTTP rejected before application dispatch is handled by Go's HTTP server.

Server limits are five seconds to read headers, ten seconds to read a request, ten seconds to write a response, thirty seconds for idle keep-alive connections, and 16 KiB of header data. Each handler supplies a five-second request context to the registry and SQLite. The existing SQLite operations honor cancellation.

Shutdown stops accepting connections and allows up to five seconds for active requests to finish, then closes remaining connections if needed. Local agents are marked offline after HTTP shutdown. The CLI then closes SQLite and logs.

## Manual test

Build the executable, then use a temporary application directory:

```sh
relayHome=$(mktemp -d)
cat > "$relayHome/config.toml" <<'TOML'
[network]
development = true
bind_address = "127.0.0.1"
port = 47833
TOML
./bin/agent-relay agents register --home "$relayHome" --name codex-auth --task "Checking auth" --cwd /private/local-only
./bin/agent-relay daemon --home "$relayHome" &
relayPid=$!
sleep 1
curl --max-time 5 http://127.0.0.1:47833/v1/health
curl --max-time 5 http://127.0.0.1:47833/v1/hello
curl --max-time 5 http://127.0.0.1:47833/v1/agents
curl --max-time 5 -i -H 'X-Agent-Relay-Protocol-Version: 2' http://127.0.0.1:47833/v1/hello
kill -TERM "$relayPid"
wait "$relayPid"
```

Integration tests run the daemon on `127.0.0.1:0`, allowing the OS to select an unused port. They require permission to create local listeners and do not call external services.

## Message transport

`POST /v1/messages` now accepts trusted peer delivery of regular messages and questions. See [message wire format, acknowledgment, validation, trust, timeouts and retry behavior](delivery.md). Response-specific convenience APIs and MCP remain deferred.
