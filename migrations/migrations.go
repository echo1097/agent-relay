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
`}}
}
