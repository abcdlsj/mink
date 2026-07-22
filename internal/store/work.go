package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	WorkStateOpen            = "open"
	WorkStateBlocked         = "blocked"
	WorkStateWaitingApproval = "waiting_approval"
	WorkStateCompleted       = "completed"
	WorkStateFailed          = "failed"
	WorkStateCancelled       = "cancelled"

	WorkAssignmentCoordinator = "coordinator"
	WorkAssignmentContributor = "contributor"
)

type Work struct {
	ID                   string
	OrganizationID       string
	RootWorkID           string
	ParentWorkID         string
	SourceMessageID      string
	SourceSpaceID        string
	SourceTarget         MessageTarget
	SourceTargetSequence uint64
	TeamSpaceID          string
	Goal                 string
	State                string
	BlockingReason       string
	Result               string
	Creator              Principal
	CreatedAt            time.Time
	UpdatedAt            time.Time
	StateChangedAt       time.Time
	CompletedAt          *time.Time
	FailedAt             *time.Time
	CancelledAt          *time.Time
	Constraints          []WorkText
	AcceptanceCriteria   []WorkCriterion
}

type WorkText struct {
	ID        string
	Ordinal   uint32
	Body      string
	CreatedAt time.Time
}

type WorkCriterion struct {
	ID        string
	Ordinal   uint32
	Body      string
	CreatedAt time.Time
}

type WorkCreateParams struct {
	RequestID            string
	Actor                Principal
	ParentWorkID         string
	SourceMessageID      string
	SourceSpaceID        string
	SourceTarget         MessageTarget
	SourceTargetSequence uint64
	Goal                 string
	Constraints          []string
	AcceptanceCriteria   []string
	Now                  time.Time
}

type WorkReadParams struct {
	Actor  Principal
	WorkID string
	Now    time.Time
}

type ListWorksParams struct {
	Actor Principal
	Now   time.Time
}

type WorkAssignment struct {
	ID                        string
	WorkID                    string
	OrganizationID            string
	Role                      string
	AgentID                   string
	HolderComputerID          string
	HolderPlacementGeneration uint64
	AssignedBy                Principal
	AssignedAt                time.Time
	EndedAt                   *time.Time
	EndReason                 string
}

type AssignWorkParams struct {
	RequestID string
	Actor     Principal
	WorkID    string
	Role      string
	AgentID   string
	Now       time.Time
}

type WorkCriterionResultInput struct {
	CriterionID string
	Verdict     string
	Evidence    string
}

type TransitionWorkParams struct {
	RequestID        string
	Actor            Principal
	WorkID           string
	ToState          string
	Reason           string
	Result           string
	CriterionResults []WorkCriterionResultInput
	Now              time.Time
}

type RequestWorkApprovalParams struct {
	RequestID string
	Actor     Principal
	WorkID    string
	Question  string
	Now       time.Time
}

type ResolveWorkApprovalParams struct {
	RequestID  string
	Actor      Principal
	ApprovalID string
	Decision   string
	Note       string
	Now        time.Time
}

type WorkApproval struct {
	ID               string
	WorkID           string
	OrganizationID   string
	Status           string
	Question         string
	RequestedBy      Principal
	RequestedAt      time.Time
	DecidedByHumanID string
	DecisionNote     string
	DecidedAt        *time.Time
}

type WorkEvent struct {
	Sequence       uint64
	ID             string
	WorkID         string
	OrganizationID string
	Kind           string
	Actor          Principal
	FromState      string
	ToState        string
	ReferenceKind  string
	ReferenceID    string
	Reason         string
	OccurredAt     time.Time
}

const (
	workRequestCreate          = "work.create"
	workRequestAssign          = "work.assign"
	workRequestTransition      = "work.transition"
	workRequestApprovalRequest = "work.approval.request"
	workRequestApprovalResolve = "work.approval.resolve"
)

func workFingerprint(value any) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode work request: %w", err)
	}
	return sha256.Sum256(payload), nil
}

type workReceipt struct {
	ActorKind, ActorID, Operation, ResultKind, ResultID string
	CommittedAt                                         time.Time
}

