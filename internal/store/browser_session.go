package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
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
	browserSessionHashDomain = "sumi-browser-session-v1\x00"
)

func (s *Store) CreateBrowserSession(ctx context.Context, command authorityapp.CreateBrowserSessionCommand) error {
	if command.Human.Kind != authoritydomain.PrincipalHuman || command.Human.ID == "" || command.Human.OrganizationID == "" ||
		!validBrowserToken(command.Token) || !command.ExpiresAt.After(command.Now) {
		return ErrBrowserSessionInvalid
	}
	h := browserTokenHash(browserSessionHashDomain, command.Token)
	r, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_sessions(token_hash, human_id, created_at, expires_at)
		SELECT ?, id, ?, ?
		FROM humans
		WHERE id = ? AND organization_id = ? AND status = 'active'
	`, h[:], unixNano(command.Now), unixNano(command.ExpiresAt), command.Human.ID, command.Human.OrganizationID)
	if err != nil {
		return fmt.Errorf("persist browser session: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("count browser session creation: %w", err)
	}
	if n != 1 {
		return ErrPermissionDenied
	}
	return nil
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
