package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

var ErrCursorKeyUnavailable = errors.New("cursor key unavailable")

type cursorCodec struct {
	aead   cipher.AEAD
	random io.Reader
}

func newCursorCodec(key [32]byte, random io.Reader) (cursorCodec, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return cursorCodec{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return cursorCodec{}, err
	}
	if random == nil {
		return cursorCodec{}, errors.New("invalid cursor codec")
	}
	return cursorCodec{aead: aead, random: random}, nil
}

func bootstrapCursorKey(ctx context.Context, db *sql.DB, random io.Reader) ([32]byte, error) {
	if random == nil {
		return [32]byte{}, fmt.Errorf("initialize cursor key: %w", ErrCursorKeyUnavailable)
	}
	var candidate [32]byte
	if _, err := io.ReadFull(random, candidate[:]); err != nil {
		return [32]byte{}, fmt.Errorf("initialize cursor key: %w", ErrCursorKeyUnavailable)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return [32]byte{}, fmt.Errorf("initialize cursor key: %w", ErrCursorKeyUnavailable)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_cursor_keys(singleton, key) VALUES(1, ?) ON CONFLICT(singleton) DO NOTHING`, candidate[:]); err != nil {
		return [32]byte{}, fmt.Errorf("initialize cursor key: %w", ErrCursorKeyUnavailable)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT key FROM work_cursor_keys WHERE singleton = 1`).Scan(&raw); err != nil || len(raw) != 32 {
		return [32]byte{}, fmt.Errorf("initialize cursor key: %w", ErrCursorKeyUnavailable)
	}
	var key [32]byte
	copy(key[:], raw)
	if err := tx.Commit(); err != nil {
		return [32]byte{}, fmt.Errorf("initialize cursor key: %w", ErrCursorKeyUnavailable)
	}
	return key, nil
}
