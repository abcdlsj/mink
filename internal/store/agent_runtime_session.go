package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
)

const (
	agentRuntimeSessionHashDomain = "sumi-agent-runtime-session-v1\x00"
	agentRuntimeSessionTTL        = 10 * time.Minute
)

var agentRuntimeTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type AgentRuntimeSession = authorityapp.RuntimeSession

type AgentRuntimeProof = authorityapp.RuntimeProof

type AgentRuntimeAuthentication = authorityapp.RuntimeAuthentication

type CreateAgentRuntimeSessionParams = authorityapp.CreateRuntimeSessionCommand

type RenewAgentRuntimeSessionParams = authorityapp.RenewRuntimeSessionCommand

type RevokeAgentRuntimeSessionParams = authorityapp.RevokeRuntimeSessionCommand

func (s *Store) CreateAgentRuntimeSession(ctx context.Context, params CreateAgentRuntimeSessionParams) (AgentRuntimeSession, error) {
	if !validAgentRuntimeSession(params.AgentID, params.ComputerID, params.PlacementDesiredRevision, params.Token, params.Now, params.ExpiresAt) {
		return AgentRuntimeSession{}, ErrAgentRuntimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRuntimeSession{}, fmt.Errorf("begin agent runtime session creation: %w", err)
	}
	defer tx.Rollback()

	if err := authenticateComputer(ctx, tx, params.ComputerID, params.RegistrationKey); err != nil {
		return AgentRuntimeSession{}, err
	}
	bound, err := readyRuntimeBinding(ctx, tx, params.AgentID, params.ComputerID, params.PlacementDesiredRevision)
	if err != nil {
		return AgentRuntimeSession{}, err
	}
	if !bound {
		return AgentRuntimeSession{}, ErrAgentRuntimeBinding
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_runtime_sessions
		SET revoked_at = max(created_at, ?)
		WHERE agent_id = ? AND revoked_at IS NULL
	`, unixNano(params.Now), params.AgentID); err != nil {
		return AgentRuntimeSession{}, fmt.Errorf("revoke current agent runtime session: %w", err)
	}
	session := AgentRuntimeSession{
		AgentID: params.AgentID, ComputerID: params.ComputerID,
		PlacementDesiredRevision: params.PlacementDesiredRevision,
		CreatedAt:                params.Now.UTC(), ExpiresAt: params.ExpiresAt.UTC(),
	}
	if err := insertAgentRuntimeSession(ctx, tx, session, params.Token); err != nil {
		return AgentRuntimeSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRuntimeSession{}, fmt.Errorf("commit agent runtime session creation: %w", err)
	}
	return session, nil
}

func (s *Store) AuthenticateAgentRuntimeSession(ctx context.Context, token string, now time.Time) (AgentRuntimeAuthentication, error) {
	if !agentRuntimeTokenPattern.MatchString(token) || now.IsZero() {
		return AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	hash := agentRuntimeTokenHash(token)
	authentication, err := agentRuntimeAuthentication(ctx, s.db, hash, now)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	if err != nil {
		return AgentRuntimeAuthentication{}, fmt.Errorf("authenticate agent runtime session: %w", err)
	}
	return authentication, nil
}

func (s *Store) RenewAgentRuntimeSession(ctx context.Context, params RenewAgentRuntimeSessionParams) (AgentRuntimeSession, error) {
	if !validAgentRuntimeRenewal(params) {
		return AgentRuntimeSession{}, ErrAgentRuntimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentRuntimeSession{}, fmt.Errorf("begin agent runtime session renewal: %w", err)
	}
	defer tx.Rollback()

	authentication, err := requireAgentRuntimeSession(ctx, tx, params.Proof, params.Now)
	if err != nil {
		return AgentRuntimeSession{}, err
	}
	if err := authenticateComputer(ctx, tx, params.ComputerID, params.RegistrationKey); err != nil {
		return AgentRuntimeSession{}, err
	}
	if authentication.Proof.ComputerID() != params.ComputerID {
		return AgentRuntimeSession{}, ErrAgentRuntimeBinding
	}
	revoked, err := revokeAgentRuntimeProof(ctx, tx, authentication.Proof, params.Now)
	if err != nil {
		return AgentRuntimeSession{}, err
	}
	if !revoked {
		return AgentRuntimeSession{}, ErrAgentRuntimeUnauthenticated
	}
	session := AgentRuntimeSession{
		AgentID: authentication.Proof.AgentID(), ComputerID: authentication.Proof.ComputerID(),
		PlacementDesiredRevision: authentication.Proof.PlacementDesiredRevision(),
		CreatedAt:                params.Now.UTC(), ExpiresAt: params.ExpiresAt.UTC(),
	}
	if err := insertAgentRuntimeSession(ctx, tx, session, params.Token); err != nil {
		return AgentRuntimeSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentRuntimeSession{}, fmt.Errorf("commit agent runtime session renewal: %w", err)
	}
	return session, nil
}

func (s *Store) RevokeAgentRuntimeSession(ctx context.Context, params RevokeAgentRuntimeSessionParams) error {
	if params.ComputerID == "" || params.RegistrationKey == "" || params.Now.IsZero() {
		return ErrAgentRuntimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent runtime session revocation: %w", err)
	}
	defer tx.Rollback()

	authentication, err := requireAgentRuntimeSession(ctx, tx, params.Proof, params.Now)
	if err != nil {
		return err
	}
	if err := authenticateComputer(ctx, tx, params.ComputerID, params.RegistrationKey); err != nil {
		return err
	}
	if authentication.Proof.ComputerID() != params.ComputerID {
		return ErrAgentRuntimeBinding
	}
	revoked, err := revokeAgentRuntimeProof(ctx, tx, authentication.Proof, params.Now)
	if err != nil {
		return err
	}
	if !revoked {
		return ErrAgentRuntimeUnauthenticated
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent runtime session revocation: %w", err)
	}
	return nil
}

func requireAgentRuntimeSession(ctx context.Context, tx *sql.Tx, proof AgentRuntimeProof, now time.Time) (AgentRuntimeAuthentication, error) {
	if !validAgentRuntimeProof(proof) || now.IsZero() {
		return AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	authentication, err := agentRuntimeAuthentication(ctx, tx, proof.TokenHash(), now)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	if err != nil {
		return AgentRuntimeAuthentication{}, fmt.Errorf("recheck agent runtime session: %w", err)
	}
	if authentication.Proof != proof {
		return AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	return authentication, nil
}

func agentRuntimeAuthentication(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, hash [sha256.Size]byte, now time.Time) (AgentRuntimeAuthentication, error) {
	var principal Principal
	var storedHash []byte
	var computerID string
	var placementDesiredRevision uint64
	err := queryer.QueryRowContext(ctx, `
		SELECT 'agent', sessions.agent_id, organizations.id,
		       sessions.token_hash, sessions.computer_id, sessions.placement_desired_revision
		FROM agent_runtime_sessions AS sessions
		JOIN agent_placements AS placements
		  ON placements.agent_id = sessions.agent_id
		 AND placements.computer_id = sessions.computer_id
		 AND placements.desired_revision = sessions.placement_desired_revision
		JOIN organizations ON organizations.singleton = 1
		WHERE sessions.token_hash = ?
		  AND sessions.revoked_at IS NULL
		  AND sessions.expires_at > ?
		  AND placements.state = 'ready'
	`, hash[:], unixNano(now)).Scan(
		&principal.Kind,
		&principal.ID,
		&principal.OrganizationID,
		&storedHash,
		&computerID,
		&placementDesiredRevision,
	)
	if err != nil {
		return AgentRuntimeAuthentication{}, err
	}
	if len(storedHash) != sha256.Size {
		return AgentRuntimeAuthentication{}, ErrAgentRuntimeUnauthenticated
	}
	var tokenHash [sha256.Size]byte
	copy(tokenHash[:], storedHash)
	return AgentRuntimeAuthentication{
		Principal: principal,
		Proof:     authorityapp.NewRuntimeProof(tokenHash, principal.ID, computerID, placementDesiredRevision),
	}, nil
}

func readyRuntimeBinding(ctx context.Context, tx *sql.Tx, agentID, computerID string, desiredRevision uint64) (bool, error) {
	var bound bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM agent_placements
			WHERE agent_id = ? AND computer_id = ? AND desired_revision = ? AND state = 'ready'
		)
	`, agentID, computerID, desiredRevision).Scan(&bound); err != nil {
		return false, fmt.Errorf("check ready agent runtime binding: %w", err)
	}
	return bound, nil
}