func readWorkReceipt(ctx context.Context, tx *sql.Tx, requestID, operation string, actor Principal, fingerprint [sha256.Size]byte) (workReceipt, bool, error) {
	var receipt workReceipt
	var stored []byte
	var committed int64
	err := tx.QueryRowContext(ctx, `
		SELECT actor_kind, actor_id, operation, payload_fingerprint, result_kind, result_id, committed_at
		FROM work_requests WHERE request_id = ?
	`, requestID).Scan(&receipt.ActorKind, &receipt.ActorID, &receipt.Operation, &stored, &receipt.ResultKind, &receipt.ResultID, &committed)
	if errors.Is(err, sql.ErrNoRows) {
		return workReceipt{}, false, nil
	}
	if err != nil {
		return workReceipt{}, false, fmt.Errorf("read work request receipt: %w", err)
	}
	if receipt.ActorKind != actor.Kind || receipt.ActorID != actor.ID || receipt.Operation != operation || !bytes.Equal(stored, fingerprint[:]) {
		return workReceipt{}, false, ErrWorkRequestConflict
	}
	receipt.CommittedAt = timeFromUnixNano(committed)
	return receipt, true, nil
}

func persistWorkReceipt(ctx context.Context, tx *sql.Tx, requestID string, actor Principal, operation string, fingerprint [sha256.Size]byte, resultKind, resultID string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO work_requests(request_id, actor_kind, actor_id, operation, payload_fingerprint, result_kind, result_id, committed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
	`, requestID, actor.Kind, actor.ID, operation, fingerprint[:], resultKind, resultID, unixNano(now))
	if err != nil {
		return fmt.Errorf("persist work request receipt: %w", err)
	}
	return nil
}

func validateWorkText(value string, max int) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
		return ErrWorkInvalid
	}
	return nil
}

func validateWorkCreateParams(params WorkCreateParams) error {
	if _, err := uuid.Parse(params.RequestID); err != nil || params.Actor.ID == "" || params.Actor.OrganizationID == "" {
		return ErrWorkInvalid
	}
	if err := validateWorkText(params.Goal, 20000); err != nil || len(params.AcceptanceCriteria) == 0 {
		return ErrWorkInvalid
	}
	for _, value := range append(append([]string{}, params.Constraints...), params.AcceptanceCriteria...) {
		if err := validateWorkText(value, 20000); err != nil {
			return err
		}
	}
	if params.SourceMessageID == "" || params.SourceSpaceID == "" || params.SourceTarget.ID == "" || params.SourceTargetSequence == 0 {
		return ErrWorkInvalid
	}
	if params.SourceTarget.Kind != MessageTargetSpace && params.SourceTarget.Kind != MessageTargetThread {
		return ErrWorkInvalid
	}
	return nil
}

func workSelect() string {
	return `SELECT id, organization_id, root_work_id, COALESCE(parent_work_id, ''),
        source_message_id, source_space_id, source_target_kind, source_target_id,
        source_target_sequence, team_space_id, goal, state, blocking_reason, result,
        creator_kind, creator_id, created_at, updated_at, state_changed_at,
        completed_at, failed_at, cancelled_at FROM works`
}

func scanWork(row scanner) (Work, error) {
	var work Work
	var sourceSequence, created, updated, stateChanged int64
	var completed, failed, cancelled sql.NullInt64
	if err := row.Scan(&work.ID, &work.OrganizationID, &work.RootWorkID, &work.ParentWorkID,
		&work.SourceMessageID, &work.SourceSpaceID, &work.SourceTarget.Kind, &work.SourceTarget.ID,
		&sourceSequence, &work.TeamSpaceID, &work.Goal, &work.State, &work.BlockingReason, &work.Result,
		&work.Creator.Kind, &work.Creator.ID, &created, &updated, &stateChanged,
		&completed, &failed, &cancelled); err != nil {
		return Work{}, err
	}
	work.Creator.OrganizationID = work.OrganizationID
	work.SourceTargetSequence = uint64(sourceSequence)
	work.CreatedAt, work.UpdatedAt, work.StateChangedAt = timeFromUnixNano(created), timeFromUnixNano(updated), timeFromUnixNano(stateChanged)
	if completed.Valid {
		value := timeFromUnixNano(completed.Int64)
		work.CompletedAt = &value
	}
	if failed.Valid {
		value := timeFromUnixNano(failed.Int64)
		work.FailedAt = &value
	}
	if cancelled.Valid {
		value := timeFromUnixNano(cancelled.Int64)
		work.CancelledAt = &value
	}
	return work, nil
}

type workPartsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const (
	workRowsConstraints = "constraints"
	workRowsCriteria    = "acceptance_criteria"
	workRowsCompletion  = "completion_criteria"
)

type workRowsErrorContextKey struct{}

type workRowsErrorFunc func(string, *sql.Rows) error

func workRowsErr(ctx context.Context, source string, rows *sql.Rows) error {
	if read, ok := ctx.Value(workRowsErrorContextKey{}).(workRowsErrorFunc); ok {
		return read(source, rows)
	}
	return rows.Err()
}

func loadWorkParts(ctx context.Context, queryer workPartsQueryer, work *Work) error {
	rows, err := queryer.QueryContext(ctx, `SELECT id, ordinal, body, created_at FROM work_constraints WHERE work_id = ? AND organization_id = ? ORDER BY ordinal`, work.ID, work.OrganizationID)
	if err != nil {
		return fmt.Errorf("list work constraints: %w", err)
	}
	for rows.Next() {
		var value WorkText
		var stamp int64
		if err := rows.Scan(&value.ID, &value.Ordinal, &value.Body, &stamp); err != nil {
			rows.Close()
			return err
		}
		value.CreatedAt = timeFromUnixNano(stamp)
		work.Constraints = append(work.Constraints, value)
	}
	if err := workRowsErr(ctx, workRowsConstraints, rows); err != nil {
		rows.Close()
		return fmt.Errorf("iterate work constraints: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = queryer.QueryContext(ctx, `SELECT id, ordinal, body, created_at FROM work_acceptance_criteria WHERE work_id = ? AND organization_id = ? ORDER BY ordinal`, work.ID, work.OrganizationID)
	if err != nil {
		return fmt.Errorf("list work criteria: %w", err)
	}
	for rows.Next() {
		var value WorkCriterion
		var stamp int64
		if err := rows.Scan(&value.ID, &value.Ordinal, &value.Body, &stamp); err != nil {
			rows.Close()
			return err
		}
		value.CreatedAt = timeFromUnixNano(stamp)
		work.AcceptanceCriteria = append(work.AcceptanceCriteria, value)
	}
	if err := workRowsErr(ctx, workRowsCriteria, rows); err != nil {
		rows.Close()
		return fmt.Errorf("iterate work criteria: %w", err)
	}
	return rows.Close()
}

func workName(goal string) string {
	name := strings.TrimSpace(goal)
	if utf8.RuneCountInString(name) > 94 {
		name = string([]rune(name)[:94])
	}
	return "Work: " + name
}

func requireWorkGrant(ctx context.Context, tx *sql.Tx, actor Principal, capability, workID, sourceSpaceID, action, requestID string, now time.Time) error {
	reason, err := requireGrant(ctx, tx, actor, capability, Scope{Kind: "work", ID: workID}, now, "")
	if err != nil {
		return err
	}
	if reason == "" {
		return nil
	}
	return denyCollaborationWithContext(ctx, tx, actor, action, "work", workID, "space", sourceSpaceID, requestID, reason, now, ErrPermissionDenied)
}

func workReplay(ctx context.Context, tx *sql.Tx, receipt workReceipt) (Work, error) {
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, receipt.ResultID))
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrWorkNotFound
	}
	if err != nil {
		return Work{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return Work{}, err
	}
	if err := tx.Commit(); err != nil {
		return Work{}, fmt.Errorf("commit work replay: %w", err)
	}
	return work, nil
}

func (s *Store) CreateWork(ctx context.Context, params WorkCreateParams) (Work, error) {
	if err := validateWorkCreateParams(params); err != nil {
		return Work{}, err
	}
	fingerprint, err := workFingerprint(params)
	if err != nil {
		return Work{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, fmt.Errorf("begin work create: %w", err)
	}
	defer tx.Rollback()
	if receipt, found, err := readWorkReceipt(ctx, tx, params.RequestID, workRequestCreate, params.Actor, fingerprint); err != nil || found {
		if err != nil {
			return Work{}, err
		}
		return workReplay(ctx, tx, receipt)
	}
	if err := requireCollaborationGrantWithContext(ctx, tx, params.Actor, CapabilityWorkCreate, Scope{Kind: "organization", ID: params.Actor.OrganizationID}, AuditWorkCreate, "work", "", "space", params.SourceSpaceID, params.RequestID, params.Now); err != nil {
		return Work{}, err
	}
	if err := validatePrincipalInOrganization(ctx, tx, params.Actor, params.Actor.OrganizationID); err != nil {
		return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", "", "space", params.SourceSpaceID, params.RequestID, "actor_invalid", params.Now, ErrPermissionDenied)
	}
	var sourceOrg string
	if err := tx.QueryRowContext(ctx, `SELECT s.organization_id FROM messages m JOIN spaces s ON s.id = m.space_id WHERE m.id = ? AND m.space_id = ? AND m.target_kind = ? AND m.target_id = ? AND m.target_sequence = ?`, params.SourceMessageID, params.SourceSpaceID, params.SourceTarget.Kind, params.SourceTarget.ID, params.SourceTargetSequence).Scan(&sourceOrg); errors.Is(err, sql.ErrNoRows) {
		return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", "", "space", params.SourceSpaceID, params.RequestID, "source_invalid", params.Now, ErrWorkInvalid)
	} else if err != nil {
		return Work{}, err
	} else if sourceOrg != params.Actor.OrganizationID {
		return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", "", "space", params.SourceSpaceID, params.RequestID, "source_invalid", params.Now, ErrWorkInvalid)
	}
	if reason, err := requireGrant(ctx, tx, params.Actor, CapabilitySpaceRead, Scope{Kind: "space", ID: params.SourceSpaceID}, params.Now, ""); err != nil {
		return Work{}, err
	} else if reason != "" {
		return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", "", "space", params.SourceSpaceID, params.RequestID, reason, params.Now, ErrPermissionDenied)
	}
	if err := requireActiveMembership(ctx, tx, params.SourceSpaceID, params.Actor); err != nil {
		return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", "", "space", params.SourceSpaceID, params.RequestID, "source_membership_missing", params.Now, ErrPermissionDenied)
	}
	rootID, parentSpace := "", params.SourceSpaceID
	if params.ParentWorkID != "" {
		parent, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ? AND organization_id = ?`, params.ParentWorkID, params.Actor.OrganizationID))
		if errors.Is(err, sql.ErrNoRows) {
			return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", params.ParentWorkID, "space", params.SourceSpaceID, params.RequestID, "parent_invalid", params.Now, ErrWorkNotFound)
		} else if err != nil {
			return Work{}, err
		}
		if err := requireWorkGrant(ctx, tx, params.Actor, CapabilityWorkManage, parent.ID, parent.SourceSpaceID, AuditWorkCreate, params.RequestID, params.Now); err != nil {
			return Work{}, err
		}
		rootID, parentSpace = parent.RootWorkID, parent.SourceSpaceID
	}
	if parentSpace != params.SourceSpaceID {
		return Work{}, denyCollaborationWithContext(ctx, tx, params.Actor, AuditWorkCreate, "work", params.ParentWorkID, "space", params.SourceSpaceID, params.RequestID, "parent_source_mismatch", params.Now, ErrWorkInvalid)
	}
	workID, spaceID := uuid.NewString(), uuid.NewString()
	stamp := unixNano(params.Now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO spaces(id, organization_id, kind, name, dm_key, created_at, updated_at) VALUES(?, ?, 'group', ?, NULL, ?, ?)`, spaceID, params.Actor.OrganizationID, workName(params.Goal), stamp, stamp); err != nil {
		return Work{}, fmt.Errorf("persist work team space: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO space_memberships(space_id, principal_kind, principal_id, joined_at) VALUES(?, ?, ?, ?)`, spaceID, params.Actor.Kind, params.Actor.ID, stamp); err != nil {
		return Work{}, fmt.Errorf("persist work team membership: %w", err)
	}
	if rootID == "" {
		rootID = workID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO works(id, organization_id, root_work_id, parent_work_id, source_message_id, source_space_id, source_target_kind, source_target_id, source_target_sequence, team_space_id, goal, state, blocking_reason, result, creator_kind, creator_id, created_at, updated_at, state_changed_at) VALUES(?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, 'open', '', '', ?, ?, ?, ?, ?)`, workID, params.Actor.OrganizationID, rootID, params.ParentWorkID, params.SourceMessageID, params.SourceSpaceID, params.SourceTarget.Kind, params.SourceTarget.ID, params.SourceTargetSequence, spaceID, params.Goal, params.Actor.Kind, params.Actor.ID, stamp, stamp, stamp); err != nil {
		return Work{}, fmt.Errorf("persist work: %w", err)
	}
	for ordinal, body := range params.Constraints {
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_constraints(id, work_id, organization_id, ordinal, body, created_at) VALUES(?, ?, ?, ?, ?, ?)`, uuid.NewString(), workID, params.Actor.OrganizationID, ordinal, body, stamp); err != nil {
			return Work{}, err
		}
	}
	for ordinal, body := range params.AcceptanceCriteria {
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_acceptance_criteria(id, work_id, organization_id, ordinal, body, created_at) VALUES(?, ?, ?, ?, ?, ?)`, uuid.NewString(), workID, params.Actor.OrganizationID, ordinal, body, stamp); err != nil {
			return Work{}, err
		}
	}
	if err := insertWorkEvent(ctx, tx, workID, params.Actor.OrganizationID, "created", params.Actor, "", "", "", "", "", params.Now); err != nil {
		return Work{}, err
	}
	if err := persistWorkReceipt(ctx, tx, params.RequestID, params.Actor, workRequestCreate, fingerprint, "work", workID, params.Now); err != nil {
		return Work{}, err
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{OrganizationID: params.Actor.OrganizationID, Actor: params.Actor, Action: AuditWorkCreate, TargetKind: "work", TargetID: workID, ContextKind: "space", ContextID: params.SourceSpaceID, RequestID: params.RequestID, Outcome: "committed", Now: params.Now}); err != nil {
		return Work{}, err
	}
	work, err := scanWork(tx.QueryRowContext(ctx, workSelect()+` WHERE id = ?`, workID))
	if err != nil {
		return Work{}, err
	}
	if err := loadWorkParts(ctx, tx, &work); err != nil {
		return Work{}, err
	}
	if _, err := enqueueKnowledgeDirtySource(ctx, tx, KnowledgeSource{Kind: KnowledgeSourceWork, ID: work.ID}, knowledgeWorkRevision(work), params.Now); err != nil {
		return Work{}, err
	}
	if err := tx.Commit(); err != nil {
		return Work{}, fmt.Errorf("commit work create: %w", err)
	}
	return work, nil
}

func insertWorkEvent(ctx context.Context, tx *sql.Tx, workID, organizationID, kind string, actor Principal, fromState, toState, referenceKind, referenceID, reason string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO work_events(id, work_id, organization_id, event_kind, actor_kind, actor_id, from_state, to_state, reference_kind, reference_id, reason, occurred_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uuid.NewString(), workID, organizationID, kind, actor.Kind, actor.ID, fromState, toState, referenceKind, referenceID, reason, unixNano(now))
	if err != nil {
		return fmt.Errorf("append work event: %w", err)
	}
	return nil
}

type workAssignmentRowsErrorContextKey struct{}

type workAssignmentRowsErrorFunc func(*sql.Rows) error

func workAssignmentRowsErr(ctx context.Context, rows *sql.Rows) error {
	if read, ok := ctx.Value(workAssignmentRowsErrorContextKey{}).(workAssignmentRowsErrorFunc); ok {
		return read(rows)
	}
	return rows.Err()
}

func endWorkAssignments(ctx context.Context, tx *sql.Tx, workID, agentID, role string, actor Principal, reason string, now time.Time) error {
	filter := ""
	args := []any{workID}
	if agentID != "" || role != "" {
		filter = " AND (agent_id = ? OR role = ?)"
		args = append(args, agentID, role)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM work_assignments WHERE work_id = ? AND ended_at IS NULL`+filter, args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := workAssignmentRowsErr(ctx, rows); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	args = append([]any{unixNano(now), reason}, args...)
	if _, err := tx.ExecContext(ctx, `UPDATE work_assignments SET ended_at = ?, end_reason = ? WHERE work_id = ? AND ended_at IS NULL`+filter, args...); err != nil {
		return err
	}
	for _, id := range ids {
		if err := insertWorkEvent(ctx, tx, workID, actor.OrganizationID, "assignment.ended", actor, "", "", "assignment", id, reason, now); err != nil {
			return err
		}
	}
	return nil
}
