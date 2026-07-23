package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) GetWork(ctx context.Context, params WorkReadParams) (Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, fmt.Errorf("begin work read: %w", err)
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, params.WorkID))
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrWorkNotFound
	} else if err != nil {
		return Work{}, err
	}
	if work.OrganizationID != params.Actor.OrganizationID {
		return Work{}, ErrWorkNotFound
	}
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return Work{}, ErrPermissionDenied
	}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityWorkRead, Scope{Kind: "work", ID: work.ID}, params.Now, ""); err != nil {
		return Work{}, err
	} else if reason != "" {
		return Work{}, ErrPermissionDenied
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return Work{}, err
	}
	if err := tx.Commit(); err != nil {
		return Work{}, fmt.Errorf("commit work read: %w", err)
	}
	return work, nil
}

func (s *Store) ListWorks(ctx context.Context, params ListWorksParams) ([]Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin work list: %w", err)
	}
	defer tx.Rollback()
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return nil, ErrPermissionDenied
	}
	rows, err := tx.QueryContext(ctx, workSelect()+` WHERE organization_id = ? ORDER BY created_at, id`, params.Actor.OrganizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var works []Work
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			return nil, err
		}
		if reason, err := requireGrant(ctx, tx, params.Actor, CapabilityWorkRead, Scope{Kind: "work", ID: work.ID}, params.Now, ""); err != nil {
			return nil, err
		} else if reason != "" {
			continue
		}
		if err := loadWorkParts(ctx, tx, &work); err != nil {
			return nil, err
		}
		works = append(works, work)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return works, nil
}

const (
	workPageDefaultLimit  = 50
	workPageMaxLimit      = 200
	workPageCandidateScan = 400
)

func (s *Store) GetWorkDetail(ctx context.Context, params WorkReadParams) (WorkDetail, error) {
	tx, actor, err := s.beginWorkReadTransaction(ctx, params.Actor, params.Agent, params.Now)
	if err != nil {
		return WorkDetail{}, err
	}
	defer tx.Rollback()
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, params.WorkID))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && work.OrganizationID != actor.OrganizationID) {
		return WorkDetail{}, ErrWorkNotFound
	}
	if err != nil {
		return WorkDetail{}, err
	}
	if reason, err := requireGrant(ctx, tx, actor, CapabilityWorkRead, Scope{Kind: "work", ID: work.ID}, params.Now, ""); err != nil {
		return WorkDetail{}, err
	} else if reason != "" {
		return WorkDetail{}, ErrPermissionDenied
	}
	detail, err := loadWorkDetail(ctx, tx, work)
	if err != nil {
		return WorkDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkDetail{}, fmt.Errorf("commit work detail read: %w", err)
	}
	return detail, nil
}

func (s *Store) ListWorkPage(ctx context.Context, params ListWorkPageParams) (WorkPage, error) {
	if params.Now.IsZero() || params.Limit > workPageMaxLimit {
		return WorkPage{}, ErrWorkInvalid
	}
	limit := params.Limit
	if limit == 0 {
		limit = workPageDefaultLimit
	}
	tx, actor, err := s.beginWorkReadTransaction(ctx, params.Actor, params.Agent, params.Now)
	if err != nil {
		return WorkPage{}, err
	}
	defer tx.Rollback()
	binding := WorkCursorBinding{
		PrincipalFingerprint: workCursorPrincipalFingerprint(actor),
		OrganizationID:       actor.OrganizationID,
	}
	var after *WorkCursorSeekKey
	if params.Cursor != "" {
		seek, err := s.OpenWorkCursor(params.Cursor, binding)
		if err != nil {
			return WorkPage{}, ErrWorkCursorUnavailable
		}
		after = &seek
	}
	candidates, err := listWorkPageCandidates(ctx, tx, actor.OrganizationID, after)
	if err != nil {
		return WorkPage{}, err
	}
	page := WorkPage{Works: make([]Work, 0, limit)}
	var last *WorkCursorSeekKey
	scanned := 0
	for _, candidate := range candidates {
		if scanned == workPageCandidateScan || len(page.Works) == int(limit) {
			break
		}
		scanned++
		seek := workCursorSeek(candidate)
		last = &seek
		reason, err := requireGrant(ctx, tx, actor, CapabilityWorkRead, Scope{Kind: "work", ID: candidate.ID}, params.Now, "")
		if err != nil {
			return WorkPage{}, err
		}
		if reason == "" {
			page.Works = append(page.Works, candidate)
		}
	}
	hasRawContinuation := len(candidates) > scanned
	if scanned == workPageCandidateScan {
		hasRawContinuation = len(candidates) == workPageCandidateScan+1
	}
	if hasRawContinuation && last != nil {
		cursor, err := s.SealWorkCursor(binding, *last)
		if err != nil {
			return WorkPage{}, ErrWorkCursorUnavailable
		}
		page.NextCursor = cursor
	}
	if err := tx.Commit(); err != nil {
		return WorkPage{}, fmt.Errorf("commit work page read: %w", err)
	}
	return page, nil
}