func insertAgentRuntimeSession(ctx context.Context, tx *sql.Tx, session AgentRuntimeSession, token string) error {
	hash := agentRuntimeTokenHash(token)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_runtime_sessions(
			token_hash, agent_id, computer_id, placement_desired_revision, created_at, expires_at
		)
		VALUES(?, ?, ?, ?, ?, ?)
	`, hash[:], session.AgentID, session.ComputerID, session.PlacementDesiredRevision,
		unixNano(session.CreatedAt), unixNano(session.ExpiresAt)); err != nil {
		return fmt.Errorf("persist agent runtime session: %w", err)
	}
	return nil
}

func revokeAgentRuntimeProof(ctx context.Context, tx *sql.Tx, proof AgentRuntimeProof, now time.Time) (bool, error) {
	tokenHash := proof.TokenHash()
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_runtime_sessions
		SET revoked_at = max(created_at, ?)
		WHERE token_hash = ?
		  AND agent_id = ?
		  AND computer_id = ?
		  AND placement_desired_revision = ?
		  AND revoked_at IS NULL
		  AND expires_at > ?
		  AND EXISTS(
			SELECT 1 FROM agent_placements
			WHERE agent_id = agent_runtime_sessions.agent_id
			  AND computer_id = agent_runtime_sessions.computer_id
			  AND desired_revision = agent_runtime_sessions.placement_desired_revision
			  AND state = 'ready'
		  )
	`, unixNano(now), tokenHash[:], proof.AgentID(), proof.ComputerID(),
		proof.PlacementDesiredRevision(), unixNano(now))
	if err != nil {
		return false, fmt.Errorf("revoke agent runtime session: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count agent runtime session revocation: %w", err)
	}
	return count == 1, nil
}

func validAgentRuntimeSession(agentID, computerID string, desiredRevision uint64, token string, now, expiresAt time.Time) bool {
	lifetime := expiresAt.Sub(now)
	return agentID != "" && computerID != "" && desiredRevision > 0 &&
		agentRuntimeTokenPattern.MatchString(token) && !now.IsZero() && lifetime > 0 && lifetime <= agentRuntimeSessionTTL
}

func validAgentRuntimeRenewal(params RenewAgentRuntimeSessionParams) bool {
	lifetime := params.ExpiresAt.Sub(params.Now)
	return validAgentRuntimeProof(params.Proof) && params.ComputerID != "" && params.RegistrationKey != "" &&
		agentRuntimeTokenPattern.MatchString(params.Token) && !params.Now.IsZero() && lifetime > 0 && lifetime <= agentRuntimeSessionTTL
}

func validAgentRuntimeProof(proof AgentRuntimeProof) bool {
	return proof.Valid()
}

func agentRuntimeTokenHash(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(agentRuntimeSessionHashDomain + token))
}
