package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (s *Store) AssignWork(ctx context.Context, params AssignWorkParams) (WorkAssignment, error) {
	if params.Role != WorkAssignmentCoordinator && params.Role != WorkAssignmentContributor {
		return WorkAssignment{}, ErrWorkInvalid
	}
	fingerprint, err := workAssignFingerprint(params)
	if err != nil {
		return WorkAssignment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkAssignment{}, err
	}
	defer tx.Rollback()
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Agent, params.Now)
	if err != nil {
		return WorkAssignment{}, err
	}
	params.Actor = actor
	if _, _, err := requireToolRun(ctx, tx, params.Agent, params.Run, params.Now); err != nil {
		return WorkAssignment{}, err
	}
	if receipt, found, err := readWorkReceipt(ctx, tx, params.RequestID, workRequestAssign, params.Actor, fingerprint); err != nil || found {
		if err != nil {
			return WorkAssignment{}, err
		}
		assignment, err := assignmentByID(ctx, tx, receipt.ResultID)
		if err != nil {
			return WorkAssignment{}, err
		}
		if err := tx.Commit(); err != nil {
			return WorkAssignment{}, err
		}
		return assignment, nil
	}
	var sourceSpace string
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT source_space_id, state FROM works WHERE id = ? AND organization_id = ?`, params.WorkID, params.Actor.OrganizationID).Scan(&sourceSpace, &state); errors.Is(err, sql.ErrNoRows) {
		return WorkAssignment{}, ErrWorkNotFound
	} else if err != nil {
		return WorkAssignment{}, err
	}
	if err := requireWorkGrant(ctx, tx, params.Actor, CapabilityWorkManage, params.WorkID, sourceSpace, AuditWorkAssign, params.RequestID, params.Now); err != nil {
		return WorkAssignment{}, err
	}
	if state == WorkStateCompleted || state == WorkStateFailed || state == WorkStateCancelled {
		return WorkAssignment{}, ErrWorkTerminal
	}
	var desiredRevision uint64
	var computerID, placementState string
	if err := tx.QueryRowContext(ctx, `SELECT computer_id, desired_revision, state FROM agent_placements WHERE agent_id = ?`, params.AgentID).Scan(&computerID, &desiredRevision, &placementState); errors.Is(err, sql.ErrNoRows) {
		return WorkAssignment{}, ErrWorkPlacementInvalid
	} else if err != nil {
		return WorkAssignment{}, err
	}
	if placementState != "ready" {
		return WorkAssignment{}, ErrWorkPlacementInvalid
	}
	if exists, err := agentExists(ctx, tx, params.AgentID); err != nil || !exists {
		return WorkAssignment{}, ErrPrincipalNotFound
	}
	if err := endWorkAssignments(ctx, tx, params.WorkID, params.AgentID, params.Role, params.Actor, "reassigned", params.Now); err != nil {
		return WorkAssignment{}, err
	}
	assignmentID := uuid.NewString()
	stamp := unixNano(params.Now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_assignments(id, work_id, organization_id, role, agent_id, holder_computer_id, holder_placement_desired_revision, assigned_by_kind, assigned_by_id, assigned_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, assignmentID, params.WorkID, params.Actor.OrganizationID, params.Role, params.AgentID, computerID, desiredRevision, params.Actor.Kind, params.Actor.ID, stamp); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return WorkAssignment{}, ErrWorkAssignmentConflict
		}
		return WorkAssignment{}, err
	}
	var teamSpace string
	if err := tx.QueryRowContext(ctx, `SELECT team_space_id FROM works WHERE id = ?`, params.WorkID).Scan(&teamSpace); err != nil {
		return WorkAssignment{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO space_memberships(space_id, principal_kind, principal_id, joined_at) VALUES(?, 'agent', ?, ?)`, teamSpace, params.AgentID, stamp); err != nil {
		return WorkAssignment{}, err
	}
	if err := insertWorkEvent(ctx, tx, params.WorkID, params.Actor.OrganizationID, "assignment.started", params.Actor, "", "", "assignment", assignmentID, "", params.Now); err != nil {
		return WorkAssignment{}, err
	}
	if err := persistWorkReceipt(ctx, tx, params.RequestID, params.Actor, workRequestAssign, fingerprint, "assignment", assignmentID, params.Now); err != nil {
		return WorkAssignment{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditWorkAssign, TargetKind: "work", TargetID: params.WorkID, ContextKind: "space", ContextID: sourceSpace, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return WorkAssignment{}, err
	}
	assignment, err := assignmentByID(ctx, tx, assignmentID)
	if err != nil {
		return WorkAssignment{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkAssignment{}, err
	}
	return assignment, nil
}

func assignmentByID(ctx context.Context, tx *sql.Tx, id string) (WorkAssignment, error) {
	var value WorkAssignment
	var assigned, ended sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, work_id, organization_id, role, agent_id, holder_computer_id, holder_placement_desired_revision, assigned_by_kind, assigned_by_id, assigned_at, ended_at, end_reason FROM work_assignments WHERE id = ?`, id).Scan(&value.ID, &value.WorkID, &value.OrganizationID, &value.Role, &value.AgentID, &value.HolderComputerID, &value.HolderPlacementDesiredRevision, &value.AssignedBy.Kind, &value.AssignedBy.ID, &assigned, &ended, &value.EndReason)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkAssignment{}, ErrWorkAssignmentConflict
	}
	if err != nil {
		return WorkAssignment{}, err
	}
	value.AssignedBy.OrganizationID = value.OrganizationID
	value.AssignedAt = timeFromUnixNano(assigned.Int64)
	if ended.Valid {
		stamp := timeFromUnixNano(ended.Int64)
		value.EndedAt = &stamp
	}
	return value, nil
}

func validWorkTransition(from, to string) bool {
	if from == WorkStateCompleted || from == WorkStateFailed || from == WorkStateCancelled {
		return false
	}
	switch from {
	case WorkStateOpen:
		return to == WorkStateBlocked || to == WorkStateCompleted || to == WorkStateFailed || to == WorkStateCancelled
	case WorkStateBlocked:
		return to == WorkStateOpen || to == WorkStateFailed || to == WorkStateCancelled
	case WorkStateWaitingApproval:
		return to == WorkStateCancelled
	default:
		return false
	}
}
