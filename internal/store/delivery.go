package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	execution "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/google/uuid"
)

const (
	DeliveryStateAvailable = "available"
	DeliveryStateAccepted  = "accepted"
	DeliveryStateCompleted = "completed"

	RunStateAccepted  = "accepted"
	RunStateRunning   = "running"
	RunStateCompleted = "completed"

	RunOutcomeSucceeded = "succeeded"
	RunOutcomeFailed    = "failed"

	RunLaunchCloseReplaced  = "replaced"
	RunLaunchCloseCompleted = "completed"

	operationAcceptDelivery = "delivery.accept"
	operationClaimRun       = "run.claim"
	operationRenewRun       = "run.renew"
	operationCompleteRun    = "run.complete"

	maxDeliveryListLimit = 200
	runLeaseTTL          = 60 * time.Second
)

type Delivery struct {
	Sequence              uint64
	ID                    string
	AgentID               string
	InboxItemID           string
	TriggerMessageID      string
	SpaceID               string
	Target                MessageTarget
	TriggerTargetSequence uint64
	State                 string
	CreatedAt             time.Time
	AcceptedAt            *time.Time
	CompletedAt           *time.Time
}

type Run struct {
	ID                  string
	DeliveryID          string
	AgentID             string
	BasisTargetSequence uint64
	State               string
	Outcome             string
	ResultKind          string
	ResultID            string
	AcceptedAt          time.Time
	StartedAt           *time.Time
	CompletedAt         *time.Time
}

type RunLaunch struct {
	ID                        string
	RunID                     string
	AgentID                   string
	HolderComputerID          string
	HolderPlacementGeneration uint64
	Fence                     uint64
	ClaimedAt                 time.Time
	ExpiresAt                 time.Time
	ClosedAt                  *time.Time
	CloseReason               string
}

type ListDeliveriesParams struct {
	Authentication AgentRuntimeAuthentication
	AfterSequence  uint64
	Limit          uint32
	Now            time.Time
}

type ListDeliveriesResult struct {
	Deliveries     []Delivery
	NextSequence   uint64
	ActiveDelivery *Delivery
	ActiveRun      *Run
	ActiveLaunch   *RunLaunch
}

type AcceptDeliveryParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	DeliveryID     string
	Now            time.Time
}

type GetRunParams struct {
	Authentication AgentRuntimeAuthentication
	RunID          string
	Now            time.Time
}

type ClaimRunParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	RunID          string
	Now            time.Time
}

type RenewRunParams struct {
	RequestID      string
	Authentication AgentRuntimeAuthentication
	RunID          string
	LaunchID       string
	Fence          uint64
	Now            time.Time
}

type CompleteRunParams struct {
	RequestID         string
	OutboxEventID     string
	Authentication    AgentRuntimeAuthentication
	RunID             string
	LaunchID          string
	Fence             uint64
	Outcome           string
	Body              string
	MentionedAgentIDs []string
	Now               time.Time
}

type CompleteRunResult struct {
	Run         Run
	Kind        string
	Message     *Message
	HeldDraft   *HeldDraft
	CommittedAt time.Time
}

type runAcceptRequestReceipt struct {
	Run Run `json:"run"`
}

type runLaunchRequestReceipt struct {
	Launch RunLaunch `json:"launch"`
}

type runCompleteRequestReceipt struct {
	OutboxEventID string                  `json:"outbox_event_id"`
	Run           Run                     `json:"run"`
	LaunchID      string                  `json:"launch_id"`
	Fence         uint64                  `json:"fence"`
	Result        inboxSendRequestReceipt `json:"result"`
}

type runCompletionReceipt struct {
	OutboxEventID string
	RequestID     string
	Fingerprint   [sha256.Size]byte
	RunID         string
	LaunchID      string
	Fence         uint64
	ResultKind    string
	ResultID      string
	CommittedAt   time.Time
}

