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

func (s *Store) AssignWork(ctx context.Context, params AssignWorkParams) (WorkAssignment, error) {
	if params.Role != WorkAssignmentCoordinator && params.Role != WorkAssignmentContributor {
		return WorkAssignment{}, ErrWorkInvalid
	}
	fingerprint, err := workFingerprint(struct{ WorkID, Role, AgentID string }{params.WorkID, params.Role, params.AgentID})
	if err != nil {
		return WorkAssignment{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkAssignment{}, err
	}
	defer tx.Rollback()
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
	var generation uint64
	var computerID, placementState string
	if err := tx.QueryRowContext(ctx, `SELECT computer_id, generation, state FROM agent_placements WHERE agent_id = ?`, params.AgentID).Scan(&computerID, &generation, &placementState); errors.Is(err, sql.ErrNoRows) {
		return WorkAssignment{}, ErrWorkPlacementInvalid
	} else if err != nil {
		return WorkAssignment{}, err
	}
	if placementState != "active" {
		return WorkAssignment{}, ErrWorkPlacementInvalid
	}
	if exists, err := recordExists(ctx, tx, "agents", params.AgentID); err != nil || !exists {
		return WorkAssignment{}, ErrPrincipalNotFound
	}
	if err := endWorkAssignments(ctx, tx, params.WorkID, params.AgentID, params.Role, params.Actor, "reassigned", params.Now); err != nil {
		return WorkAssignment{}, err
	}
	assignmentID := uuid.NewString()
	stamp := unixNano(params.Now)
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_assignments(id, work_id, organization_id, role, agent_id, holder_computer_id, holder_placement_generation, assigned_by_kind, assigned_by_id, assigned_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, assignmentID, params.WorkID, params.Actor.OrganizationID, params.Role, params.AgentID, computerID, generation, params.Actor.Kind, params.Actor.ID, stamp); err != nil {
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
	err := tx.QueryRowContext(ctx, `SELECT id, work_id, organization_id, role, agent_id, holder_computer_id, holder_placement_generation, assigned_by_kind, assigned_by_id, assigned_at, ended_at, end_reason FROM work_assignments WHERE id = ?`, id).Scan(&value.ID, &value.WorkID, &value.OrganizationID, &value.Role, &value.AgentID, &value.HolderComputerID, &value.HolderPlacementGeneration, &value.AssignedBy.Kind, &value.AssignedBy.ID, &assigned, &ended, &value.EndReason)
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

func (s *Store) TransitionWork(ctx context.Context, params TransitionWorkParams) (Work, error) {
	if params.ToState == WorkStateWaitingApproval || params.ToState == "" {
		return Work{}, ErrWorkTransitionInvalid
	}
	fingerprint, err := workFingerprint(params)
	if err != nil {
		return Work{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Work{}, err
	}
	defer tx.Rollback()
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

func (s *Store) RequestWorkApproval(ctx context.Context, params RequestWorkApprovalParams) (WorkApproval, error) {
	if err := validateWorkText(params.Question, 20000); err != nil {
		return WorkApproval{}, err
	}
	fingerprint, err := workFingerprint(params)
	if err != nil {
		return WorkApproval{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkApproval{}, err
	}
	defer tx.Rollback()
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
	fingerprint, err := workFingerprint(params)
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
