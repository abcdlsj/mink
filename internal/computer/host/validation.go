package host

import (
	"context"
	"crypto/sha256"
	"errors"

	"connectrpc.com/connect"
	deliveryv1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
)

func (d *Daemon) invalidateRuntimeOnUnauthenticated(ctx context.Context, session computerstate.RuntimeSession, err error) {
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		_ = d.config.State.DeleteRuntimeSession(ctx, session.AgentID, session.ComputerID, session.PlacementGeneration)
	}
}

func validateAvailableDelivery(delivery *deliveryv1.Delivery, agentID string) error {
	if delivery == nil || !validUUID(delivery.GetId()) || delivery.GetAgentId() != agentID ||
		delivery.GetTarget() == nil || delivery.GetState() != deliveryv1.DeliveryState_DELIVERY_STATE_AVAILABLE {
		return errors.New("available delivery facts are invalid")
	}
	return nil
}

func validateActiveDeliveryResponse(agentID string, delivery *deliveryv1.Delivery, run *deliveryv1.Run, launch *deliveryv1.RunLaunch) error {
	if run == nil {
		if delivery != nil || launch != nil {
			return errors.New("active delivery response has facts without a run")
		}
		return nil
	}
	if delivery == nil || !validUUID(delivery.GetId()) || delivery.GetAgentId() != agentID || delivery.GetTarget() == nil ||
		delivery.GetState() != deliveryv1.DeliveryState_DELIVERY_STATE_ACCEPTED {
		return errors.New("active delivery facts are invalid")
	}
	if err := validateRun(run, delivery.GetId(), agentID); err != nil {
		return err
	}
	switch run.GetState() {
	case deliveryv1.RunState_RUN_STATE_ACCEPTED:
		if launch != nil {
			return errors.New("accepted run must not have a launch")
		}
		return nil
	case deliveryv1.RunState_RUN_STATE_RUNNING:
		return validateLaunchFacts(launch, run.GetId(), agentID)
	default:
		return errors.New("active run state is invalid")
	}
}

func validateRun(run *deliveryv1.Run, deliveryID, agentID string) error {
	if run == nil || !validUUID(run.GetId()) || run.GetDeliveryId() != deliveryID || run.GetAgentId() != agentID ||
		(run.GetState() != deliveryv1.RunState_RUN_STATE_ACCEPTED && run.GetState() != deliveryv1.RunState_RUN_STATE_RUNNING) {
		return errors.New("run facts are invalid")
	}
	return nil
}

func validateLaunch(launch *deliveryv1.RunLaunch, runID, agentID, computerID string, generation uint64) error {
	if err := validateLaunchFacts(launch, runID, agentID); err != nil {
		return err
	}
	if launch.GetHolderComputerId() != computerID || launch.GetHolderPlacementGeneration() != generation {
		return errors.New("launch holder binding is invalid")
	}
	return nil
}

func validateLaunchFacts(launch *deliveryv1.RunLaunch, runID, agentID string) error {
	if launch == nil || !validUUID(launch.GetId()) || launch.GetRunId() != runID || launch.GetAgentId() != agentID ||
		!validUUID(launch.GetHolderComputerId()) || launch.GetHolderPlacementGeneration() == 0 || launch.GetFence() == 0 ||
		launch.GetClaimedAt() == nil || launch.GetClaimedAt().CheckValid() != nil ||
		launch.GetExpiresAt() == nil || launch.GetExpiresAt().CheckValid() != nil ||
		!launch.GetExpiresAt().AsTime().After(launch.GetClaimedAt().AsTime()) || launch.GetClosedAt() != nil ||
		launch.GetCloseReason() != deliveryv1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_UNSPECIFIED {
		return errors.New("launch facts are invalid")
	}
	return nil
}

func validateCompleteResponse(response *deliveryv1.CompleteRunResponse, event computerstate.OutboxEvent) error {
	if response == nil || response.GetCommittedAt() == nil || response.GetCommittedAt().CheckValid() != nil ||
		(response.GetMessage() == nil && response.GetHeldDraft() == nil) {
		return errors.New("completion response facts are invalid")
	}
	run := response.GetRun()
	if run == nil || run.GetId() != event.RunID || run.GetAgentId() != event.AgentID ||
		run.GetState() != deliveryv1.RunState_RUN_STATE_COMPLETED || run.GetOutcome() != outcomeValue(event.Outcome) ||
		run.GetCompletedAt() == nil || run.GetCompletedAt().CheckValid() != nil {
		return errors.New("completed run facts do not match outbox event")
	}
	if message := response.GetMessage(); message != nil {
		if !validUUID(message.GetId()) || run.GetResultMessageId() != message.GetId() || run.GetResultHeldDraftId() != "" {
			return errors.New("completion message result is invalid")
		}
		return nil
	}
	draft := response.GetHeldDraft()
	if !validUUID(draft.GetId()) || run.GetResultHeldDraftId() != draft.GetId() || run.GetResultMessageId() != "" {
		return errors.New("completion held draft result is invalid")
	}
	return nil
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func runtimeRequest[Request any](token string, message *Request) *connect.Request[Request] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+token)
	return request
}

func mutationHash(values ...string) [sha256.Size]byte {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func validateCompletion(completion Completion) error {
	if completion.Outcome != deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED && completion.Outcome != deliveryv1.RunOutcome_RUN_OUTCOME_FAILED {
		return errors.New("completion outcome is invalid")
	}
	if err := computerstate.ValidateCompletionPayload(completion.Body, completion.MentionedAgentIDs); err != nil {
		return err
	}
	return nil
}

func outcomeName(outcome deliveryv1.RunOutcome) computerstate.CompletionOutcome {
	if outcome == deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED {
		return computerstate.CompletionSucceeded
	}
	return computerstate.CompletionFailed
}

func outcomeValue(outcome computerstate.CompletionOutcome) deliveryv1.RunOutcome {
	if outcome == computerstate.CompletionSucceeded {
		return deliveryv1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	}
	return deliveryv1.RunOutcome_RUN_OUTCOME_FAILED
}
