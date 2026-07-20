package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Computer struct {
	ID         string
	Name       string
	OS         string
	Arch       string
	CreatedAt  time.Time
	LastSeenAt time.Time
}

type RegisterComputerParams struct {
	RegistrationKey string
	Name            string
	OS              string
	Arch            string
	Now             time.Time
}

func (s *Store) RegisterComputer(ctx context.Context, params RegisterComputerParams) (Computer, error) {
	stamp := unixNano(params.Now)
	keyHash := sha256.Sum256([]byte(params.RegistrationKey))
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(registration_key_hash) DO UPDATE SET
			name = excluded.name,
			os = excluded.os,
			arch = excluded.arch,
			last_seen_at = max(computers.last_seen_at, excluded.last_seen_at)
		RETURNING id, name, os, arch, created_at, last_seen_at
	`, uuid.NewString(), keyHash[:], params.Name, params.OS, params.Arch, stamp, stamp)
	computer, err := scanComputer(row)
	if err != nil {
		return Computer{}, fmt.Errorf("register computer: %w", err)
	}
	return computer, nil
}

func (s *Store) HeartbeatComputer(ctx context.Context, id, registrationKey string, now time.Time) (Computer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Computer{}, fmt.Errorf("begin computer heartbeat: %w", err)
	}
	defer tx.Rollback()

	keyHash := sha256.Sum256([]byte(registrationKey))
	row := tx.QueryRowContext(ctx, `
		UPDATE computers
		SET last_seen_at = max(last_seen_at, ?)
		WHERE id = ? AND registration_key_hash = ?
		RETURNING id, name, os, arch, created_at, last_seen_at
	`, unixNano(now), id, keyHash[:])
	computer, err := scanComputer(row)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Computer{}, fmt.Errorf("commit computer heartbeat: %w", err)
		}
		return computer, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Computer{}, fmt.Errorf("heartbeat computer: %w", err)
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM computers WHERE id = ?)", id).Scan(&exists); err != nil {
		return Computer{}, fmt.Errorf("check computer identity: %w", err)
	}
	if !exists {
		return Computer{}, ErrComputerNotFound
	}
	return Computer{}, ErrRegistrationKeyMismatch
}

func (s *Store) GetComputer(ctx context.Context, id string) (Computer, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, os, arch, created_at, last_seen_at
		FROM computers
		WHERE id = ?
	`, id)
	computer, err := scanComputer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Computer{}, ErrComputerNotFound
	}
	if err != nil {
		return Computer{}, fmt.Errorf("get computer: %w", err)
	}
	return computer, nil
}

func (s *Store) ListComputers(ctx context.Context) ([]Computer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, os, arch, created_at, last_seen_at
		FROM computers
		ORDER BY name, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list computers: %w", err)
	}
	defer rows.Close()

	var computers []Computer
	for rows.Next() {
		computer, err := scanComputer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan computer: %w", err)
		}
		computers = append(computers, computer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate computers: %w", err)
	}
	return computers, nil
}

type scanner interface {
	Scan(...any) error
}

func scanComputer(row scanner) (Computer, error) {
	var computer Computer
	var createdAt, lastSeenAt int64
	if err := row.Scan(&computer.ID, &computer.Name, &computer.OS, &computer.Arch, &createdAt, &lastSeenAt); err != nil {
		return Computer{}, err
	}
	computer.CreatedAt = timeFromUnixNano(createdAt)
	computer.LastSeenAt = timeFromUnixNano(lastSeenAt)
	return computer, nil
}
