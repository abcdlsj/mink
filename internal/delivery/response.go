package delivery

import (
	v1 "github.com/abcdlsj/sumi/gen/go/sumi/delivery/v1"
	"github.com/abcdlsj/sumi/internal/agentmessage"
	"github.com/abcdlsj/sumi/internal/execution"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func completeRunResponse(result CompleteRunResult) (*v1.CompleteRunResponse, error) {
	if err := validateCompleteResult(result); err != nil {
		return nil, err
	}
	run, err := runMessage(result.Run)
	if err != nil {
		return nil, err
	}
	response := &v1.CompleteRunResponse{Run: run, CommittedAt: timestamppb.New(result.CommittedAt)}
	switch result.Kind {
	case execution.ResultMessage:
		message, err := agentmessage.Message(storeMessageView(*result.Message))
		if err != nil {
			return nil, err
		}
		response.Result = &v1.CompleteRunResponse_Message{Message: message}
	case execution.ResultHeldDraft:
		draft, err := agentmessage.HeldDraft(storeHeldDraftView(*result.HeldDraft))
		if err != nil {
			return nil, err
		}
		response.Result = &v1.CompleteRunResponse_HeldDraft{HeldDraft: draft}
	}
	return response, nil
}

func deliveryMessage(value DeliveryResult) (*v1.Delivery, error) {
	if err := execution.ValidateDelivery(value.Fact); err != nil {
		return nil, internalError()
	}
	state := v1.DeliveryState_DELIVERY_STATE_UNSPECIFIED
	switch value.Fact.State {
	case execution.DeliveryAvailable:
		state = v1.DeliveryState_DELIVERY_STATE_AVAILABLE
	case execution.DeliveryAccepted:
		state = v1.DeliveryState_DELIVERY_STATE_ACCEPTED
	case execution.DeliveryCompleted:
		state = v1.DeliveryState_DELIVERY_STATE_COMPLETED
	default:
		return nil, internalError()
	}
	target, err := agentmessage.Target(store.MessageTarget{Kind: value.Fact.TargetKind, ID: value.Fact.TargetID})
	if err != nil {
		return nil, err
	}
	message := &v1.Delivery{
		Sequence: value.Sequence, Id: value.Fact.ID, AgentId: value.Fact.AgentID,
		InboxItemId: value.InboxItemID, TriggerMessageId: value.TriggerMessageID,
		SpaceId: value.SpaceID, Target: target,
		TriggerTargetSequence: value.Fact.TriggerTargetSequence, State: state,
		CreatedAt: timestamppb.New(value.CreatedAt),
	}
	if value.Fact.AcceptedAt != nil {
		message.AcceptedAt = timestamppb.New(*value.Fact.AcceptedAt)
	}
	if value.Fact.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.Fact.CompletedAt)
	}
	return message, nil
}

func runMessage(value execution.Run) (*v1.Run, error) {
	if err := execution.ValidateRun(value); err != nil {
		return nil, internalError()
	}
	state := v1.RunState_RUN_STATE_UNSPECIFIED
	switch value.State {
	case execution.RunAccepted:
		state = v1.RunState_RUN_STATE_ACCEPTED
	case execution.RunRunning:
		state = v1.RunState_RUN_STATE_RUNNING
	case execution.RunCompleted:
		state = v1.RunState_RUN_STATE_COMPLETED
	default:
		return nil, internalError()
	}
	outcome := v1.RunOutcome_RUN_OUTCOME_UNSPECIFIED
	switch value.Outcome {
	case "":
	case execution.OutcomeSucceeded:
		outcome = v1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	case execution.OutcomeFailed:
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
	case execution.ResultMessage:
		message.ResultRef = &v1.Run_ResultMessageId{ResultMessageId: value.ResultID}
	case execution.ResultHeldDraft:
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

func runLaunchMessage(value execution.Launch) (*v1.RunLaunch, error) {
	if err := execution.ValidateLaunch(value); err != nil {
		return nil, internalError()
	}
	reason := v1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_UNSPECIFIED
	switch value.CloseReason {
	case "":
	case execution.CloseReplaced:
		reason = v1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_REPLACED
	case execution.CloseCompleted:
		reason = v1.RunLaunchCloseReason_RUN_LAUNCH_CLOSE_REASON_COMPLETED
	default:
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