func (s *Store) ListDeliveries(ctx context.Context, params ListDeliveriesParams) (ListDeliveriesResult, error) {
	if params.Limit == 0 || params.Limit > maxDeliveryListLimit {
		return ListDeliveriesResult{}, ErrInvalidDeliveryLimit
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return ListDeliveriesResult{}, err
	}
	defer tx.Rollback()
	result := ListDeliveriesResult{
		Deliveries:   make([]Delivery, 0, params.Limit),
		NextSequence: params.AfterSequence,
	}
	activeRun, found, err := activeRunByAgent(ctx, tx, authentication.Principal.ID)
	if err != nil {
		return ListDeliveriesResult{}, err
	}
	if found {
		delivery, item, _, err := requireOwnedDelivery(ctx, tx, authentication.Principal.ID, activeRun.DeliveryID)
		if err != nil {
			return ListDeliveriesResult{}, err
		}
		if err := requireRunItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
			return ListDeliveriesResult{}, err
		}
		launch, launchFound, err := currentRunLaunch(ctx, tx, activeRun.ID)
		if err != nil {
			return ListDeliveriesResult{}, err
		}
		if item.State != InboxStateClaimed || (!launchFound && activeRun.State == RunStateRunning) {
			return ListDeliveriesResult{}, ErrRunIntegrity
		}
		if launchFound {
			result.ActiveLaunch = &launch
		}
		result.ActiveDelivery = &delivery
		result.ActiveRun = &activeRun
		if err := execution.ValidateActiveFacts(executionActiveFacts(result)); err != nil {
			return ListDeliveriesResult{}, err
		}
	}
	for len(result.Deliveries) < int(params.Limit) {
		rows, err := tx.QueryContext(ctx, deliverySelect+`
			WHERE deliveries.agent_id = ? AND deliveries.state = 'available' AND deliveries.sequence > ?
			ORDER BY deliveries.sequence
			LIMIT ?
		`, authentication.Principal.ID, result.NextSequence, params.Limit)
		if err != nil {
			return ListDeliveriesResult{}, fmt.Errorf("list deliveries: %w", err)
		}
		candidates, err := scanDeliveries(rows)
		rows.Close()
		if err != nil {
			return ListDeliveriesResult{}, err
		}
		if len(candidates) == 0 {
			break
		}
		for _, delivery := range candidates {
			result.NextSequence = delivery.Sequence
			_, item, _, err := requireOwnedDelivery(ctx, tx, authentication.Principal.ID, delivery.ID)
			if err != nil {
				return ListDeliveriesResult{}, err
			}
			if item.State != InboxStateUnread && item.State != InboxStateClaimed {
				continue
			}
			allowed, err := inboxItemReadable(ctx, tx, authentication.Principal, item, params.Now)
			if err != nil {
				return ListDeliveriesResult{}, err
			}
			if !allowed {
				if err := closeInboxItemAccessLost(ctx, tx, item.ID, params.Now); err != nil {
					return ListDeliveriesResult{}, err
				}
				continue
			}
			result.Deliveries = append(result.Deliveries, delivery)
			if len(result.Deliveries) == int(params.Limit) {
				break
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return ListDeliveriesResult{}, fmt.Errorf("commit delivery list: %w", err)
	}
	return result, nil
}

func (s *Store) AcceptDelivery(ctx context.Context, params AcceptDeliveryParams) (Run, error) {
	fingerprint, err := inboxFingerprint(struct {
		DeliveryID string `json:"delivery_id"`
	}{params.DeliveryID})
	if err != nil {
		return Run{}, err
	}
	tx, authentication, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	delivery, item, _, err := requireOwnedDelivery(ctx, tx, authentication.Principal.ID, params.DeliveryID)
	if err != nil {
		return Run{}, err
	}
	if err := requireRunItemAccess(ctx, tx, authentication.Principal, item, params.Now); err != nil {
		return Run{}, err
	}
	if replay, found, err := replayRunAcceptRequest(ctx, tx, params.RequestID, authentication.Principal.ID, fingerprint, delivery); err != nil {
		return Run{}, err
	} else if found {
		return commitInboxReplay(tx, replay)
	}
	basis, err := deliveryCursor(ctx, tx, delivery)
	if err != nil {
		return Run{}, err
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE agent_id = ? AND state IN ('accepted', 'running'))`, delivery.AgentID).Scan(&active); err != nil {
		return Run{}, fmt.Errorf("check active run: %w", err)
	}
	if err := execution.CanAccept(execution.DeliveryState(delivery.State), execution.InboxState(item.State), true, active); err != nil {
		return Run{}, err
	}
	if item.State == InboxStateUnread {
		if _, err := tx.ExecContext(ctx, `UPDATE agent_inbox_items SET state = 'claimed', claimed_at = ? WHERE id = ? AND state = 'unread'`, unixNano(params.Now), item.ID); err != nil {
			return Run{}, fmt.Errorf("claim delivery inbox item: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE deliveries SET state = 'accepted', accepted_at = ? WHERE id = ? AND state = 'available'`, unixNano(params.Now), delivery.ID); err != nil {
		return Run{}, fmt.Errorf("accept delivery: %w", err)
	}
	runID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runs(id, delivery_id, agent_id, basis_target_sequence, state, accepted_at)
		VALUES(?, ?, ?, ?, 'accepted', ?)
	`, runID, delivery.ID, delivery.AgentID, basis, unixNano(params.Now)); err != nil {
		if isUniqueConstraint(err, "runs.agent_id") {
			return Run{}, ErrRunAlreadyActive
		}
		return Run{}, fmt.Errorf("persist accepted run: %w", err)
	}
	run, err := runByID(ctx, tx, runID)
	if err != nil {
		return Run{}, fmt.Errorf("read accepted run: %w", err)
	}
	if err := appendAuditEvent(ctx, tx, AppendAuditParams{
		OrganizationID: authentication.Principal.OrganizationID,
		Actor:          authentication.Principal,
		Action:         AuditRunAccept,
		TargetKind:     "run",
		TargetID:       run.ID,
		ContextKind:    delivery.Target.Kind,
		ContextID:      delivery.Target.ID,
		RequestID:      params.RequestID,
		Outcome:        "committed",
		Now:            params.Now,
	}); err != nil {
		return Run{}, err
	}
	if err := persistInboxRequest(ctx, tx, params.RequestID, delivery.AgentID, operationAcceptDelivery, fingerprint, runAcceptRequestReceipt{Run: run}, params.Now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit delivery acceptance: %w", err)
	}
	return run, nil
}

func (s *Store) GetRun(ctx context.Context, params GetRunParams) (Run, error) {
	tx, current, err := s.beginRunTransaction(ctx, params.Authentication, params.Now)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	run, err := requireOwnedRun(ctx, tx, current.Principal.ID, params.RunID)
	if err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, fmt.Errorf("commit run read: %w", err)
	}
	return run, nil
}
