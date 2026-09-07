package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"agent-relay/internal/agents"
	"agent-relay/migrations"
)

func TestIdentityPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	var firstNode Node
	for index := range 2 {
		store, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		node, err := store.Node(ctx, "machine")
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			firstNode = node
		} else if node != firstNode {
			t.Fatalf("identity changed: %+v", node)
		}
		if !regexp.MustCompile(`^node_[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(node.ID) {
			t.Fatalf("invalid UUIDv7: %s", node.ID)
		}
		version, err := store.SchemaVersion(ctx)
		if err != nil || version != len(migrations.All()) {
			t.Fatalf("schema: %d, %v", version, err)
		}
		var integrity string
		if err := store.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
			t.Fatalf("integrity: %s, %v", integrity, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationsRollbackAndRejectFuture(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	steps := append(migrations.All(), migrations.Migration{Version: len(migrations.All()) + 1, SQL: "CREATE TABLE rollback_probe (id INTEGER); INVALID SQL;"})
	if err := store.migrate(ctx, steps); err == nil {
		t.Fatal("expected migration failure")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name = 'rollback_probe'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("migration was not rolled back: %d, %v", count, err)
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil || version != len(migrations.All()) {
		t.Fatalf("version changed: %d, %v", version, err)
	}
	if _, err := store.db.Exec("INSERT INTO schema_migrations VALUES (99, 'future')"); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(ctx, migrations.All()); err == nil {
		t.Fatal("accepted future schema")
	}
}

func TestConcurrentInitialization(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	var workers sync.WaitGroup
	nodeIDs := make(chan string, 4)
	for range 4 {
		workers.Go(func() {
			store, err := Open(ctx, path)
			if err != nil {
				t.Error(err)
				return
			}
			defer store.Close()
			node, err := store.Node(ctx, "machine")
			if err != nil {
				t.Error(err)
				return
			}
			nodeIDs <- node.ID
		})
	}
	workers.Wait()
	close(nodeIDs)
	firstID := ""
	for nodeID := range nodeIDs {
		if firstID == "" {
			firstID = nodeID
		}
		if firstID != nodeID {
			t.Fatal("concurrent launches created different identities")
		}
	}
}

func TestUpgradeFromFoundation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	initialStore := &Store{db: db}
	if err := initialStore.migrate(ctx, migrations.All()[:1]); err != nil {
		t.Fatal(err)
	}
	node, err := initialStore.Node(ctx, "original")
	if err != nil {
		t.Fatal(err)
	}
	if err := initialStore.Close(); err != nil {
		t.Fatal(err)
	}
	upgradedStore, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgradedStore.Close()
	upgradedNode, err := upgradedStore.Node(ctx, "changed")
	if err != nil || upgradedNode != node {
		t.Fatalf("node changed during upgrade: %+v, %v", upgradedNode, err)
	}
	registry, err := agents.New(upgradedStore, node.ID, agents.Options{OfflineAfter: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(ctx, agents.Registration{DisplayName: "new-agent"}); err != nil {
		t.Fatal(err)
	}
}
