package storage

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

func TestDndPersistenceAndConcurrentToggle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "relay.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if enabled, err := store.Dnd(ctx); err != nil || enabled {
		t.Fatalf("default: %v %v", enabled, err)
	}
	if enabled, err := store.ToggleDnd(ctx); err != nil || !enabled {
		t.Fatalf("toggle: %v %v", enabled, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if enabled, err := other.Dnd(ctx); err != nil || !enabled {
		t.Fatalf("persisted: %v %v", enabled, err)
	}
	var workers sync.WaitGroup
	for _, currentStore := range []*Store{store, other} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, err := currentStore.ToggleDnd(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if enabled, err := store.Dnd(ctx); err != nil || !enabled {
		t.Fatalf("lost toggle: %v %v", enabled, err)
	}
}
