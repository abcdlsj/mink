package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	operationCreateDM       = "space.create.dm"
	operationCreateGroup    = "space.create.group"
	operationAddMember      = "space.member.add"
	operationRemoveMember   = "space.member.remove"
	operationArchiveSpace   = "space.archive"
	operationUnarchiveSpace = "space.unarchive"
)

type CreateDMParams struct {
	RequestID string
	Actor     Principal
	Peer      Principal
	Now       time.Time
}

type CreateGroupParams struct {
	RequestID string
	Actor     Principal
	Name      string
	Now       time.Time
}

type SpaceReadParams struct {
	Actor   Principal
	SpaceID string
	Now     time.Time
}

type ListSpacesParams struct {
	Actor Principal
	Now   time.Time
}

type ChangeMemberParams struct {
	RequestID string
	Actor     Principal
	SpaceID   string
	Member    Principal
	Now       time.Time
}

type ChangeSpaceArchiveParams struct {
	RequestID string
	Actor     Principal
	SpaceID   string
	Now       time.Time
}

func (s *Store) CreateDM(ctx context.Context, params CreateDMParams) (Space, error) {
	dmKey, err := canonicalDMKey(params.Actor, params.Peer)
	if err != nil {
		return Space{}, err
	}
	fingerprint, err := collaborationFingerprint(struct {
		ActorKind PrincipalKind `json:"actor_kind"`
		ActorID   string        `json:"actor_id"`
		PeerKind  PrincipalKind `json:"peer_kind"`
		PeerID    string        `json:"peer_id"`
	}{params.Actor.Kind, params.Actor.ID, params.Peer.Kind, params.Peer.ID})
	if err != nil {
		return Space{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Space{}, fmt.Errorf("begin dm creation: %w", err)
	}
	defer tx.Rollback()
	if receipt, found, err := readCollaborationReceipt(ctx, tx, params.RequestID, operationCreateDM, fingerprint); err != nil {
		return Space{}, err
	} else if found {
		return commitSpaceReplay(tx, receipt.ResultID)
	}
	if err := requireCollaborationGrant(ctx, tx, params.Actor, CapabilitySpaceCreate,
		Scope{Kind: "organization", ID: params.Actor.OrganizationID}, AuditSpaceCreate,
		"space", "", params.RequestID, params.Now); err != nil {
		return Space{}, err
	}
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return Space{}, denyCollaboration(ctx, tx, params.Actor, AuditSpaceCreate, "space", "", params.RequestID, "actor_invalid", params.Now, ErrPermissionDenied)
	}
	if err := validatePrincipalInOrganization(ctx, tx, params.Peer, params.Actor.OrganizationID); err != nil {
		return Space{}, denyCollaboration(ctx, tx, params.Actor, AuditSpaceCreate, "space", "", params.RequestID, "peer_invalid", params.Now, err)
	}

	space, err := scanSpace(tx.QueryRowContext(ctx, spaceSelect+` WHERE organization_id = ? AND dm_key = ?`, params.Actor.OrganizationID, dmKey))
	if err == nil {
		if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operationCreateDM, fingerprint, space.ID, params.Now); err != nil {
			return Space{}, err
		}
		if err := tx.Commit(); err != nil {
			return Space{}, fmt.Errorf("commit canonical dm replay: %w", err)
		}
		return space, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Space{}, fmt.Errorf("read canonical dm: %w", err)
	}

	stamp := unixNano(params.Now)
	spaceID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO spaces(id, organization_id, kind, name, dm_key, created_at, updated_at)
		VALUES(?, ?, 'dm', '', ?, ?, ?)
	`, spaceID, params.Actor.OrganizationID, dmKey, stamp, stamp); err != nil {
		return Space{}, fmt.Errorf("persist dm: %w", err)
	}
	for _, member := range []Principal{params.Actor, params.Peer} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO space_memberships(space_id, principal_kind, principal_id, joined_at)
			VALUES(?, ?, ?, ?)
		`, spaceID, member.Kind, member.ID, stamp); err != nil {
			return Space{}, fmt.Errorf("persist dm membership: %w", err)
		}
	}
	if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operationCreateDM, fingerprint, spaceID, params.Now); err != nil {
		return Space{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditSpaceCreate,
		TargetKind: "space", TargetID: spaceID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return Space{}, err
	}
	space, err = scanSpace(tx.QueryRowContext(ctx, spaceSelect+` WHERE id = ?`, spaceID))
	if err != nil {
		return Space{}, fmt.Errorf("read created dm: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Space{}, fmt.Errorf("commit dm creation: %w", err)
	}
	return space, nil
}

func (s *Store) CreateGroup(ctx context.Context, params CreateGroupParams) (Space, error) {
	if err := validateSpaceName(params.Name); err != nil {
		return Space{}, err
	}
	fingerprint, err := collaborationFingerprint(struct {
		ActorKind PrincipalKind `json:"actor_kind"`
		ActorID   string        `json:"actor_id"`
		Name      string        `json:"name"`
	}{params.Actor.Kind, params.Actor.ID, params.Name})
	if err != nil {
		return Space{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Space{}, fmt.Errorf("begin group creation: %w", err)
	}
	defer tx.Rollback()
	if receipt, found, err := readCollaborationReceipt(ctx, tx, params.RequestID, operationCreateGroup, fingerprint); err != nil {
		return Space{}, err
	} else if found {
		return commitSpaceReplay(tx, receipt.ResultID)
	}
	if err := requireCollaborationGrant(ctx, tx, params.Actor, CapabilitySpaceCreate,
		Scope{Kind: "organization", ID: params.Actor.OrganizationID}, AuditSpaceCreate,
		"space", "", params.RequestID, params.Now); err != nil {
		return Space{}, err
	}
	if params.Actor.Kind != "human" {
		return Space{}, denyCollaboration(ctx, tx, params.Actor, AuditSpaceCreate, "space", "", params.RequestID, "human_creator_required", params.Now, ErrPermissionDenied)
	}
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return Space{}, denyCollaboration(ctx, tx, params.Actor, AuditSpaceCreate, "space", "", params.RequestID, "actor_invalid", params.Now, ErrPermissionDenied)
	}

	stamp := unixNano(params.Now)
	spaceID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO spaces(id, organization_id, kind, name, dm_key, created_at, updated_at)
		VALUES(?, ?, 'group', ?, NULL, ?, ?)
	`, spaceID, params.Actor.OrganizationID, params.Name, stamp, stamp); err != nil {
		return Space{}, fmt.Errorf("persist group: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO space_memberships(space_id, principal_kind, principal_id, joined_at)
		VALUES(?, 'human', ?, ?)
	`, spaceID, params.Actor.ID, stamp); err != nil {
		return Space{}, fmt.Errorf("persist group creator membership: %w", err)
	}
	if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operationCreateGroup, fingerprint, spaceID, params.Now); err != nil {
		return Space{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditSpaceCreate,
		TargetKind: "space", TargetID: spaceID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return Space{}, err
	}
	space, err := scanSpace(tx.QueryRowContext(ctx, spaceSelect+` WHERE id = ?`, spaceID))
	if err != nil {
		return Space{}, fmt.Errorf("read created group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Space{}, fmt.Errorf("commit group creation: %w", err)
	}
	return space, nil
}

func (s *Store) GetSpace(ctx context.Context, params SpaceReadParams) (Space, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Space{}, fmt.Errorf("begin space read: %w", err)
	}
	defer tx.Rollback()
	space, err := requireReadableSpace(ctx, tx, params.Actor, params.SpaceID, params.Now)
	if err != nil {
		return Space{}, err
	}
	if err := tx.Commit(); err != nil {
		return Space{}, fmt.Errorf("commit space read: %w", err)
	}
	return space, nil
}

