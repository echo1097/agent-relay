# Test a complete conversation

Current trust setup: start both daemons, then run `agent-relay trust PEER_NODE_ID --home "$relayHome"` on each machine before following the message steps. Legacy configuration supplies routes only. See [trust setup](trust.md).

This procedure uses the actual CLI and daemon. No MCP or model provider is involved. Run the steps in order, alternating between A and B. Both agents are registered local sessions; you supply their example answers through the CLI.

## Install on the other test machine

Use macOS or Linux with Git, Go 1.25 or newer, Python 3, and `jq` on PATH. For two machines, install Tailscale on both, sign into the same tailnet, and make the `tailscale` command available on PATH. Tailnet policy and host firewalls must permit TCP 47832 between them. Do not open a public internet port.

On the other machine, clone and build:

```sh
git clone https://github.com/echo1097/agent-relay.git
cd agent-relay
python3 -m venv .venv
source .venv/bin/activate
export GOMODCACHE="$PWD/.cache/go-mod"
export GOCACHE="$PWD/.cache/go-build"
go build -o bin/agent-relay ./cmd/agent-relay
./bin/agent-relay version
```

On your current machine, stop any daemon using the binary you will replace, then update and rebuild from your existing checkout:

```sh
cd /Users/echo/Code/AgentRelay
git pull --ff-only
source .venv/bin/activate
export GOMODCACHE="$PWD/.cache/go-mod"
export GOCACHE="$PWD/.cache/go-build"
go build -o bin/agent-relay ./cmd/agent-relay
```

The binary is built for each machine's OS and CPU. Do not copy the SQLite database between machines: each machine needs its own node and agent identities. Restart the backend daemon after installing the new build. This procedure starts fresh test instances below.

## Prepare terminal A and terminal B

For localhost, open two terminals in the same repository. For two machines, use one terminal on each machine in its checkout.

In terminal A:

```sh
relayBin="$PWD/bin/agent-relay"
relayHome=$(mktemp -d "${TMPDIR:-/tmp}/relay-conversation-a.XXXXXX")
localAgent=$("$relayBin" agents register --home "$relayHome" --name agent-a --provider codex --task 'Debugging refresh token 401s')
localNode=$("$relayBin" agents get --home "$relayHome" --id "$localAgent" | jq -r .node_id)
printf 'A home: %s\nA agent: %s\nA node: %s\n' "$relayHome" "$localAgent" "$localNode"
```

In terminal B:

```sh
relayBin="$PWD/bin/agent-relay"
relayHome=$(mktemp -d "${TMPDIR:-/tmp}/relay-conversation-b.XXXXXX")
localAgent=$("$relayBin" agents register --home "$relayHome" --name agent-b --provider claude --task 'Refactoring authentication middleware')
localNode=$("$relayBin" agents get --home "$relayHome" --id "$localAgent" | jq -r .node_id)
printf 'B home: %s\nB agent: %s\nB node: %s\n' "$relayHome" "$localAgent" "$localNode"
```

Save the printed home paths to reopen these histories later. Each fresh registration creates a new session.

In terminal A, paste B's printed IDs:

```sh
peerAgent='PASTE_B_AGENT_ID'
peerNode='PASTE_B_NODE_ID'
```

In terminal B, paste A's printed IDs:

```sh
peerAgent='PASTE_A_AGENT_ID'
peerNode='PASTE_A_NODE_ID'
```

Choose one network setup.

### Option 1: two terminals on one machine

In A:

```sh
development=true
bindAddress=127.0.0.1
localPort=47931
peerAddress=127.0.0.1
peerPort=47932
```

In B:

```sh
development=true
bindAddress=127.0.0.1
localPort=47932
peerAddress=127.0.0.1
peerPort=47931
```

### Option 2: two machines over Tailscale

Run this on both machines and exchange the printed IPv4 addresses:

```sh
tailscale status
tailscale ip -4
```

In A:

```sh
development=false
bindAddress=tailscale
localPort=47832
peerAddress='PASTE_B_TAILSCALE_IPV4'
peerPort=47832
tailscale ping "$peerAddress"
```

