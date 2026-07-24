package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) TransitionWork(ctx context.Context, params TransitionWorkParams) (Work, error) {
	if params.ToState == WorkStateWaitingApproval || params.ToState == "" {
		return Work{}, ErrWorkTransitionInvalid
	}
	fingerprint, err := workTransitionFingerprint(params)
	if err != nil {
		return Work{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
	actor, err := recheckMessageActor(ctx, tx, params.Actor, params.Agent, params.Now)
	if err != nil {
		return Work{}, err
	}
	params.Actor = actor
	if _, _, err := requireToolRun(ctx, tx, params.Agent, params.Run, params.Now); err != nil {
		return Work{}, err
	}
	if receipt, found, err := readWorkReceipt(ctx, tx, params.RequestID, workRequestTransition, params.Actor, fingerprint); err != nil || found {
		if err != nil {
			return Work{}, err
		}
		return workReplay(ctx, tx, receipt)
	}
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ? AND organization_id = ?`, params.WorkID, params.Actor.OrganizationID))
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrWorkNotFound
	} else if err != nil {
		return Work{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return Work{}, err
	}
	previousRevision := knowledgeWorkRevision(work)
	if err := requireWorkGrant(ctx, tx, params.Actor, CapabilityWorkManage, work.ID, work.SourceSpaceID, AuditWorkTransition, params.RequestID, params.Now); err != nil {
		return Work{}, err
	}
	if !validWorkTransition(work.State, params.ToState) {
		return Work{}, ErrWorkTransitionInvalid
	}
	if params.ToState == WorkStateBlocked && validateWorkText(params.Reason, 20000) != nil {
		return Work{}, ErrWorkInvalid
	}
	if (params.ToState == WorkStateCompleted || params.ToState == WorkStateFailed) && validateWorkText(params.Result, 400000) != nil {
		return Work{}, ErrWorkInvalid
	}
	if params.ToState == WorkStateCompleted {
		if err := ensureCriteriaSatisfied(ctx, tx, work, params.CriterionResults); err != nil {
			return Work{}, err
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_approvals WHERE work_id = ? AND status = 'pending')`, work.ID).Scan(&pending); err != nil {
			return Work{}, err
		}
		if pending {
			return Work{}, ErrWorkTransitionInvalid
		}
		var children bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM works WHERE parent_work_id = ? AND organization_id = ? AND state != 'completed')`, work.ID, work.OrganizationID).Scan(&children); err != nil {
			return Work{}, err
		}
		if children {
			return Work{}, ErrWorkTransitionInvalid
		}
	}
	for _, result := range params.CriterionResults {
		if err := insertCriterionResult(ctx, tx, work, params.Actor, result, params.Now); err != nil {
			return Work{}, err
		}
	}
	stamp := unixNano(params.Now)
	completed, failed, cancelled := "NULL", "NULL", "NULL"
	if params.ToState == WorkStateCompleted {
		completed = "?"
	} else if params.ToState == WorkStateFailed {
		failed = "?"
	} else if params.ToState == WorkStateCancelled {
		cancelled = "?"
	}
	query := `UPDATE works SET state = ?, blocking_reason = ?, result = ?, updated_at = ?, state_changed_at = ?, completed_at = ` + completed + `, failed_at = ` + failed + `, cancelled_at = ` + cancelled + ` WHERE id = ? AND organization_id = ?`
	args := []any{params.ToState, "", "", stamp, stamp}
	if params.ToState == WorkStateBlocked {
		args[1] = params.Reason
	}
	if params.ToState == WorkStateCompleted || params.ToState == WorkStateFailed {
		args[2] = params.Result
	}
	if completed == "?" {
		args = append(args, stamp)
	}
	if failed == "?" {
		args = append(args, stamp)
	}
	if cancelled == "?" {
		args = append(args, stamp)
	}
	args = append(args, work.ID, work.OrganizationID)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return Work{}, err
	}
	if params.ToState == WorkStateCompleted || params.ToState == WorkStateFailed || params.ToState == WorkStateCancelled {
		reason := params.ToState
		if params.ToState == WorkStateCancelled {
			if _, err := tx.ExecContext(ctx, `UPDATE work_approvals SET status = 'cancelled', decided_at = ? WHERE work_id = ? AND status = 'pending'`, stamp, work.ID); err != nil {
				return Work{}, err
			}
		}
		if err := endWorkAssignments(ctx, tx, work.ID, "", "", params.Actor, reason, params.Now); err != nil {
			return Work{}, err
		}
	}
	if err := insertWorkEvent(ctx, tx, work.ID, work.OrganizationID, "state.transitioned", params.Actor, work.State, params.ToState, "", "", params.Reason, params.Now); err != nil {
		return Work{}, err
	}
	if err := persistWorkReceipt(ctx, tx, params.RequestID, params.Actor, workRequestTransition, fingerprint, "work", work.ID, params.Now); err != nil {
		return Work{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: work.OrganizationID, Actor: params.Actor, Action: AuditWorkTransition, TargetKind: "work", TargetID: work.ID, ContextKind: "space", ContextID: work.SourceSpaceID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return Work{}, err
	}
	work, err = scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, work.ID))
	if err != nil {
		return Work{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return Work{}, err
	}
	if revision := knowledgeWorkRevision(work); revision != previousRevision {
		if _, err := enqueueKnowledgeDirtySource(ctx, tx, KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, revision, params.Now); err != nil {
			return Work{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Work{}, err
	}
	return work, nil
}

func ensureCriteriaSatisfied(ctx context.Context, tx *sql.Tx, work Work, inputs []WorkCriterionResultInput) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM work_acceptance_criteria WHERE work_id = ? AND organization_id = ? ORDER BY ordinal`, work.ID, work.OrganizationID)
	if err != nil {
		return err
	}
	defer rows.Close()
	latest := map[string]string{}
	for rows.Next() {
		var criterionID string
		if err := rows.Scan(&criterionID); err != nil {
			return err
		}
		var verdict string
		err := tx.QueryRowContext(ctx, `SELECT verdict FROM work_acceptance_results WHERE work_id = ? AND criterion_id = ? ORDER BY sequence DESC LIMIT 1`, work.ID, criterionID).Scan(&verdict)
		if errors.Is(err, sql.ErrNoRows) {
			latest[criterionID] = ""
		} else if err != nil {
			return err
		} else {
			latest[criterionID] = verdict
		}
	}
	if err := workRowsErr(ctx, workRowsCompletion, rows); err != nil {
		return fmt.Errorf("iterate work completion criteria: %w", err)
	}
	for _, input := range inputs {
		if input.Verdict != "passed" && input.Verdict != "failed" {
			return ErrWorkInvalid
		}
		if _, ok := latest[input.CriterionID]; !ok {
			return ErrWorkInvalid
		}
		latest[input.CriterionID] = input.Verdict
	}
	for _, verdict := range latest {
		if verdict != "passed" {
			return ErrWorkAcceptanceIncomplete
		}
	}
	return nil
}

func insertCriterionResult(ctx context.Context, tx *sql.Tx, work Work, actor Principal, input WorkCriterionResultInput, now time.Time) error {
	if input.Verdict != "passed" && input.Verdict != "failed" || validateWorkText(input.Evidence, 20000) != nil {
		return ErrWorkInvalid
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_acceptance_criteria WHERE id = ? AND work_id = ? AND organization_id = ?)`, input.CriterionID, work.ID, work.OrganizationID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrWorkInvalid
	}
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_acceptance_results(id, work_id, organization_id, criterion_id, verdict, evidence, actor_kind, actor_id, occurred_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, work.ID, work.OrganizationID, input.CriterionID, input.Verdict, input.Evidence, actor.Kind, actor.ID, unixNano(now)); err != nil {
		return err
	}
	return insertWorkEvent(ctx, tx, work.ID, work.OrganizationID, "acceptance.recorded", actor, "", "", "criterion_result", id, "", now)
}
