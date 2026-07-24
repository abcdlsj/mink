package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (s *State) configure() error {
	for _, statement := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = FULL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("configure computer state: %w", err)
		}
	}
	var objects int
	if err := s.db.QueryRow(`
		SELECT count(*) FROM sqlite_master
		WHERE type IN ('table', 'index', 'trigger', 'view') AND name NOT LIKE 'sqlite_%'
	`).Scan(&objects); err != nil {
		return fmt.Errorf("inspect computer state schema: %w", err)
	}
	if objects == 0 {
		if _, err := s.db.Exec(schema); err != nil {
			return fmt.Errorf("initialize computer state schema: %w", err)
		}
		return nil
	}
	var marker string
	if err := s.db.QueryRow(`SELECT value FROM state_metadata WHERE key = 'schema_version'`).Scan(&marker); err != nil || marker != "next-greenfield-1" {
		return errors.New("computer state schema is incompatible; initialize a new data root")
	}
	for _, table := range []string{"computer_identity", "credential_delivery_keys", "credential_bindings", "runtime_sessions", "mutation_attempts", "outbox_events", "run_journals", "tool_results"} {
		var found int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&found); err != nil || found != 1 {
			return errors.New("computer state schema is incomplete")
		}
	}
	return nil
}

func (s *State) secureSQLiteFiles() error {
	for _, name := range []string{databaseName, databaseName + "-wal", databaseName + "-shm", lockName} {
		path := filepath.Join(s.dir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect computer state file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("computer state file %s is not regular", name)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("secure computer state file: %w", err)
		}
	}
	return nil
}

func ensureDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create computer state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect computer state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("computer state path is not a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure computer state directory: %w", err)
	}
	return nil
}

func openSecureFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, errors.New("computer state file is not regular")
		}
		if info.Mode().Perm() != 0o600 {
			return nil, fmt.Errorf("computer state file mode is %o, want 600", info.Mode().Perm())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect computer state file: %w", err)
	}
	descriptor, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open computer state file: %w", err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	after, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect open computer state file: %w", err)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(after, current) || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || current.Mode().Perm() != 0o600 {
		file.Close()
		return nil, errors.New("computer state file changed while opening")
	}
	return file, nil
}

func inspectExistingStateFiles(directory string, names ...string) error {
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(directory, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect computer state file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("computer state file %s is not regular", name)
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("computer state file %s mode is %o, want 600", name, info.Mode().Perm())
		}
	}
	return nil
}
