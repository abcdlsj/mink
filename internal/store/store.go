package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

var migrationMutex sync.Mutex

type Store struct {
	db                   *sql.DB
	knowledgeCursorCodec knowledgeCursorCodec
}

func Open(path string) (*Store, error) {
	return openWithRandomReader(path, rand.Reader)
}

func openWithRandomReader(path string, random io.Reader) (*Store, error) {
	if err := secureSQLiteFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := configure(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := initializeSQLiteWAL(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := secureSQLiteFiles(path); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	key, err := bootstrapKnowledgeCursorKey(context.Background(), db, random)
	if err != nil {
		db.Close()
		return nil, err
	}
	cursorCodec, err := newKnowledgeCursorCodec(key, random)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize knowledge cursor codec: %w", ErrKnowledgeCursorKeyUnavailable)
	}
	if err := secureSQLiteFiles(path); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, knowledgeCursorCodec: cursorCodec}, nil
}

func secureSQLiteFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("secure sqlite: %w", err)
	}
	return secureSQLitePath(path)
}

func secureSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := secureSQLitePath(candidate); err != nil {
			return err
		}
	}
	return nil
}

func secureSQLitePath(path string) error {
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
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return nil
}

func initializeSQLiteWAL(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA user_version = user_version`); err != nil {
		return fmt.Errorf("initialize sqlite WAL: %w", err)
	}
	return nil
}

func migrate(db *sql.DB) error {
	migrationMutex.Lock()
	defer migrationMutex.Unlock()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("configure migrations: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}
	return nil
}