func (s *Store) ListSpaces(ctx context.Context, params ListSpacesParams) ([]Space, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin space list: %w", err)
	}
	defer tx.Rollback()
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return nil, ErrPermissionDenied
	}
	rows, err := tx.QueryContext(ctx, spaceSelect+`
		WHERE organization_id = ? AND EXISTS (
			SELECT 1 FROM space_memberships m
			WHERE m.space_id = spaces.id AND m.principal_kind = ? AND m.principal_id = ?
		)
		ORDER BY created_at, id
	`, params.Actor.OrganizationID, params.Actor.Kind, params.Actor.ID)
	if err != nil {
		return nil, fmt.Errorf("list member spaces: %w", err)
	}
	var candidates []Space
	for rows.Next() {
		space, err := scanSpace(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan member space: %w", err)
		}
		candidates = append(candidates, space)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close member spaces: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate member spaces: %w", err)
	}
	spaces := make([]Space, 0, len(candidates))
	for _, space := range candidates {
		reason, err := requireGrant(ctx, tx, params.Actor, CapabilitySpaceRead, Scope{Kind: "space", ID: space.ID}, params.Now, "")
		if err != nil {
			return nil, err
		}
		if reason == "" {
			spaces = append(spaces, space)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit space list: %w", err)
	}
	return spaces, nil
}

type membershipChange string

