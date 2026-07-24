package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
)

func validBrowserToken(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

const (
	browserHandoffHashDomain = "sumi-browser-handoff-v1\x00"
	browserSessionHashDomain = "sumi-browser-session-v1\x00"
)

type CreateBrowserHandoffParams = authorityapp.CreateBrowserHandoffCommand

type ConsumeBrowserHandoffParams = authorityapp.ConsumeBrowserHandoffCommand

func (s *Store) CreateBrowserHandoff(ctx context.Context, params CreateBrowserHandoffParams) error {
	if params.Human.Kind != "human" || params.Human.ID == "" || params.Human.OrganizationID == "" || !params.ExpiresAt.After(params.Now) || !validBrowserToken(params.Token) {
		return ErrBrowserSessionInvalid
	}
	h := browserTokenHash(browserHandoffHashDomain, params.Token)
	r, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_handoffs(token_hash, human_id, created_at, expires_at)
		SELECT ?, id, ?, ?
		FROM humans
		WHERE id = ? AND organization_id = ? AND status = 'active'
	`, h[:], unixNano(params.Now), unixNano(params.ExpiresAt), params.Human.ID, params.Human.OrganizationID)
	if err != nil {
		return fmt.Errorf("persist browser handoff: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("count browser handoff: %w", err)
	}
	if n != 1 {
		return ErrPermissionDenied
	}
	return nil
}

func (s *Store) ConsumeBrowserHandoff(ctx context.Context, params ConsumeBrowserHandoffParams) (Principal, error) {
	if !params.SessionExpiresAt.After(params.Now) || !validBrowserToken(params.HandoffToken) || !validBrowserToken(params.SessionToken) {
		return Principal{}, ErrBrowserSessionInvalid
	}
	hh := browserTokenHash(browserHandoffHashDomain, params.HandoffToken)
	sh := browserTokenHash(browserSessionHashDomain, params.SessionToken)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Principal{}, fmt.Errorf("begin browser handoff consumption: %w", err)
	}
	defer tx.Rollback()

	var p Principal
	err = tx.QueryRowContext(ctx, `
		SELECT 'human', h.id, h.organization_id
		FROM browser_handoffs bh
		JOIN humans h ON h.id = bh.human_id
		WHERE bh.token_hash = ?
		  AND bh.consumed_at IS NULL
		  AND bh.expires_at > ?
		  AND h.status = 'active'
	`, hh[:], unixNano(params.Now)).Scan(&p.Kind, &p.ID, &p.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrBrowserHandoffInvalid
	}
	if err != nil {
		return Principal{}, fmt.Errorf("read browser handoff: %w", err)
	}
	r, err := tx.ExecContext(ctx, `
		UPDATE browser_handoffs
		SET consumed_at = ?
		WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?
	`, unixNano(params.Now), hh[:], unixNano(params.Now))
	if err != nil {
		return Principal{}, fmt.Errorf("consume browser handoff: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return Principal{}, fmt.Errorf("count browser handoff consumption: %w", err)
	}
	if n != 1 {
		return Principal{}, ErrBrowserHandoffInvalid
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_sessions(token_hash, human_id, created_at, expires_at)
		VALUES(?, ?, ?, ?)
	`, sh[:], p.ID, unixNano(params.Now), unixNano(params.SessionExpiresAt)); err != nil {
		return Principal{}, fmt.Errorf("persist browser session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Principal{}, fmt.Errorf("commit browser handoff consumption: %w", err)
	}
	return p, nil
}

func (s *Store) AuthenticateBrowserSession(ctx context.Context, token string, now time.Time) (Principal, error) {
	if !validBrowserToken(token) {
		return Principal{}, ErrPermissionDenied
	}
	h := browserTokenHash(browserSessionHashDomain, token)
	var p Principal
	err := s.db.QueryRowContext(ctx, `
		SELECT 'human', h.id, h.organization_id
		FROM browser_sessions bs
		JOIN humans h ON h.id = bs.human_id
		WHERE bs.token_hash = ?
		  AND bs.revoked_at IS NULL
		  AND bs.expires_at > ?
		  AND h.status = 'active'
	`, h[:], unixNano(now)).Scan(&p.Kind, &p.ID, &p.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrPermissionDenied
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate browser session: %w", err)
	}
	return p, nil
}

func (s *Store) RevokeBrowserSession(ctx context.Context, token string, now time.Time) error {
	if !validBrowserToken(token) {
		return ErrPermissionDenied
	}
	h := browserTokenHash(browserSessionHashDomain, token)
	r, err := s.db.ExecContext(ctx, `
		UPDATE browser_sessions
		SET revoked_at = ?
		WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
	`, unixNano(now), h[:], unixNano(now))
	if err != nil {
		return fmt.Errorf("revoke browser session: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("count browser session revocation: %w", err)
	}
	if n != 1 {
		return ErrPermissionDenied
	}
	return nil
}

func browserTokenHash(domain, token string) [32]byte {
	return sha256.Sum256([]byte(domain + token))
}