In B:

```sh
development=false
bindAddress=tailscale
localPort=47832
peerAddress='PASTE_A_TAILSCALE_IPV4'
peerPort=47832
tailscale ping "$peerAddress"
```

Both production nodes must use the same port for automatic discovery. An existing Relay daemon on port 47832 must be stopped before starting this test instance.

## Configure and start both daemons

Run this block in both terminals after setting the variables above:

```sh
cat > "$relayHome/config.toml" <<TOML
[network]
development = $development
bind_address = "$bindAddress"
port = $localPort

[[trusted_peers]]
node_id = "$peerNode"
address = "$peerAddress"
port = $peerPort
TOML

"$relayBin" daemon --home "$relayHome" > "$relayHome/terminal.log" 2>&1 &
relayPid=$!
trap 'kill -TERM "$relayPid" 2>/dev/null; wait "$relayPid" 2>/dev/null' EXIT
```

After both are started, run this in both terminals:

```sh
"$relayBin" agents heartbeat --home "$relayHome" --id "$localAgent"
curl --noproxy '*' --fail --max-time 5 "http://$peerAddress:$peerPort/v1/health"
curl --noproxy '*' --fail --max-time 5 "http://$peerAddress:$peerPort/v1/agents" | jq .
```

Each remote agent list should contain `peerAgent`. For two Tailscale machines, also run `"$relayBin" peers --home "$relayHome"` after up to 15 seconds and confirm the other node is online. Localhost development does not simulate Tailscale peer discovery. The automated test supplies a fake tailnet and performs real HTTP hello probes.

If health fails, inspect `cat "$relayHome/terminal.log"` and check the IP, port, listener, and firewall. Before sending, run `agent-relay trust PEER_NODE_ID --home "$relayHome"` on both machines after both daemons are running. If delivery fails, inspect `trust-state` on both machines. Configuration entries alone do not grant trust. Restart after editing configuration. Presence may become offline after 30 seconds without heartbeats; registered offline agents still receive messages.

Define this helper in both terminals. It waits up to 45 seconds for the local persisted status:

```sh
waitMessage() {
  messageID=$1
  expectedStatus=$2
  attempt=0
  while [ "$attempt" -lt 45 ]; do
    currentStatus=$("$relayBin" messages get --home "$relayHome" --id "$messageID" 2>/dev/null | jq -r .Status)
    if [ "$currentStatus" = "$expectedStatus" ]; then
      return 0
    fi
    attempt=$((attempt + 1))
    sleep 1
  done
  printf 'Timed out waiting for %s to become %s\n' "$messageID" "$expectedStatus"
  return 1
}
```

## 1. A asks B

In A:

```sh
question=$("$relayBin" messages send --home "$relayHome" \
  --from "$localAgent" --to "$peerAgent" --peer "$peerNode" \
  --type question --text 'Did you change refresh token validation?')
questionID=$(printf '%s' "$question" | jq -r .ID)
conversationID=$(printf '%s' "$question" | jq -r .ConversationID)
waitMessage "$questionID" delivered
printf '%s\n' "$question" | jq .
```

Proceed only after `waitMessage` succeeds. Delivery means B committed the question before acknowledging it.

## 2. B receives and responds

In B:

```sh
inbox=$("$relayBin" messages inbox --home "$relayHome" --agent "$localAgent")
printf '%s\n' "$inbox" | jq .
questionID=$(printf '%s' "$inbox" | jq -r '.[0].ID')
conversationID=$(printf '%s' "$inbox" | jq -r '.[0].ConversationID')
answer=$("$relayBin" messages respond --home "$relayHome" \
  --from "$localAgent" --id "$questionID" \
  --text 'Yes, I changed the validation logic.')
answerID=$(printf '%s' "$answer" | jq -r .ID)
waitMessage "$answerID" delivered
"$relayBin" messages get --home "$relayHome" --id "$questionID" | jq '{ID, Status, AnsweredAt}'
```

