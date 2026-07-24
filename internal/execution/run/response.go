package run

import (
	"errors"

	"connectrpc.com/connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/abcdlsj/sumi/internal/transport/messagecodec"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runMessage(value executionapp.Run) (*runv1.Run, error) {
	if err := executiondomain.ValidateRun(executionRun(value)); err != nil {
		return nil, internalError()
	}
	target, err := messagecodec.Target(value.Target)
	if err != nil {
		return nil, err
	}
	message := &runv1.Run{
		Sequence: value.Sequence, Id: value.ID, AgentId: value.AgentID, InboxItemId: value.InboxItemID,
		TriggerMessageId: value.TriggerMessageID, SpaceId: value.SpaceID, Target: target,
		TriggerTargetSequence: value.TriggerTargetSequence, InputBasisTargetSequence: value.InputBasisTargetSequence,
		Attempt: value.Attempt, LeaseHolderComputerId: value.LeaseHolderComputerID, Fence: value.Fence,
		PlacementDesiredRevision: value.PlacementDesiredRevision,
		Usage:                    &runv1.RunUsage{InputUnits: value.Usage.InputUnits, OutputUnits: value.Usage.OutputUnits},
		ErrorCode:                value.ErrorCode, CreatedAt: timestamppb.New(value.CreatedAt),
	}
	switch value.State {
	case executionappStateQueued:
		message.State = runv1.RunState_RUN_STATE_QUEUED
	case executionappStateRunning:
		message.State = runv1.RunState_RUN_STATE_RUNNING
	case executionappStateSucceeded:
		message.State = runv1.RunState_RUN_STATE_SUCCEEDED
	case executionappStateFailed:
		message.State = runv1.RunState_RUN_STATE_FAILED
	case executionappStateCancelled:
		message.State = runv1.RunState_RUN_STATE_CANCELLED
	default:
		return nil, internalError()
	}
	switch value.ResultKind {
	case "":
	case executionapp.ResultMessage:
		message.ResultRef = &runv1.Run_ResultMessageId{ResultMessageId: value.ResultID}
	case executionapp.ResultHeldDraft:
		message.ResultRef = &runv1.Run_ResultHeldDraftId{ResultHeldDraftId: value.ResultID}
	default:
		return nil, internalError()
	}
	if value.LeaseExpiresAt != nil {
		message.LeaseExpiresAt = timestamppb.New(*value.LeaseExpiresAt)
	}
	if value.StartedAt != nil {
		message.StartedAt = timestamppb.New(*value.StartedAt)
	}
	if value.CompletedAt != nil {
		message.CompletedAt = timestamppb.New(*value.CompletedAt)
	}
	if value.CancelledAt != nil {
		message.CancelledAt = timestamppb.New(*value.CancelledAt)
	}
	return message, nil
}

const (
	executionappStateQueued    = "queued"
	executionappStateRunning   = "running"
	executionappStateSucceeded = "succeeded"
	executionappStateFailed    = "failed"
	executionappStateCancelled = "cancelled"
)

func executionRun(value executionapp.Run) executiondomain.Run {
	return executiondomain.Run{
		State: executiondomain.RunState(value.State), InputBasisTargetSequence: value.InputBasisTargetSequence,
		Attempt: value.Attempt, LeaseHolderComputerID: value.LeaseHolderComputerID, LeaseExpiresAt: value.LeaseExpiresAt,
		Fence: value.Fence, PlacementDesiredRevision: value.PlacementDesiredRevision,
		ResultKind: executiondomain.ResultKind(value.ResultKind), ResultID: value.ResultID, ErrorCode: value.ErrorCode,
		StartedAt: value.StartedAt, CompletedAt: value.CompletedAt, CancelledAt: value.CancelledAt,
	}
}

func completeResponse(value executionapp.CompleteRunResult) (*runv1.CompleteRunResponse, error) {
	run, err := runMessage(value.Run)
	if err != nil || value.CommittedAt.IsZero() {
		return nil, internalError()
	}
	response := &runv1.CompleteRunResponse{Run: run, CommittedAt: timestamppb.New(value.CommittedAt)}
	switch value.Kind {
	case executionapp.ResultMessage:
		if value.Message == nil || value.HeldDraft != nil {
			return nil, internalError()
		}
		message, err := messagecodec.Message(*value.Message)
		if err != nil {
			return nil, err
		}
		response.Result = &runv1.CompleteRunResponse_Message{Message: message}
	case executionapp.ResultHeldDraft:
		if value.Message != nil || value.HeldDraft == nil {
			return nil, internalError()
		}
		draft, err := messagecodec.HeldDraft(*value.HeldDraft)
		if err != nil {
			return nil, err
		}
		response.Result = &runv1.CompleteRunResponse_HeldDraft{HeldDraft: draft}
	default:
		return nil, internalError()
	}
	return response, nil
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("agent run operation failed"))
}
