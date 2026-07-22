package store

import (
	"context"
	"database/sql"
	"fmt"
)

func agentExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id = ?)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check agent identity: %w", err)
	}
	return exists, nil
}

func computerExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM computers WHERE id = ?)`, id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check computer identity: %w", err)
	}
	return exists, nil
}
