package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := configure(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure sqlite: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ServerID(ctx context.Context) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin server identity transaction: %w", err)
	}
	defer tx.Rollback()

	candidate := uuid.NewString()
	if _, err := tx.ExecContext(ctx, "INSERT INTO system_metadata(key, value) VALUES('server_id', ?) ON CONFLICT(key) DO NOTHING", candidate); err != nil {
		return "", fmt.Errorf("persist server identity: %w", err)
	}

	var id string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM system_metadata WHERE key = 'server_id'").Scan(&id); err != nil {
		return "", fmt.Errorf("read server identity: %w", err)
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("invalid persisted server identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit server identity: %w", err)
	}
	return id, nil
}

func configure(db *sql.DB) error {
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return nil
}

func migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("configure migrations: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	return nil
}
