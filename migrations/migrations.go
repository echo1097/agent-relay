package migrations

type Migration struct {
	Version int
	SQL     string
}

func All() []Migration {
	return []Migration{{Version: 1, SQL: `
CREATE TABLE nodes (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 tailscale_ip TEXT,
 trust_state TEXT NOT NULL,
 last_seen_at TEXT
);
CREATE TABLE local_node (
 singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
 node_id TEXT NOT NULL UNIQUE REFERENCES nodes(id)
);
`}, {Version: 2, SQL: `
CREATE TABLE agents (
 id TEXT PRIMARY KEY,
 node_id TEXT NOT NULL REFERENCES nodes(id),
 display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
 provider TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK (status IN ('online', 'busy', 'idle', 'offline')),
 task TEXT NOT NULL DEFAULT '',
 project TEXT NOT NULL DEFAULT '',
 repository TEXT NOT NULL DEFAULT '',
 branch TEXT NOT NULL DEFAULT '',
 cwd TEXT NOT NULL DEFAULT '',
 files TEXT NOT NULL DEFAULT '[]',
 registered_at TEXT NOT NULL,
 last_seen_at TEXT NOT NULL
);
CREATE INDEX agents_presence ON agents(node_id, status, last_seen_at);
`}, {Version: 3, SQL: `
CREATE TABLE conversations (
 id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
 local_agent_id TEXT NOT NULL REFERENCES agents(id),
 remote_agent_id TEXT NOT NULL CHECK (length(trim(remote_agent_id)) > 0),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 CHECK (local_agent_id != remote_agent_id)
);
CREATE INDEX conversations_local ON conversations(local_agent_id, created_at, id);
CREATE TRIGGER conversations_immutable BEFORE UPDATE OF id, local_agent_id, remote_agent_id ON conversations
BEGIN SELECT RAISE(ABORT, 'conversation participants are immutable'); END;
CREATE TABLE messages (
 id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 sender_agent_id TEXT NOT NULL,
 recipient_agent_id TEXT NOT NULL,
 type TEXT NOT NULL CHECK (type IN ('message', 'question', 'response')),
 text TEXT NOT NULL CHECK (length(trim(text)) > 0),
 reply_to TEXT REFERENCES messages(id),
 status TEXT NOT NULL CHECK (status IN ('created', 'sending', 'delivered', 'pending', 'answered', 'failed', 'expired', 'pending_delivery')),
 created_at TEXT NOT NULL,
 expires_at TEXT,
 delivered_at TEXT,
 received_at TEXT,
 read_at TEXT,
 answered_at TEXT,
 CHECK (sender_agent_id != recipient_agent_id),
 CHECK ((type = 'response') = (reply_to IS NOT NULL)),
 CHECK (expires_at IS NULL OR (type = 'question' AND expires_at > created_at)),
 CHECK (type = 'question' OR status NOT IN ('pending', 'answered', 'expired'))
);
CREATE INDEX messages_history ON messages(conversation_id, created_at, id);
CREATE INDEX messages_inbox ON messages(recipient_agent_id, received_at, created_at, id);
CREATE INDEX messages_expiration ON messages(expires_at, status);
CREATE TRIGGER messages_participants BEFORE INSERT ON messages
WHEN NOT EXISTS (SELECT 1 FROM conversations WHERE id = NEW.conversation_id AND
 ((local_agent_id = NEW.sender_agent_id AND remote_agent_id = NEW.recipient_agent_id) OR
 (remote_agent_id = NEW.sender_agent_id AND local_agent_id = NEW.recipient_agent_id)))
BEGIN SELECT RAISE(ABORT, 'message participants do not match conversation'); END;
CREATE TRIGGER messages_immutable BEFORE UPDATE OF id, conversation_id, sender_agent_id, recipient_agent_id, type, text, reply_to, created_at, expires_at ON messages
BEGIN SELECT RAISE(ABORT, 'message content is immutable'); END;
CREATE TABLE processed_messages (
 message_id TEXT PRIMARY KEY NOT NULL REFERENCES messages(id),
 processed_at TEXT NOT NULL
);
`}}
}
