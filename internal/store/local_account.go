package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/google/uuid"
)

const (
	localIdentityProvider = "local"
	argon2IDAlgorithm     = "argon2id"
)

func (s *Store) LocalAccountSetupRequired(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM auth_identities
		WHERE provider = ?
	`, localIdentityProvider).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect local account setup: %w", err)
	}
	return count == 0, nil
}

func (s *Store) BindBootstrapLocalAccount(ctx context.Context, command authorityapp.BindBootstrapLocalAccountCommand) (authoritydomain.Principal, error) {
	if !validBootstrapLocalAccountCommand(command) {
		return authoritydomain.Principal{}, ErrLocalAccountInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return authoritydomain.Principal{}, fmt.Errorf("begin local account setup: %w", err)
	}
	defer tx.Rollback()

	var principal authoritydomain.Principal
	err = tx.QueryRowContext(ctx, `
		SELECT 'human', h.id, h.organization_id
		FROM organizations o
		JOIN humans h ON h.id = o.bootstrap_human_id
		WHERE o.singleton = 1
		  AND h.id = ?
		  AND h.organization_id = ?
		  AND h.status = 'active'
	`, command.BootstrapHuman.ID, command.BootstrapHuman.OrganizationID).Scan(
		&principal.Kind, &principal.ID, &principal.OrganizationID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return authoritydomain.Principal{}, ErrPermissionDenied
	}
	if err != nil {
		return authoritydomain.Principal{}, fmt.Errorf("read bootstrap human for local account: %w", err)
	}

	var identities int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM auth_identities WHERE provider = ?`, localIdentityProvider).Scan(&identities); err != nil {
		return authoritydomain.Principal{}, fmt.Errorf("inspect existing local identity: %w", err)
	}
	if identities != 0 {
		return authoritydomain.Principal{}, ErrLocalAccountSetupDone
	}

	identityID := uuid.NewString()
	stamp := unixNano(command.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO auth_identities(id, human_id, provider, subject, created_at)
		VALUES(?, ?, ?, ?, ?)
	`, identityID, principal.ID, command.Identity.Provider, command.Identity.Subject, stamp); err != nil {
		if isUniqueConstraint(err, "auth_identities.provider, auth_identities.subject") {
			return authoritydomain.Principal{}, ErrLocalAccountSetupDone
		}
		return authoritydomain.Principal{}, fmt.Errorf("persist local auth identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_password_credentials(
			identity_id, algorithm, salt, digest, memory_kib, iterations, parallelism, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, identityID, command.Password.Algorithm, command.Password.Salt, command.Password.Digest,
		command.Password.Memory, command.Password.Iterations, command.Password.Parallelism, stamp); err != nil {
		return authoritydomain.Principal{}, fmt.Errorf("persist local password credential: %w", err)
	}
	sessionHash := browserTokenHash(browserSessionHashDomain, command.SessionToken)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_sessions(token_hash, human_id, created_at, expires_at)
		VALUES(?, ?, ?, ?)
	`, sessionHash[:], principal.ID, stamp, unixNano(command.SessionExpiresAt)); err != nil {
		return authoritydomain.Principal{}, fmt.Errorf("persist setup browser session: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: principal.OrganizationID,
		Actor:          principal,
		Action:         AuditAuthIdentityBind,
		TargetKind:     "human",
		TargetID:       principal.ID,
		RequestID:      command.RequestID,
		Outcome:        "committed",
		Now:            command.Now,
	}); err != nil {
		return authoritydomain.Principal{}, err
	}
	if err := tx.Commit(); err != nil {
		return authoritydomain.Principal{}, fmt.Errorf("commit local account setup: %w", err)
	}
	return principal, nil
}

func (s *Store) GetLocalAccount(ctx context.Context, subject string) (authorityapp.LocalAccount, error) {
	var account authorityapp.LocalAccount
	var salt, digest []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT ai.provider, ai.subject, 'human', h.id, h.organization_id,
		       lp.algorithm, lp.salt, lp.digest, lp.memory_kib, lp.iterations, lp.parallelism
		FROM auth_identities ai
		JOIN local_password_credentials lp ON lp.identity_id = ai.id
		JOIN humans h ON h.id = ai.human_id
		WHERE ai.provider = ? AND ai.subject = ? AND h.status = 'active'
	`, localIdentityProvider, subject).Scan(
		&account.Identity.Provider, &account.Identity.Subject,
		&account.Human.Kind, &account.Human.ID, &account.Human.OrganizationID,
		&account.Password.Algorithm, &salt, &digest, &account.Password.Memory,
		&account.Password.Iterations, &account.Password.Parallelism,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return authorityapp.LocalAccount{}, ErrPermissionDenied
	}
	if err != nil {
		return authorityapp.LocalAccount{}, fmt.Errorf("read local account: %w", err)
	}
	account.Password.Salt = append([]byte(nil), salt...)
	account.Password.Digest = append([]byte(nil), digest...)
	if !validPasswordDigest(account.Password) {
		return authorityapp.LocalAccount{}, fmt.Errorf("read local account: %w", ErrLocalAccountInvalid)
	}
	return account, nil
}

func (s *Store) CreateBrowserSession(ctx context.Context, command authorityapp.CreateBrowserSessionCommand) error {
	if command.Human.Kind != authoritydomain.PrincipalHuman || command.Human.ID == "" || command.Human.OrganizationID == "" ||
		!validBrowserToken(command.Token) || !command.ExpiresAt.After(command.Now) {
		return ErrBrowserSessionInvalid
	}
	hash := browserTokenHash(browserSessionHashDomain, command.Token)
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO browser_sessions(token_hash, human_id, created_at, expires_at)
		SELECT ?, id, ?, ?
		FROM humans
		WHERE id = ? AND organization_id = ? AND status = 'active'
	`, hash[:], unixNano(command.Now), unixNano(command.ExpiresAt), command.Human.ID, command.Human.OrganizationID)
	if err != nil {
		return fmt.Errorf("persist browser session: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count browser session creation: %w", err)
	}
	if created != 1 {
		return ErrPermissionDenied
	}
	return nil
}

func validBootstrapLocalAccountCommand(command authorityapp.BindBootstrapLocalAccountCommand) bool {
	requestID, err := uuid.Parse(command.RequestID)
	if err != nil || requestID.String() != command.RequestID {
		return false
	}
	return command.BootstrapHuman.Kind == authoritydomain.PrincipalHuman &&
		command.BootstrapHuman.ID != "" && command.BootstrapHuman.OrganizationID != "" &&
		command.Identity.Provider == localIdentityProvider &&
		len(command.Identity.Subject) >= 3 && len(command.Identity.Subject) <= 32 &&
		validPasswordDigest(command.Password) &&
		validBrowserToken(command.SessionToken) &&
		command.SessionExpiresAt.After(command.Now)
}

func validPasswordDigest(digest authorityapp.PasswordDigest) bool {
	return digest.Algorithm == argon2IDAlgorithm && len(digest.Salt) == 16 && len(digest.Digest) == 32 &&
		digest.Memory >= 8192 && digest.Memory <= 262144 &&
		digest.Iterations >= 1 && digest.Iterations <= 10 &&
		digest.Parallelism >= 1 && digest.Parallelism <= 8
}