The question is `answered` with a non-null `AnsweredAt`. The response command automatically derives the conversation, recipient and remote node from the question.

## 3. A receives the response and asks a follow-up

In A:

```sh
waitMessage "$questionID" answered
"$relayBin" messages inbox --home "$relayHome" --agent "$localAgent" | jq .
followup=$("$relayBin" messages send --home "$relayHome" \
  --from "$localAgent" --to "$peerAgent" --peer "$peerNode" \
  --conversation "$conversationID" --type question --text 'Was session_id added?')
followupID=$(printf '%s' "$followup" | jq -r .ID)
waitMessage "$followupID" delivered
```

A's inbox should contain the first response, with `ReplyTo` equal to `questionID`. The follow-up keeps the same `ConversationID`.

## 4. B responds again

In B:

```sh
inbox=$("$relayBin" messages inbox --home "$relayHome" --agent "$localAgent")
printf '%s\n' "$inbox" | jq .
followupID=$(printf '%s' "$inbox" | jq -r '[.[] | select(.Type == "question" and .Status != "answered")] | last | .ID')
secondAnswer=$("$relayBin" messages respond --home "$relayHome" \
  --from "$localAgent" --id "$followupID" --text 'Yes.')
secondAnswerID=$(printf '%s' "$secondAnswer" | jq -r .ID)
waitMessage "$secondAnswerID" delivered
```

## 5. Verify both histories and duplicate protection

In A:

```sh
waitMessage "$followupID" answered
"$relayBin" messages inbox --home "$relayHome" --agent "$localAgent" | jq .
```

There should be exactly two responses. In both terminals:

```sh
"$relayBin" messages history --home "$relayHome" --conversation "$conversationID" \
  | jq 'map({ID, ConversationID, Type, Text, ReplyTo, Status, AnsweredAt})'
```

Both histories must contain the same four message IDs in this order:

1. A's first question, `answered`.
2. B's first response, linked to the first question.
3. A's follow-up question, `answered`.
4. B's second response, linked to the follow-up.

Both questions have answered timestamps. Timestamps reflect each node's local processing time and need not match between machines. History sorts by creation time, with message ID as the tie-breaker; keep machine clocks synchronized.

In B, this must fail and leave the history at four messages:

```sh
"$relayBin" messages respond --home "$relayHome" \
  --from "$localAgent" --id "$questionID" --text 'A duplicate answer'
```

A newly submitted second answer is rejected. Automatic transport retries reuse the original response ID and are safe. An unanswered expired question rejects new answers as well.

## 6. Stop and check persistence

In both terminals:

```sh
kill -TERM "$relayPid"
wait "$relayPid"
trap - EXIT
"$relayBin" messages history --home "$relayHome" --conversation "$conversationID" | jq .
```

Both CLI commands must still show all four messages. To check daemon restart too, start each again with the same home directory, inspect history, then stop them again. Do not leave test daemons running.

## Automated validation

From the repository root:

```sh
source .venv/bin/activate
export GOMODCACHE="$PWD/.cache/go-mod"
export GOCACHE="$PWD/.cache/go-build"
go fmt ./...
go vet ./...
go test ./...
go build ./...
go build -o bin/agent-relay ./cmd/agent-relay
go test -race ./...
go test ./internal/daemon -run '^TestAgentRelayEndToEndConversation$' -count=1 -v
```

The required PRD section 56 test is `TestAgentRelayEndToEndConversation` in `internal/daemon/conversation_test.go`. It exercises discovery, HTTP agent listing, the two question/answer rounds, response links, answered state and timestamps, conversation update time, idempotent response retries, and the four ordered messages on both nodes after reopening SQLite. Separate tests cover invalid originals, membership violations, duplicate answers, CLI routing, and response redelivery after a lost acknowledgment and database restart.

The full validation suite and a real two-process localhost CLI exchange passed on September 6, 2026. Both manual test daemons were stopped. Live two-machine Tailscale verification still needs to be run on your machines. MCP remains unimplemented.