func (s *Store) beginWorkReadTransaction(ctx context.Context, supplied Principal, runtime AgentRuntimeAuthentication, now time.Time) (*sql.Tx, Principal, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, Principal{}, fmt.Errorf("begin work read: %w", err)
	}
	humanProvided := supplied.Kind != "" || supplied.ID != "" || supplied.OrganizationID != ""
	runtimeProvided := runtime.Principal.Kind != "" || runtime.Principal.ID != "" || runtime.Principal.OrganizationID != "" || runtime.Proof.Valid()
	if now.IsZero() || humanProvided == runtimeProvided {
		tx.Rollback()
		if runtimeProvided {
			return nil, Principal{}, ErrAgentRuntimeUnauthenticated
		}
		return nil, Principal{}, ErrPermissionDenied
	}
	if humanProvided {
		if supplied.Kind != "human" || !supplied.Valid() {
			tx.Rollback()
			return nil, Principal{}, ErrPermissionDenied
		}
		if err := validatePrincipalInOrganization(ctx, tx, supplied, supplied.OrganizationID); err != nil {
			tx.Rollback()
			if errors.Is(err, ErrPrincipalNotFound) || errors.Is(err, ErrInvalidPrincipal) {
				return nil, Principal{}, ErrPermissionDenied
			}
			return nil, Principal{}, err
		}
		return tx, supplied, nil
	}
	if !runtime.Valid() {
		tx.Rollback()
		return nil, Principal{}, ErrAgentRuntimeUnauthenticated
	}
	current, err := requireAgentRuntimeSession(ctx, tx, runtime.Proof, now)
	if err != nil {
		tx.Rollback()
		return nil, Principal{}, err
	}
	if current.Principal != runtime.Principal {
		tx.Rollback()
		return nil, Principal{}, ErrAgentRuntimeUnauthenticated
	}
	return tx, current.Principal, nil
}

func listWorkPageCandidates(ctx context.Context, tx *sql.Tx, organizationID string, after *WorkCursorSeekKey) ([]Work, error) {
	query := workSelect() + ` WHERE organization_id = ?`
	args := []any{organizationID}
	if after != nil {
		query += ` AND (` + workPageAfterSQL(*after) + `)`
		args = append(args, workPageAfterArgs(*after)...)
	}
	query += ` ORDER BY root_work_id, parent_work_id, created_at, id LIMIT ?`
	args = append(args, workPageCandidateScan+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list work page candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]Work, 0, workPageCandidateScan+1)
	for rows.Next() {
		work, err := scanWork(rows)
		if err != nil {
			return nil, fmt.Errorf("scan work page candidate: %w", err)
		}
		candidates = append(candidates, work)
	}
	if err := workRowsErr(ctx, workRowsPage, rows); err != nil {
		return nil, fmt.Errorf("iterate work page candidates: %w", err)
	}
	return candidates, nil
}

func workPageAfterSQL(after WorkCursorSeekKey) string {
	if after.ParentIsNull {
		return `root_work_id > ? OR (root_work_id = ? AND parent_work_id IS NOT NULL)`
	}
	return `root_work_id > ? OR (root_work_id = ? AND (parent_work_id > ? OR (parent_work_id = ? AND (created_at > ? OR (created_at = ? AND id > ?)))))`
}

func workPageAfterArgs(after WorkCursorSeekKey) []any {
	if after.ParentIsNull {
		return []any{after.RootWorkID, after.RootWorkID}
	}
	created := unixNano(after.CreatedAt)
	return []any{after.RootWorkID, after.RootWorkID, after.ParentWorkID, after.ParentWorkID, created, created, after.ID}
}

func workCursorSeek(work Work) WorkCursorSeekKey {
	return WorkCursorSeekKey{
		RootWorkID: work.RootWorkID, ParentWorkID: work.ParentWorkID,
		ParentIsNull: work.parentWorkIDNull, CreatedAt: work.CreatedAt, ID: work.ID,
	}
}

func loadWorkDetail(ctx context.Context, tx *sql.Tx, work Work) (WorkDetail, error) {
	detail := WorkDetail{Work: work}
	if err := loadWorkParts(ctx, tx, &detail.Work); err != nil {
		return WorkDetail{}, err
	}
	var err error
	if detail.Assignments, err = listWorkAssignments(ctx, tx, detail.Work); err != nil {
		return WorkDetail{}, err
	}
	if detail.Approvals, err = listWorkApprovals(ctx, tx, detail.Work); err != nil {
		return WorkDetail{}, err
	}
	if detail.CriterionResults, err = listWorkCriterionResults(ctx, tx, detail.Work); err != nil {
		return WorkDetail{}, err
	}
	if detail.Events, err = listWorkEvents(ctx, tx, detail.Work); err != nil {
		return WorkDetail{}, err
	}
	return detail, nil
}

