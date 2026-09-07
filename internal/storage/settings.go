package storage

import (
	"context"
	"errors"
)

var ErrDnd = errors.New("DO_NOT_DISTURB: this computer is not accepting new requests")

func (store *Store) Dnd(ctx context.Context) (bool, error) {
	var enabled bool
	err := store.db.QueryRowContext(ctx, "SELECT dnd FROM local_settings WHERE id = 1").Scan(&enabled)
	return enabled, err
}

func (store *Store) ToggleDnd(ctx context.Context) (bool, error) {
	var enabled bool
	err := store.db.QueryRowContext(ctx, "UPDATE local_settings SET dnd = 1 - dnd WHERE id = 1 RETURNING dnd").Scan(&enabled)
	return enabled, err
}