const (
	membershipAdd    membershipChange = operationAddMember
	membershipRemove membershipChange = operationRemoveMember
)

func (change membershipChange) auditAction() string {
	if change == membershipAdd {
		return AuditSpaceMemberAdd
	}
	return AuditSpaceMemberRemove
}

func (s *Store) AddMember(ctx context.Context, params ChangeMemberParams) (MutationReceipt, error) {
	return s.changeMember(ctx, params, membershipAdd)
}

func (s *Store) RemoveMember(ctx context.Context, params ChangeMemberParams) (MutationReceipt, error) {
	return s.changeMember(ctx, params, membershipRemove)
}

func (s *Store) changeMember(ctx context.Context, params ChangeMemberParams, change membershipChange) (MutationReceipt, error) {
	operation := string(change)
	action := change.auditAction()
	adding := change == membershipAdd
	fingerprint, err := collaborationFingerprint(struct {
		ActorKind  PrincipalKind `json:"actor_kind"`
		ActorID    string        `json:"actor_id"`
		SpaceID    string        `json:"space_id"`
		MemberKind PrincipalKind `json:"member_kind"`
		MemberID   string        `json:"member_id"`
	}{params.Actor.Kind, params.Actor.ID, params.SpaceID, params.Member.Kind, params.Member.ID})
	if err != nil {
		return MutationReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationReceipt{}, fmt.Errorf("begin membership change: %w", err)
	}
	defer tx.Rollback()
	if receipt, found, err := readCollaborationReceipt(ctx, tx, params.RequestID, operation, fingerprint); err != nil {
		return MutationReceipt{}, err
	} else if found {
		return commitMutationReplay(tx, params.RequestID, receipt)
	}
	if err := requireCollaborationGrantWithContext(ctx, tx, params.Actor, CapabilitySpaceMembers,
		Scope{Kind: "space", ID: params.SpaceID}, action, string(params.Member.Kind), params.Member.ID,
		"space", params.SpaceID, params.RequestID, params.Now); err != nil {
		return MutationReceipt{}, err
	}
	space, err := loadMutationSpace(ctx, tx, params.Actor, params.SpaceID)
	if err != nil {
		return MutationReceipt{}, denyCollaborationWithContext(ctx, tx, params.Actor, action, string(params.Member.Kind), params.Member.ID, "space", params.SpaceID, params.RequestID, "space_unavailable", params.Now, err)
	}
	if err := collaborationSpace(space).ValidateMembershipMutation(); err != nil {
		return MutationReceipt{}, denyCollaborationWithContext(ctx, tx, params.Actor, action, string(params.Member.Kind), params.Member.ID, "space", params.SpaceID, params.RequestID, membershipDenialReason(err), params.Now, err)
	}
	validateMember := validatePrincipalExistsInOrganization
	if adding {
		validateMember = validatePrincipalInOrganization
	}
	if err := validateMember(ctx, tx, params.Member, params.Actor.OrganizationID); err != nil {
		return MutationReceipt{}, denyCollaborationWithContext(ctx, tx, params.Actor, action, string(params.Member.Kind), params.Member.ID, "space", params.SpaceID, params.RequestID, "member_invalid", params.Now, err)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM space_memberships WHERE space_id = ? AND principal_kind = ? AND principal_id = ?)
	`, params.SpaceID, params.Member.Kind, params.Member.ID).Scan(&exists); err != nil {
		return MutationReceipt{}, fmt.Errorf("read membership state: %w", err)
	}
	remainingActiveHumanMembers := 1
	if err := collaborationSpace(space).ValidateMembershipChange(
		collaborationMembershipChange(change), collaborationPrincipal(params.Member), exists, remainingActiveHumanMembers,
	); err != nil {
		return MutationReceipt{}, denyCollaborationWithContext(ctx, tx, params.Actor, action, string(params.Member.Kind), params.Member.ID, "space", params.SpaceID, params.RequestID, membershipDenialReason(err), params.Now, err)
	}
	if !adding && params.Member.Kind == "human" {
		var targetActive bool
		if err := tx.QueryRowContext(ctx, `SELECT status = 'active' FROM humans WHERE id = ?`, params.Member.ID).Scan(&targetActive); err != nil {
			return MutationReceipt{}, fmt.Errorf("read removed human status: %w", err)
		}
		if targetActive {
			if err := tx.QueryRowContext(ctx, `
				SELECT count(*)
				FROM space_memberships m
				JOIN humans h ON h.id = m.principal_id AND h.organization_id = ? AND h.status = 'active'
				WHERE m.space_id = ? AND m.principal_kind = 'human' AND m.principal_id != ?
			`, params.Actor.OrganizationID, params.SpaceID, params.Member.ID).Scan(&remainingActiveHumanMembers); err != nil {
				return MutationReceipt{}, fmt.Errorf("count remaining active human members: %w", err)
			}
		}
		if err := collaborationSpace(space).ValidateMembershipChange(
			collaborationMembershipChange(change), collaborationPrincipal(params.Member), exists, remainingActiveHumanMembers,
		); err != nil {
			return MutationReceipt{}, denyCollaborationWithContext(ctx, tx, params.Actor, action, string(params.Member.Kind), params.Member.ID, "space", params.SpaceID, params.RequestID, membershipDenialReason(err), params.Now, err)
		}
	}
	if adding {
		_, err = tx.ExecContext(ctx, `INSERT INTO space_memberships(space_id, principal_kind, principal_id, joined_at) VALUES(?, ?, ?, ?)`, params.SpaceID, params.Member.Kind, params.Member.ID, unixNano(params.Now))
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM space_memberships WHERE space_id = ? AND principal_kind = ? AND principal_id = ?`, params.SpaceID, params.Member.Kind, params.Member.ID)
	}
	if err != nil {
		return MutationReceipt{}, fmt.Errorf("persist membership change: %w", err)
	}
	if !adding && params.Member.Kind == "agent" {
		if err := closeRemovedAgentInbox(ctx, tx, params.Member.ID, params.SpaceID, params.Now); err != nil {
			return MutationReceipt{}, err
		}
	}
	if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operation, fingerprint, params.SpaceID, params.Now); err != nil {
		return MutationReceipt{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: action,
		TargetKind: string(params.Member.Kind), TargetID: params.Member.ID, RequestID: params.RequestID,
		ContextKind: "space", ContextID: params.SpaceID, Outcome: "committed", Now: params.Now,
	}); err != nil {
		return MutationReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return MutationReceipt{}, fmt.Errorf("commit membership change: %w", err)
	}
	return MutationReceipt{RequestID: params.RequestID, CommittedAt: params.Now.UTC()}, nil
}