func listWorkAssignments(ctx context.Context, tx *sql.Tx, work Work) ([]WorkAssignment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, work_id, organization_id, role, agent_id, holder_computer_id, holder_placement_generation, assigned_by_kind, assigned_by_id, assigned_at, ended_at, end_reason FROM work_assignments WHERE work_id = ? AND organization_id = ? ORDER BY assigned_at, id`, work.ID, work.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("list work assignments: %w", err)
	}
	defer rows.Close()
	items := []WorkAssignment{}
	for rows.Next() {
		var item WorkAssignment
		var assigned int64
		var ended sql.NullInt64
		if err := rows.Scan(&item.ID, &item.WorkID, &item.OrganizationID, &item.Role, &item.AgentID, &item.HolderComputerID, &item.HolderPlacementGeneration, &item.AssignedBy.Kind, &item.AssignedBy.ID, &assigned, &ended, &item.EndReason); err != nil {
			return nil, fmt.Errorf("scan work assignment: %w", err)
		}
		item.AssignedBy.OrganizationID = item.OrganizationID
		item.AssignedAt = timeFromUnixNano(assigned)
		if ended.Valid {
			value := timeFromUnixNano(ended.Int64)
			item.EndedAt = &value
		}
		items = append(items, item)
	}
	if err := workRowsErr(ctx, workRowsAssignments, rows); err != nil {
		return nil, fmt.Errorf("iterate work assignments: %w", err)
	}
	return items, nil
}

func listWorkApprovals(ctx context.Context, tx *sql.Tx, work Work) ([]WorkApproval, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, work_id, organization_id, status, question, requested_by_kind, requested_by_id, requested_at, COALESCE(decided_by_human_id, ''), decision_note, decided_at FROM work_approvals WHERE work_id = ? AND organization_id = ? ORDER BY requested_at, id`, work.ID, work.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("list work approvals: %w", err)
	}
	defer rows.Close()
	items := []WorkApproval{}
	for rows.Next() {
		var item WorkApproval
		var requested int64
		var decided sql.NullInt64
		if err := rows.Scan(&item.ID, &item.WorkID, &item.OrganizationID, &item.Status, &item.Question, &item.RequestedBy.Kind, &item.RequestedBy.ID, &requested, &item.DecidedByHumanID, &item.DecisionNote, &decided); err != nil {
			return nil, fmt.Errorf("scan work approval: %w", err)
		}
		item.RequestedBy.OrganizationID = item.OrganizationID
		item.RequestedAt = timeFromUnixNano(requested)
		if decided.Valid {
			value := timeFromUnixNano(decided.Int64)
			item.DecidedAt = &value
		}
		items = append(items, item)
	}
	if err := workRowsErr(ctx, workRowsApprovals, rows); err != nil {
		return nil, fmt.Errorf("iterate work approvals: %w", err)
	}
	return items, nil
}

func listWorkCriterionResults(ctx context.Context, tx *sql.Tx, work Work) ([]WorkCriterionResult, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sequence, id, work_id, organization_id, criterion_id, verdict, evidence, actor_kind, actor_id, occurred_at FROM work_acceptance_results WHERE work_id = ? AND organization_id = ? ORDER BY sequence`, work.ID, work.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("list work criterion results: %w", err)
	}
	defer rows.Close()
	items := []WorkCriterionResult{}
	for rows.Next() {
		var item WorkCriterionResult
		var sequence, occurred int64
		if err := rows.Scan(&sequence, &item.ID, &item.WorkID, &item.OrganizationID, &item.CriterionID, &item.Verdict, &item.Evidence, &item.Actor.Kind, &item.Actor.ID, &occurred); err != nil {
			return nil, fmt.Errorf("scan work criterion result: %w", err)
		}
		item.Sequence = uint64(sequence)
		item.Actor.OrganizationID = item.OrganizationID
		item.OccurredAt = timeFromUnixNano(occurred)
		items = append(items, item)
	}
	if err := workRowsErr(ctx, workRowsResults, rows); err != nil {
		return nil, fmt.Errorf("iterate work criterion results: %w", err)
	}
	return items, nil
}

func listWorkEvents(ctx context.Context, tx *sql.Tx, work Work) ([]WorkEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sequence, id, work_id, organization_id, event_kind, actor_kind, actor_id, from_state, to_state, reference_kind, reference_id, reason, occurred_at FROM work_events WHERE work_id = ? AND organization_id = ? ORDER BY sequence`, work.ID, work.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("list work events: %w", err)
	}
	defer rows.Close()
	items := []WorkEvent{}
	for rows.Next() {
		var item WorkEvent
		var sequence, occurred int64
		if err := rows.Scan(&sequence, &item.ID, &item.WorkID, &item.OrganizationID, &item.Kind, &item.Actor.Kind, &item.Actor.ID, &item.FromState, &item.ToState, &item.ReferenceKind, &item.ReferenceID, &item.Reason, &occurred); err != nil {
			return nil, fmt.Errorf("scan work event: %w", err)
		}
		item.Sequence = uint64(sequence)
		item.Actor.OrganizationID = item.OrganizationID
		item.OccurredAt = timeFromUnixNano(occurred)
		items = append(items, item)
	}
	if err := workRowsErr(ctx, workRowsEvents, rows); err != nil {
		return nil, fmt.Errorf("iterate work events: %w", err)
	}
	return items, nil
}
