package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sync"

	_ "embed"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

var schemaMu sync.Mutex

type Store struct {
	db          *sql.DB
	cursorCodec cursorCodec
}

func Open(path string) (*Store, error) {
	return openWithRand(path, rand.Reader)
}

func openWithRand(path string, random io.Reader) (*Store, error) {
	return openWithOpts(path, random, (*sql.DB).Close)
}

func openWithOpts(path string, random io.Reader, closeDB func(*sql.DB) error) (*Store, error) {
	if err := secureFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := configureDB(db); err != nil {
		closeDB(db)
		return nil, err
	}
	if err := initWAL(db); err != nil {
		closeDB(db)
		return nil, err
	}
	if err := secureDBFiles(path); err != nil {
		closeDB(db)
		return nil, err
	}
	if err := initSchema(context.Background(), db); err != nil {
		closeDB(db)
		return nil, err
	}
	key, err := bootstrapCursorKey(context.Background(), db, random)
	if err != nil {
		closeDB(db)
		return nil, err
	}
	cursorCodec, err := newCursorCodec(key, random)
	if err != nil {
		closeDB(db)
		return nil, fmt.Errorf("initialize cursor codec: %w", ErrCursorKeyUnavailable)
	}
	if err := secureDBFiles(path); err != nil {
		closeDB(db)
		return nil, err
	}
	return &Store{db: db, cursorCodec: cursorCodec}, nil
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
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO system_metadata(key, value) VALUES('server_id', ?) ON CONFLICT(key) DO NOTHING",
		candidate,
	); err != nil {
		return "", fmt.Errorf("persist server identity: %w", err)
	}
	var id string
	if err := tx.QueryRowContext(ctx,
		"SELECT value FROM system_metadata WHERE key = 'server_id'",
	).Scan(&id); err != nil {
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

// ── Setup helpers ────────────────────────────────────────────

func configureDB(db *sql.DB) error {
	for _, stmt := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return nil
}

func initWAL(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA user_version = user_version`); err != nil {
		return fmt.Errorf("initialize sqlite WAL: %w", err)
	}
	return nil
}

func initSchema(ctx context.Context, db *sql.DB) error {
	schemaMu.Lock()
	defer schemaMu.Unlock()

	var objects int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type IN ('table', 'index', 'trigger', 'view')
		  AND name NOT LIKE 'sqlite_%'
	`).Scan(&objects); err != nil {
		return fmt.Errorf("inspect sqlite schema: %w", err)
	}
	if objects != 0 {
		var marker string
		if err := db.QueryRowContext(ctx,
			"SELECT value FROM system_metadata WHERE key = 'schema_version'",
		).Scan(&marker); err != nil || marker != "next-greenfield-2" {
			return fmt.Errorf("sqlite schema is incompatible; initialize a new database")
		}
		return validateSchema(ctx, db)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite schema initialization: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite schema initialization: %w", err)
	}
	return nil
}

func validateSchema(ctx context.Context, db *sql.DB) error {
	var objects int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name IN (
			'system_metadata', 'work_cursor_keys', 'knowledge_dirty_sources',
			'knowledge_fts', 'knowledge_index_state', 'knowledge_projection_rows',
			'auth_identities', 'local_password_credentials', 'browser_sessions'
		)
	`).Scan(&objects); err != nil {
		return fmt.Errorf("inspect sqlite schema: %w", err)
	}
	if objects != 9 {
		return fmt.Errorf("sqlite schema is incomplete")
	}
	return nil
}

// ── File security ────────────────────────────────────────────

func secureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	return securePath(path)
}

func secureDBFiles(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := securePath(p); err != nil {
			return err
		}
	}
	return nil
}

func securePath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secure sqlite: %s is not a regular file", path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	info, err = os.Stat(path)
	if err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("secure sqlite: %s has unsafe permissions", path)
	}
	return nil
}
