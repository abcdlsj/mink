package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func (s *Store) RequestWorkApproval(ctx context.Context, params RequestWorkApprovalParams) (WorkApproval, error) {
	if err := validateWorkText(params.Question, 20000); err != nil {
		return WorkApproval{}, err
	}
	fingerprint, err := workApprovalRequestFingerprint(params)
	if err != nil {
		return WorkApproval{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkApproval{}, err
	}
	defer tx.Rollback()
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Agent, params.Now)
	if err != nil {
		return WorkApproval{}, err
	}
	params.Actor = actor
	if _, _, err := requireToolRun(ctx, tx, params.Agent, params.Run, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if receipt, found, err := readWorkReceipt(ctx, tx, params.RequestID, workRequestApprovalRequest, params.Actor, fingerprint); err != nil || found {
		if err != nil {
			return WorkApproval{}, err
		}
		approval, err := approvalByID(ctx, tx, receipt.ResultID)
		if err != nil {
			return WorkApproval{}, err
		}
		if err := tx.Commit(); err != nil {
			return WorkApproval{}, err
		}
		return approval, nil
	}
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ? AND organization_id = ?`, params.WorkID, params.Actor.OrganizationID))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkApproval{}, ErrWorkNotFound
	} else if err != nil {
		return WorkApproval{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return WorkApproval{}, err
	}
	previousRevision := knowledgeWorkRevision(work)
	if err := requireWorkGrant(ctx, tx, params.Actor, CapabilityWorkManage, work.ID, work.SourceSpaceID, AuditWorkApprovalRequest, params.RequestID, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if work.State != WorkStateOpen && work.State != WorkStateBlocked {
		return WorkApproval{}, ErrWorkTransitionInvalid
	}
	approvalID := uuid.NewString()
	stamp := unixNano(params.Now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_approvals(id, work_id, organization_id, status, question, requested_by_kind, requested_by_id, requested_at) VALUES(?, ?, ?, 'pending', ?, ?, ?, ?)`, approvalID, work.ID, work.OrganizationID, params.Question, params.Actor.Kind, params.Actor.ID, stamp); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return WorkApproval{}, ErrWorkApprovalConflict
		}
		return WorkApproval{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE works SET state = 'waiting_approval', blocking_reason = '', result = '', updated_at = ?, state_changed_at = ? WHERE id = ? AND organization_id = ?`, stamp, stamp, work.ID, work.OrganizationID); err != nil {
		return WorkApproval{}, err
	}
	if err := insertWorkEvent(ctx, tx, work.ID, work.OrganizationID, "approval.requested", params.Actor, "", "", "approval", approvalID, params.Question, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if err := insertWorkEvent(ctx, tx, work.ID, work.OrganizationID, "state.transitioned", params.Actor, work.State, WorkStateWaitingApproval, "approval", approvalID, "", params.Now); err != nil {
		return WorkApproval{}, err
	}
	if err := persistWorkReceipt(ctx, tx, params.RequestID, params.Actor, workRequestApprovalRequest, fingerprint, "approval", approvalID, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: work.OrganizationID, Actor: params.Actor, Action: AuditWorkApprovalRequest, TargetKind: "work", TargetID: work.ID, ContextKind: "space", ContextID: work.SourceSpaceID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return WorkApproval{}, err
	}
	work, err = scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ? AND organization_id = ?`, work.ID, work.OrganizationID))
	if err != nil {
		return WorkApproval{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return WorkApproval{}, err
	}
	if revision := knowledgeWorkRevision(work); revision != previousRevision {
		if _, err := enqueueKnowledgeDirtySource(ctx, tx, KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, revision, params.Now); err != nil {
			return WorkApproval{}, err
		}
	}
	approval, err := approvalByID(ctx, tx, approvalID)
	if err != nil {
		return WorkApproval{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkApproval{}, err
	}
	return approval, nil
}

func (s *Store) ResolveWorkApproval(ctx context.Context, params ResolveWorkApprovalParams) (WorkApproval, error) {
	if params.Decision != "approved" && params.Decision != "rejected" {
		return WorkApproval{}, ErrPermissionDenied
	}
	if params.Decision == "rejected" && validateWorkText(params.Note, 20000) != nil {
		return WorkApproval{}, ErrWorkInvalid
	}
	fingerprint, err := workApprovalResolveFingerprint(params)
	if err != nil {
		return WorkApproval{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkApproval{}, err
	}
	defer tx.Rollback()
	if receipt, found, err := readWorkReceipt(ctx, tx, params.RequestID, workRequestApprovalResolve, params.Actor, fingerprint); err != nil || found {
		if err != nil {
			return WorkApproval{}, err
		}
		approval, err := approvalByID(ctx, tx, receipt.ResultID)
		if err != nil {
			return WorkApproval{}, err
		}
		if err := tx.Commit(); err != nil {
			return WorkApproval{}, err
		}
		return approval, nil
	}
	var workID, organizationID, sourceSpace, status string
	if err := tx.QueryRowContext(ctx, `SELECT a.work_id, a.organization_id, w.source_space_id, a.status FROM work_approvals a JOIN works w ON w.id = a.work_id AND w.organization_id = a.organization_id WHERE a.id = ?`, params.ApprovalID).Scan(&workID, &organizationID, &sourceSpace, &status); errors.Is(err, sql.ErrNoRows) {
		return WorkApproval{}, ErrWorkApprovalNotFound
	} else if err != nil {
		return WorkApproval{}, err
	}
	if organizationID != params.Actor.OrganizationID || status != "pending" {
		return WorkApproval{}, ErrWorkApprovalConflict
	}
	if params.Actor.Kind != "human" {
		return WorkApproval{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkApprovalResolve, "work", workID, "space", sourceSpace, params.RequestID, "human_required", params.Now, ErrPermissionDenied)
	}
	if err := requireWorkGrant(ctx, tx, params.Actor, CapabilityWorkApprove, workID, sourceSpace, AuditWorkApprovalResolve, params.RequestID, params.Now); err != nil {
		return WorkApproval{}, err
	}
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ? AND organization_id = ?`, workID, organizationID))
	if err != nil {
		return WorkApproval{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return WorkApproval{}, err
	}
	previousRevision := knowledgeWorkRevision(work)
	stamp := unixNano(params.Now)
	nextState, reason := WorkStateOpen, ""
	if params.Decision == "rejected" {
		nextState, reason = WorkStateBlocked, params.Note
	}
	if _, err := tx.ExecContext(ctx, `UPDATE work_approvals SET status = ?, decided_by_human_id = ?, decision_note = ?, decided_at = ? WHERE id = ? AND status = 'pending'`, params.Decision, params.Actor.ID, params.Note, stamp, params.ApprovalID); err != nil {
		return WorkApproval{}, err
	}
	var previousState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM works WHERE id = ? AND organization_id = ?`, workID, organizationID).Scan(&previousState); err != nil {
		return WorkApproval{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE works SET state = ?, blocking_reason = ?, result = '', updated_at = ?, state_changed_at = ? WHERE id = ? AND organization_id = ?`, nextState, reason, stamp, stamp, workID, organizationID); err != nil {
		return WorkApproval{}, err
	}
	if err := insertWorkEvent(ctx, tx, workID, organizationID, "approval.resolved", params.Actor, "", "", "approval", params.ApprovalID, params.Note, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if err := insertWorkEvent(ctx, tx, workID, organizationID, "state.transitioned", params.Actor, previousState, nextState, "approval", params.ApprovalID, reason, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if err := persistWorkReceipt(ctx, tx, params.RequestID, params.Actor, workRequestApprovalResolve, fingerprint, "approval", params.ApprovalID, params.Now); err != nil {
		return WorkApproval{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: organizationID, Actor: params.Actor, Action: AuditWorkApprovalResolve, TargetKind: "work", TargetID: workID, ContextKind: "space", ContextID: sourceSpace, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return WorkApproval{}, err
	}
	work, err = scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ? AND organization_id = ?`, workID, organizationID))
	if err != nil {
		return WorkApproval{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return WorkApproval{}, err
	}
	if revision := knowledgeWorkRevision(work); revision != previousRevision {
		if _, err := enqueueKnowledgeDirtySource(ctx, tx, KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, revision, params.Now); err != nil {
			return WorkApproval{}, err
		}
	}
	approval, err := approvalByID(ctx, tx, params.ApprovalID)
	if err != nil {
		return WorkApproval{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkApproval{}, err
	}
	return approval, nil
}

func approvalByID(ctx context.Context, tx *sql.Tx, id string) (WorkApproval, error) {
	var value WorkApproval
	var requested, decided sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT id, work_id, organization_id, status, question, requested_by_kind, requested_by_id, requested_at, COALESCE(decided_by_human_id, ''), decision_note, decided_at FROM work_approvals WHERE id = ?`, id).Scan(&value.ID, &value.WorkID, &value.OrganizationID, &value.Status, &value.Question, &value.RequestedBy.Kind, &value.RequestedBy.ID, &requested, &value.DecidedByHumanID, &value.DecisionNote, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkApproval{}, ErrWorkApprovalNotFound
	}
	if err != nil {
		return WorkApproval{}, err
	}
	value.RequestedBy.OrganizationID = value.OrganizationID
	value.RequestedAt = timeFromUnixNano(requested.Int64)
	if decided.Valid {
		stamp := timeFromUnixNano(decided.Int64)
		value.DecidedAt = &stamp
	}
	return value, nil
}
