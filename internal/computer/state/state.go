package state

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	stateDirectory = "computer"
	databaseName   = "state.db"
	lockName       = "state.lock"
)

//go:embed schema.sql
var schema string

type State struct {
	db       *sql.DB
	dir      string
	lockFile *os.File
}

type PairingAttempt struct {
	ServerURL       string
	PairingToken    string
	RequestID       string
	RegistrationKey string
	Name            string
	OS              string
	Arch            string
	CreatedAt       time.Time
}

type Identity struct {
	ServerURL       string
	ComputerID      string
	RegistrationKey string
	PairedAt        time.Time
}

type RuntimeSession struct {
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	Token                    string
	ExpiresAt                time.Time
	UpdatedAt                time.Time
}

type MutationOperation string

const (
	MutationRunClaim  MutationOperation = "run.claim"
	MutationRunRenew  MutationOperation = "run.renew"
	MutationRunCancel MutationOperation = "run.cancel"
)

type MutationStatus string

const (
	MutationPending   MutationStatus = "pending"
	MutationSucceeded MutationStatus = "succeeded"
	MutationFailed    MutationStatus = "failed"
)

type MutationAttempt struct {
	RequestID         string
	Operation         MutationOperation
	SubjectID         string
	PayloadHash       [sha256.Size]byte
	Status            MutationStatus
	RunID             string
	Attempt           uint64
	Fence             uint64
	ResponseAttempt   uint64
	ResponseFence     uint64
	ResponseExpiresAt *time.Time
	CreatedAt         time.Time
	CompletedAt       *time.Time
}

type CompletionOutcome string

const (
	CompletionSucceeded CompletionOutcome = "succeeded"
	CompletionFailed    CompletionOutcome = "failed"
)

type OutboxState string

const (
	OutboxPending   OutboxState = "pending"
	OutboxTombstone OutboxState = "tombstone"
)

type OutboxEvent struct {
	Sequence                 uint64
	OutboxEventID            string
	RequestID                string
	AgentID                  string
	PlacementDesiredRevision uint64
	RunID                    string
	Attempt                  uint64
	Fence                    uint64
	Outcome                  CompletionOutcome
	ErrorCode                string
	Body                     string
	UsageInputUnits          uint64
	UsageOutputUnits         uint64
	State                    OutboxState
	RejectionCode            string
	MentionedAgentIDs        []string
	CreatedAt                time.Time
	LastAttemptAt            *time.Time
	Attempts                 uint64
}

type RunJournal struct {
	AgentID                  string
	PlacementDesiredRevision uint64
	RunID                    string
	Attempt                  uint64
	Fence                    uint64
	State                    string
	StartedAt                time.Time
	FinishedAt               *time.Time
}

func Open(dataRoot string) (*State, error) {
	if dataRoot == "" {
		return nil, errors.New("computer state data root is required")
	}
	dataDirectory := filepath.Join(dataRoot, "data")
	directory := filepath.Join(dataDirectory, stateDirectory)
	for _, path := range []string{dataRoot, dataDirectory, directory} {
		if err := ensureDirectory(path); err != nil {
			return nil, err
		}
	}
	lockFile, err := openSecureFile(filepath.Join(directory, lockName))
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("lock computer state: %w", err)
	}
	databasePath := filepath.Join(directory, databaseName)
	if err := inspectExistingStateFiles(directory, databaseName+"-wal", databaseName+"-shm"); err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, err
	}
	databaseFile, err := openSecureFile(databasePath)
	if err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, err
	}
	if err := databaseFile.Close(); err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("close computer state database seed: %w", err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("open computer state: %w", err)
	}
	database.SetMaxOpenConns(1)
	state := &State{db: database, dir: directory, lockFile: lockFile}
	if err := state.configure(); err != nil {
		state.Close()
		return nil, err
	}
	if err := state.secureSQLiteFiles(); err != nil {
		state.Close()
		return nil, err
	}
	return state, nil
}

func (s *State) Close() error {
	var closeErrors []error
	if s.db != nil {
		if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			closeErrors = append(closeErrors, err)
		}
		if err := s.db.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	if s.lockFile != nil {
		if err := unix.Flock(int(s.lockFile.Fd()), unix.LOCK_UN); err != nil {
			closeErrors = append(closeErrors, err)
		}
		if err := s.lockFile.Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}
