package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var browserOpaqueTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

const (
	browserHandoffHashDomain = "sumi-browser-handoff-v1\x00"
	browserSessionHashDomain = "sumi-browser-session-v1\x00"
)

type CreateBrowserHandoffParams struct {
	Human     Principal
	Token     string
	Now       time.Time
	ExpiresAt time.Time
}

type ConsumeBrowserHandoffParams struct {
	HandoffToken     string
	SessionToken     string
	Now              time.Time
	SessionExpiresAt time.Time
}

func (s *Store) CreateBrowserHandoff(ctx context.Context, params CreateBrowserHandoffParams) error {
	if params.Human.Kind != "human" || params.Human.ID == "" || params.Human.OrganizationID == "" || !params.ExpiresAt.After(params.Now) || !browserOpaqueTokenPattern.MatchString(params.Token) {
		return ErrBrowserSessionInvalid
	}
	hash := browserTokenHash(browserHandoffHashDomain, params.Token)
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_handoffs(token_hash, human_id, created_at, expires_at)
		SELECT ?, id, ?, ?
		FROM humans
		WHERE id = ? AND organization_id = ? AND status = 'active'
	`, hash[:], unixNano(params.Now), unixNano(params.ExpiresAt), params.Human.ID, params.Human.OrganizationID)
	if err != nil {
		return fmt.Errorf("persist browser handoff: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count browser handoff: %w", err)
	}
	if created != 1 {
		return ErrPermissionDenied
	}
	return nil
}

func (s *Store) ConsumeBrowserHandoff(ctx context.Context, params ConsumeBrowserHandoffParams) (Principal, error) {
	if !params.SessionExpiresAt.After(params.Now) || !browserOpaqueTokenPattern.MatchString(params.HandoffToken) || !browserOpaqueTokenPattern.MatchString(params.SessionToken) {
		return Principal{}, ErrBrowserSessionInvalid
	}
	handoffHash := browserTokenHash(browserHandoffHashDomain, params.HandoffToken)
	sessionHash := browserTokenHash(browserSessionHashDomain, params.SessionToken)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Principal{}, fmt.Errorf("begin browser handoff consumption: %w", err)
	}
	defer tx.Rollback()

	var principal Principal
	err = tx.QueryRowContext(ctx, `
		SELECT 'human', h.id, h.organization_id
		FROM browser_handoffs bh
		JOIN humans h ON h.id = bh.human_id
		WHERE bh.token_hash = ?
		  AND bh.consumed_at IS NULL
		  AND bh.expires_at > ?
		  AND h.status = 'active'
	`, handoffHash[:], unixNano(params.Now)).Scan(&principal.Kind, &principal.ID, &principal.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrBrowserHandoffInvalid
	}
	if err != nil {
		return Principal{}, fmt.Errorf("read browser handoff: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE browser_handoffs
		SET consumed_at = ?
		WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?
	`, unixNano(params.Now), handoffHash[:], unixNano(params.Now))
	if err != nil {
		return Principal{}, fmt.Errorf("consume browser handoff: %w", err)
	}
	consumed, err := result.RowsAffected()
	if err != nil {
		return Principal{}, fmt.Errorf("count browser handoff consumption: %w", err)
	}
	if consumed != 1 {
		return Principal{}, ErrBrowserHandoffInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_sessions(token_hash, human_id, created_at, expires_at)
		VALUES(?, ?, ?, ?)
	`, sessionHash[:], principal.ID, unixNano(params.Now), unixNano(params.SessionExpiresAt)); err != nil {
		return Principal{}, fmt.Errorf("persist browser session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Principal{}, fmt.Errorf("commit browser handoff consumption: %w", err)
	}
	return principal, nil
}

func (s *Store) AuthenticateBrowserSession(ctx context.Context, token string, now time.Time) (Principal, error) {
	if !browserOpaqueTokenPattern.MatchString(token) {
		return Principal{}, ErrPermissionDenied
	}
	hash := browserTokenHash(browserSessionHashDomain, token)
	var principal Principal
	err := s.db.QueryRowContext(ctx, `
		SELECT 'human', h.id, h.organization_id
		FROM browser_sessions bs
		JOIN humans h ON h.id = bs.human_id
		WHERE bs.token_hash = ?
		  AND bs.revoked_at IS NULL
		  AND bs.expires_at > ?
		  AND h.status = 'active'
	`, hash[:], unixNano(now)).Scan(&principal.Kind, &principal.ID, &principal.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrPermissionDenied
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate browser session: %w", err)
	}
	return principal, nil
}

func (s *Store) RevokeBrowserSession(ctx context.Context, token string, now time.Time) error {
	if !browserOpaqueTokenPattern.MatchString(token) {
		return ErrPermissionDenied
	}
	hash := browserTokenHash(browserSessionHashDomain, token)
	result, err := s.db.ExecContext(ctx, `
		UPDATE browser_sessions
		SET revoked_at = ?
		WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
	`, unixNano(now), hash[:], unixNano(now))
	if err != nil {
		return fmt.Errorf("revoke browser session: %w", err)
	}
	revoked, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count browser session revocation: %w", err)
	}
	if revoked != 1 {
		return ErrPermissionDenied
	}
	return nil
}

func browserTokenHash(domain, token string) [32]byte {
	return sha256.Sum256([]byte(domain + token))
}
