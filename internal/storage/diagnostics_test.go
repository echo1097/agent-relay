package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticsPreserveState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	if _, err := Inspect(ctx, path); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("inspection created a database")
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	node, err := store.Node(ctx, "original")
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := Inspect(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspector.Close()
	if err := inspector.CheckIntegrity(ctx); err != nil {
		t.Fatal(err)
	}
	if err := inspector.CheckMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if err := CheckWritable(ctx, path); err != nil {
		t.Fatal(err)
	}
	saved, err := inspector.ExistingNode(ctx)
	if err != nil || saved != node {
		t.Fatalf("identity changed: %+v %v", saved, err)
	}
	if _, err := store.db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = 2"); err != nil {
		t.Fatal(err)
	}
	if err := inspector.CheckMigrations(ctx); err == nil {
		t.Fatal("migration gap accepted")
	}
	if _, err := inspector.db.ExecContext(ctx, "DELETE FROM local_node"); err == nil {
		t.Fatal("inspection allowed mutation")
	}
}
