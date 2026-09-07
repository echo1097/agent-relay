package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"agent-relay/migrations"
	"modernc.org/sqlite"
	"modernc.org/sqlite/lib"
)

type Store struct{ db *sql.DB }

type Node struct {
	ID   string
	Name string
}

func Open(ctx context.Context, path string) (*Store, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	databaseURL := url.URL{Scheme: "file", Path: path}
	query := databaseURL.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "synchronous(FULL)")
	databaseURL.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.enableWAL(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := store.migrate(ctx, migrations.All()); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return store, nil
}

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) enableWAL(ctx context.Context) error {
	startupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		_, err := store.db.ExecContext(startupCtx, "PRAGMA journal_mode=WAL")
		if err == nil {
			return nil
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_BUSY {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-startupCtx.Done():
			timer.Stop()
			return startupCtx.Err()
		case <-timer.C:
		}
	}
}

func (store *Store) migrate(ctx context.Context, steps []migrations.Migration) (returnErr error) {
	conn, err := store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var currentVersion int
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion); err != nil {
		return err
	}
	if currentVersion > len(steps) {
		return fmt.Errorf("database schema %d is newer than supported schema %d", currentVersion, len(steps))
	}
	for index, step := range steps {
		if step.Version != index+1 {
			return errors.New("migrations must have consecutive versions")
		}
		if step.Version <= currentVersion {
			continue
		}
		if _, err := conn.ExecContext(ctx, step.SQL); err != nil {
			return fmt.Errorf("migration %d: %w", step.Version, err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", step.Version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func (store *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := store.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	return version, err
}

func (store *Store) Node(ctx context.Context, name string) (node Node, returnErr error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, err
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if !errors.Is(rollbackErr, sql.ErrTxDone) {
			returnErr = errors.Join(returnErr, rollbackErr)
		}
	}()
	nodeID, err := newNodeID()
	if err != nil {
		return Node{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO nodes (id, name, trust_state) SELECT ?, ?, 'trusted' WHERE NOT EXISTS (SELECT 1 FROM local_node)", nodeID, name)
	if err != nil {
		return Node{}, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO local_node (singleton, node_id) VALUES (1, ?) ON CONFLICT(singleton) DO NOTHING", nodeID)
	if err != nil {
		return Node{}, err
	}
	err = tx.QueryRowContext(ctx, "SELECT nodes.id, nodes.name FROM nodes JOIN local_node ON nodes.id = local_node.node_id WHERE singleton = 1").Scan(&node.ID, &node.Name)
	if err != nil {
		return Node{}, err
	}
	return node, tx.Commit()
}

func newNodeID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	timestamp := time.Now().UnixMilli()
	for index := 5; index >= 0; index-- {
		data[index] = byte(timestamp)
		timestamp >>= 8
	}
	data[6] = (data[6] & 0x0f) | 0x70
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("node_%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}