func membershipDenialReason(err error) string {
	switch {
	case errors.Is(err, ErrDMImmutable):
		return "dm_immutable"
	case errors.Is(err, ErrSpaceArchived):
		return "space_archived"
	case errors.Is(err, ErrMembershipExists):
		return "member_exists"
	case errors.Is(err, ErrMembershipNotFound):
		return "member_missing"
	case errors.Is(err, ErrLastActiveHumanMember):
		return "last_active_human"
	default:
		return "membership_invalid"
	}
}

func (s *Store) ListMembers(ctx context.Context, params SpaceReadParams) ([]Membership, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin membership list: %w", err)
	}
	defer tx.Rollback()
	space, err := requireReadableSpace(ctx, tx, params.Actor, params.SpaceID, params.Now)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT space_id, principal_kind, principal_id, joined_at
		FROM space_memberships
		WHERE space_id = ?
		ORDER BY principal_kind, principal_id
	`, space.ID)
	if err != nil {
		return nil, fmt.Errorf("list space memberships: %w", err)
	}
	var memberships []Membership
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan space membership: %w", err)
		}
		membership.Principal.OrganizationID = space.OrganizationID
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate space memberships: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close space memberships: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit membership list: %w", err)
	}
	return memberships, nil
}

type spaceArchiveChange string

const (
	spaceArchive   spaceArchiveChange = operationArchiveSpace
	spaceUnarchive spaceArchiveChange = operationUnarchiveSpace
)

func (change spaceArchiveChange) auditAction() string {
	if change == spaceArchive {
		return AuditSpaceArchive
	}
	return AuditSpaceUnarchive
}

func (s *Store) ArchiveSpace(ctx context.Context, params ChangeSpaceArchiveParams) (MutationReceipt, error) {
	return s.changeSpaceArchive(ctx, params, spaceArchive)
}

func (s *Store) UnarchiveSpace(ctx context.Context, params ChangeSpaceArchiveParams) (MutationReceipt, error) {
	return s.changeSpaceArchive(ctx, params, spaceUnarchive)
}

func (s *Store) changeSpaceArchive(ctx context.Context, params ChangeSpaceArchiveParams, change spaceArchiveChange) (MutationReceipt, error) {
	operation := string(change)
	action := change.auditAction()
	archiving := change == spaceArchive
	fingerprint, err := collaborationFingerprint(struct {
		ActorKind PrincipalKind `json:"actor_kind"`
		ActorID   string        `json:"actor_id"`
		SpaceID   string        `json:"space_id"`
	}{params.Actor.Kind, params.Actor.ID, params.SpaceID})
	if err != nil {
		return MutationReceipt{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MutationReceipt{}, fmt.Errorf("begin space archive change: %w", err)
	}
	defer tx.Rollback()
	if receipt, found, err := readCollaborationReceipt(ctx, tx, params.RequestID, operation, fingerprint); err != nil {
		return MutationReceipt{}, err
	} else if found {
		return commitMutationReplay(tx, params.RequestID, receipt)
	}
	if err := requireCollaborationGrant(ctx, tx, params.Actor, CapabilitySpaceArchive,
		Scope{Kind: "space", ID: params.SpaceID}, action, "space", params.SpaceID,
		params.RequestID, params.Now); err != nil {
		return MutationReceipt{}, err
	}
	space, err := loadMutationSpace(ctx, tx, params.Actor, params.SpaceID)
	if err != nil {
		return MutationReceipt{}, denyCollaboration(ctx, tx, params.Actor, action, "space", params.SpaceID, params.RequestID, "space_unavailable", params.Now, err)
	}
	if err := collaborationSpace(space).ValidateArchiveChange(); err != nil {
		return MutationReceipt{}, denyCollaboration(ctx, tx, params.Actor, action, "space", params.SpaceID, params.RequestID, "dm_immutable", params.Now, err)
	}
	alreadyDesired := (archiving && space.ArchivedAt != nil) || (!archiving && space.ArchivedAt == nil)
	if !alreadyDesired {
		if archiving {
			_, err = tx.ExecContext(ctx, `UPDATE spaces SET archived_at = ?, updated_at = max(updated_at, ?) WHERE id = ?`, unixNano(params.Now), unixNano(params.Now), space.ID)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE spaces SET archived_at = NULL, updated_at = max(updated_at, ?) WHERE id = ?`, unixNano(params.Now), space.ID)
		}
		if err != nil {
			return MutationReceipt{}, fmt.Errorf("persist space archive change: %w", err)
		}
	}
	if err := persistCollaborationReceipt(ctx, tx, params.RequestID, operation, fingerprint, space.ID, params.Now); err != nil {
		return MutationReceipt{}, err
	}
	if !alreadyDesired {
		if err := appendAuditEvent(ctx, tx, AppendAuditParams{
			OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: action,
			TargetKind: "space", TargetID: space.ID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now,
		}); err != nil {
			return MutationReceipt{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MutationReceipt{}, fmt.Errorf("commit space archive change: %w", err)
	}
	return MutationReceipt{RequestID: params.RequestID, CommittedAt: params.Now.UTC()}, nil
}

func commitSpaceReplay(tx *sql.Tx, spaceID string) (Space, error) {
	space, err := scanSpace(tx.QueryRow(spaceSelect+` WHERE id = ?`, spaceID))
	if err != nil {
		return Space{}, fmt.Errorf("read space request result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Space{}, fmt.Errorf("commit space request replay: %w", err)
	}
	return space, nil
}

func requireReadableSpace(ctx context.Context, tx *sql.Tx, actor Principal, spaceID string, now time.Time) (Space, error) {
	reason, err := requireGrant(ctx, tx, actor, CapabilitySpaceRead, Scope{Kind: "space", ID: spaceID}, now, "")
	if err != nil {
		return Space{}, err
	}
	if reason != "" {
		return Space{}, ErrPermissionDenied
	}
	space, err := scanSpace(tx.QueryRowContext(ctx, spaceSelect+` WHERE id = ? AND organization_id = ?`, spaceID, actor.OrganizationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Space{}, ErrSpaceNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("read space: %w", err)
	}
	if err := validatePrincipalInOrganization(ctx, tx, actor, actor.OrganizationID); err != nil {
		return Space{}, ErrPermissionDenied
	}
	if err := requireActiveMembership(ctx, tx, space.ID, actor); err != nil {
		return Space{}, err
	}
	return space, nil
}

func loadMutationSpace(ctx context.Context, tx *sql.Tx, actor Principal, spaceID string) (Space, error) {
	space, err := scanSpace(tx.QueryRowContext(ctx, spaceSelect+` WHERE id = ? AND organization_id = ?`, spaceID, actor.OrganizationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Space{}, ErrSpaceNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("read mutation space: %w", err)
	}
	if err := validatePrincipalInOrganization(ctx, tx, actor, actor.OrganizationID); err != nil {
		return Space{}, ErrPermissionDenied
	}
	if err := requireActiveMembership(ctx, tx, space.ID, actor); err != nil {
		return Space{}, err
	}
	return space, nil
}
