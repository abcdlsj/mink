package delivery

import (
	v1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/internal/agentmessage"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func completeRunResponse(result store.CompleteRunResult) (*v1.CompleteRunResponse, error) {
	if err := validateCompleteResult(result); err != nil {
		return nil, err
	}
	run, err := runMessage(result.Run)
	if err != nil {
		return nil, err
	}
	response := &v1.CompleteRunResponse{Run: run, CommittedAt: timestamppb.New(result.CommittedAt)}
	switch result.Kind {
	case store.InboxResultMessage:
		message, err := agentmessage.Message(*result.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &v1.CompleteRunResponse_Message{Message: message}
	case store.InboxResultHeldDraft:
		draft, err := agentmessage.HeldDraft(*result.HeldDraft)
		if err != nil {
			return nil, err
		}
		response.Result = &v1.CompleteRunResponse_HeldDraft{HeldDraft: draft}
	}
	return response, nil
}

func deliveryMessage(value store.Delivery) (*v1.Delivery, error) {
	state := v1.DeliveryState_DELIVERY_STATE_UNSPECIFIED
	switch value.State {
	case store.DeliveryStateAvailable:
		if value.AcceptedAt != nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = v1.DeliveryState_DELIVERY_STATE_AVAILABLE
	case store.DeliveryStateAccepted:
		if value.AcceptedAt == nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = v1.DeliveryState_DELIVERY_STATE_ACCEPTED
	case store.DeliveryStateCompleted:
		if value.AcceptedAt == nil || value.CompletedAt == nil {
			return nil, internalError()
		}
		state = v1.DeliveryState_DELIVERY_STATE_COMPLETED
	default:
		return nil, internalError()
	}
	target, err := agentmessage.Target(value.Target)
	if err != nil {
		return nil, err
	}
	message := &v1.Delivery{
		Sequence: value.Sequence, Id: value.ID, AgentId: value.AgentID,
		InboxItemId: value.InboxItemID, TriggerMessageId: value.TriggerMessageID,
		SpaceId: value.SpaceID, Target: target,
		TriggerTargetSequence: value.TriggerTargetSequence, State: state,
		CreatedAt: timestamppb.New(value.CreatedAt),
	}
	if value.AcceptedAt != nil {
		message.AcceptedAt = timestamppb.New(*value.AcceptedAt)
	}
	if value.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.CompletedAt)
	}
	return message, nil
}

func runMessage(value store.Run) (*v1.Run, error) {
	state := v1.RunState_RUN_STATE_UNSPECIFIED
	switch value.State {
	case store.RunStateAccepted:
		if value.Outcome != "" || value.ResultKind != "" || value.ResultID != "" || value.StartedAt != nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = v1.RunState_RUN_STATE_ACCEPTED
	case store.RunStateRunning:
		if value.Outcome != "" || value.ResultKind != "" || value.ResultID != "" || value.StartedAt == nil || value.CompletedAt != nil {
			return nil, internalError()
		}
		state = v1.RunState_RUN_STATE_RUNNING
	case store.RunStateCompleted:
		if value.Outcome == "" || value.ResultKind == "" || value.ResultID == "" || value.StartedAt == nil || value.CompletedAt == nil {
			return nil, internalError()
		}
		state = v1.RunState_RUN_STATE_COMPLETED
	default:
		return nil, internalError()
	}
	outcome := v1.RunOutcome_RUN_OUTCOME_UNSPECIFIED
	switch value.Outcome {
	case "":
	case store.RunOutcomeSucceeded:
		outcome = v1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	case store.RunOutcomeFailed:
		outcome = v1.RunOutcome_RUN_OUTCOME_FAILED
	default:
		return nil, internalError()
	}
	message := &v1.Run{
		Id: value.ID, DeliveryId: value.DeliveryID, AgentId: value.AgentID,
		BasisTargetSequence: value.BasisTargetSequence, State: state, Outcome: outcome,
		AcceptedAt: timestamppb.New(value.AcceptedAt),
	}
	switch value.ResultKind {
	case "":
		if value.ResultID != "" {
			return nil, internalError()
		}
	case store.InboxResultMessage:
		message.ResultRef = &v1.Run_ResultMessageId{ResultMessageId: value.ResultID}
	case store.InboxResultHeldDraft:
		message.ResultRef = &v1.Run_ResultHeldDraftId{ResultHeldDraftId: value.ResultID}
	default:
		return nil, internalError()
	}
	if value.StartedAt != nil {
		message.StartedAt = timestamppb.New(*value.StartedAt)
	}
	if value.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.CompletedAt)
	}
	return message, nil
}

func runLaunchMessage(value store.RunLaunch) (*v1.RunLaunch, error) {
	reason := v1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_UNSPECIFIED
	switch value.CloseReason {
	case "":
		if value.ClosedAt != nil {
			return nil, internalError()
		}
	case store.RunLaunchCloseReplaced:
		if value.ClosedAt == nil || value.ClosedAt.Before(value.ExpiresAt) {
			return nil, internalError()
		}
		reason = v1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_REPLACED
	case store.RunLaunchCloseCompleted:
		if value.ClosedAt == nil || value.ClosedAt.Before(value.ClaimedAt) {
			return nil, internalError()
		}
		reason = v1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_COMPLETED
	default:
		return nil, internalError()
	}
	if value.Fence == 0 || value.HolderComputerID == "" || value.HolderPlacementGeneration == 0 || !value.ExpiresAt.After(value.ClaimedAt) {
		return nil, internalError()
	}
	message := &v1.RunLaunch{
		Id: value.ID, RunId: value.RunID, AgentId: value.AgentID,
		HolderComputerId:          value.HolderComputerID,
		HolderPlacementGeneration: value.HolderPlacementGeneration, Fence: value.Fence,
		ClaimedAt: timestamppb.New(value.ClaimedAt), ExpiresAt: timestamppb.New(value.ExpiresAt),
		CloseReason: reason,
	}
	if value.ClosedAt != nil {
		message.ClosedAt = timestamppb.New(*value.ClosedAt)
	}
	return message, nil
}
