package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	"github.com/google/uuid"
)

const (
	localIdentityProvider = "local"
	argon2IDAlgorithm     = "argon2id"
)

func (s *Store) FirstOwnerRegistrationRequired(ctx context.Context) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
			SELECT count(*)
			FROM organizations
		`).Scan(&count); err != nil {
		return false, fmt.Errorf("inspect first owner registration: %w", err)
	}
	return count == 0, nil
}

func (s *Store) RegisterFirstOwner(ctx context.Context, command authorityapp.RegisterFirstOwnerCommand) (AuthorityBootstrap, error) {
	if !validRegisterFirstOwnerCommand(command) {
		return AuthorityBootstrap{}, ErrLocalAccountInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("begin first owner registration: %w", err)
	}
	defer tx.Rollback()

	var organizations int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM organizations`).Scan(&organizations); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("inspect first owner registration: %w", err)
	}
	if organizations != 0 {
		return AuthorityBootstrap{}, ErrRegistrationClosed
	}

	organizationID := uuid.NewString()
	humanID := uuid.NewString()
	grantID := uuid.NewString()
	stamp := unixNano(command.Now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO organizations(singleton, id, name, bootstrap_human_id, created_at)
		VALUES(1, ?, 'Sumi', ?, ?)
	`, organizationID, humanID, stamp); err != nil {
		if isUniqueConstraint(err, "organizations.singleton") {
			return AuthorityBootstrap{}, ErrRegistrationClosed
		}
		return AuthorityBootstrap{}, fmt.Errorf("persist organization bootstrap: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO humans(id, organization_id, name, role, status, created_at, updated_at)
		VALUES(?, ?, ?, 'owner', 'active', ?, ?)
	`, humanID, organizationID, command.Name, stamp, stamp); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("persist bootstrap human: %w", err)
	}
	if err := insertLocalAccount(ctx, tx, humanID, command.Identity, command.Password, command.Now); err != nil {
		return AuthorityBootstrap{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO grants(
			id, organization_id, subject_kind, subject_id, issuer_kind, issuer_id,
			capability, scope_kind, scope_id, parent_grant_id, created_at, updated_at
		)
		VALUES(?, ?, 'human', ?, 'system', '', ?, 'organization', ?, '', ?, ?)
	`, grantID, organizationID, humanID, CapabilityOrganizationAdmin, organizationID, stamp, stamp); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("persist bootstrap grant: %w", err)
	}
	sessionHash := browserTokenHash(browserSessionHashDomain, command.SessionToken)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO browser_sessions(token_hash, human_id, created_at, expires_at)
		VALUES(?, ?, ?, ?)
	`, sessionHash[:], humanID, stamp, unixNano(command.SessionExpiresAt)); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("persist registration browser session: %w", err)
	}
	system := Principal{Kind: "system", OrganizationID: organizationID}
	for _, event := range []AppendAuditParams{
		{OrganizationID: organizationID, Actor: system, Action: AuditOrganizationBootstrap, TargetKind: "organization", TargetID: organizationID, RequestID: command.RequestID, Outcome: "committed", Now: command.Now},
		{OrganizationID: organizationID, Actor: system, Action: AuditHumanCreate, TargetKind: "human", TargetID: humanID, RequestID: command.RequestID, Outcome: "committed", Now: command.Now},
		{OrganizationID: organizationID, Actor: system, Action: AuditGrantIssue, TargetKind: "grant", TargetID: grantID, RequestID: command.RequestID, Outcome: "committed", Now: command.Now},
		{OrganizationID: organizationID, Actor: system, Action: AuditAuthIdentityBind, TargetKind: "human", TargetID: humanID, RequestID: command.RequestID, Outcome: "committed", Now: command.Now},
	} {
		if err := appendAuditEvent(ctx, tx, event); err != nil {
			return AuthorityBootstrap{}, err
		}
	}
	bootstrap, err := readAuthorityBootstrap(ctx, tx)
	if err != nil {
		return AuthorityBootstrap{}, err
	}
	if err := tx.Commit(); err != nil {
		return AuthorityBootstrap{}, fmt.Errorf("commit first owner registration: %w", err)
	}
	return bootstrap, nil
}

func insertLocalAccount(ctx context.Context, tx *sql.Tx, humanID string, identity authorityapp.AuthenticationIdentity, password authorityapp.PasswordDigest, now time.Time) error {
	if humanID == "" || !validLocalIdentity(identity) || !validPasswordDigest(password) {
		return ErrLocalAccountInvalid
	}
	identityID := uuid.NewString()
	stamp := unixNano(now)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO auth_identities(id, human_id, provider, subject, created_at)
		VALUES(?, ?, ?, ?, ?)
	`, identityID, humanID, identity.Provider, identity.Subject, stamp); err != nil {
		if isUniqueConstraint(err, "auth_identities.provider, auth_identities.subject") {
			return ErrHumanAccountExists
		}
		return fmt.Errorf("persist local auth identity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO local_password_credentials(
			identity_id, algorithm, salt, digest, memory_kib, iterations, parallelism, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, identityID, password.Algorithm, password.Salt, password.Digest,
		password.Memory, password.Iterations, password.Parallelism, stamp); err != nil {
		return fmt.Errorf("persist local password credential: %w", err)
	}
	return nil
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

func validRegisterFirstOwnerCommand(command authorityapp.RegisterFirstOwnerCommand) bool {
	requestID, err := uuid.Parse(command.RequestID)
	if err != nil || requestID.String() != command.RequestID {
		return false
	}
	return command.Name == strings.TrimSpace(command.Name) && utf8.ValidString(command.Name) &&
		utf8.RuneCountInString(command.Name) >= 1 && utf8.RuneCountInString(command.Name) <= 100 &&
		validLocalIdentity(command.Identity) &&
		validPasswordDigest(command.Password) &&
		validBrowserToken(command.SessionToken) &&
		command.SessionExpiresAt.After(command.Now)
}

func validLocalIdentity(identity authorityapp.AuthenticationIdentity) bool {
	subject, ok := localauth.NormalizeUsername(identity.Subject)
	return identity.Provider == localIdentityProvider && ok && subject == identity.Subject
}

func validPasswordDigest(digest authorityapp.PasswordDigest) bool {
	return digest.Algorithm == argon2IDAlgorithm && len(digest.Salt) == 16 && len(digest.Digest) == 32 &&
		digest.Memory >= 8192 && digest.Memory <= 262144 &&
		digest.Iterations >= 1 && digest.Iterations <= 10 &&
		digest.Parallelism >= 1 && digest.Parallelism <= 8
}
