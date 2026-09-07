package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	"agent-relay/migrations"
)

func Inspect(ctx context.Context, path string) (*Store, error) {
	databaseURL := url.URL{Scheme: "file", Path: path}
	query := databaseURL.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(1000)")
	databaseURL.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Store{db: db}, nil
}

func (store *Store) CheckIntegrity(ctx context.Context) error {
	var result string
	if err := store.db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("SQLite integrity check failed; preserve the database and restore a verified backup")
	}
	rows, err := store.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("SQLite foreign key check failed")
	}
	return rows.Err()
}

func CheckWritable(ctx context.Context, path string) (returnErr error) {
	databaseURL := url.URL{Scheme: "file", Path: path}
	query := databaseURL.Query()
	query.Set("mode", "rw")
	query.Add("_pragma", "busy_timeout(1000)")
	databaseURL.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", databaseURL.String())
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		err := tx.Rollback()
		if !errors.Is(err, sql.ErrTxDone) {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	_, err = tx.ExecContext(ctx, "UPDATE nodes SET name = name WHERE id IN (SELECT node_id FROM local_node)")
	return err
}

func (store *Store) ExistingNode(ctx context.Context) (Node, error) {
	var node Node
	err := store.db.QueryRowContext(ctx, "SELECT nodes.id, nodes.name FROM nodes JOIN local_node ON nodes.id = local_node.node_id WHERE singleton = 1").Scan(&node.ID, &node.Name)
	return node, err
}

func (store *Store) CheckMigrations(ctx context.Context) error {
	var count, version int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&count, &version); err != nil {
		return err
	}
	if count != len(migrations.All()) || version != count {
		return fmt.Errorf("migration history is incomplete or incompatible: %d entries, latest %d, expected %d", count, version, len(migrations.All()))
	}
	var invalid int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version < 1").Scan(&invalid); err != nil {
		return err
	}
	if invalid != 0 {
		return errors.New("invalid migration history")
	}
	return nil
}

func (store *Store) AgentCount(ctx context.Context, nodeID string) (int, error) {
	var count int
	err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents WHERE node_id = ?", nodeID).Scan(&count)
	return count, err
}
