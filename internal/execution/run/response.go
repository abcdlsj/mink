package run

import (
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/abcdlsj/sumi/internal/servicesvc"
	"github.com/abcdlsj/sumi/internal/transport/msgcodec"
)

var stateToRunProto = map[string]runv1.RunState{
	"queued":     runv1.RunState_RUN_STATE_QUEUED,
	"running":    runv1.RunState_RUN_STATE_RUNNING,
	"succeeded":  runv1.RunState_RUN_STATE_SUCCEEDED,
	"failed":     runv1.RunState_RUN_STATE_FAILED,
	"cancelled":  runv1.RunState_RUN_STATE_CANCELLED,
}

func runToProto(v executionapp.Run) (*runv1.Run, error) {
	if err := executiondomain.ValidateRun(toExecRun(v)); err != nil {
		return nil, servicesvc.ErrInternal
	}
	target, err := messagecodec.Target(v.Target)
	if err != nil {
		return nil, err
	}
	state, ok := stateToRunProto[v.State]
	if !ok {
		return nil, servicesvc.ErrInternal
	}
	msg := &runv1.Run{
		Sequence: v.Sequence, Id: v.ID, AgentId: v.AgentID,
		InboxItemId: v.InboxItemID, TriggerMessageId: v.TriggerMessageID,
		SpaceId: v.SpaceID, Target: target,
		TriggerTargetSequence:       v.TriggerTargetSequence,
		InputBasisTargetSequence:    v.InputBasisTargetSequence,
		Attempt:                     v.Attempt,
		LeaseHolderComputerId:       v.LeaseHolderComputerID,
		Fence:                       v.Fence,
		PlacementDesiredRevision:    v.PlacementDesiredRevision,
		State: state,
		Usage: &runv1.RunUsage{
			InputUnits: v.Usage.InputUnits, OutputUnits: v.Usage.OutputUnits,
		},
		ErrorCode: v.ErrorCode,
		CreatedAt: servicesvc.Ts(v.CreatedAt),
	}
	switch v.ResultKind {
	case "":
	case executionapp.ResultMessage:
		msg.ResultRef = &runv1.Run_ResultMessageId{ResultMessageId: v.ResultID}
	case executionapp.ResultHeldDraft:
		msg.ResultRef = &runv1.Run_ResultHeldDraftId{ResultHeldDraftId: v.ResultID}
	default:
		return nil, servicesvc.ErrInternal
	}
	if v.LeaseExpiresAt != nil {
		msg.LeaseExpiresAt = servicesvc.Ts(*v.LeaseExpiresAt)
	}
	if v.StartedAt != nil {
		msg.StartedAt = servicesvc.Ts(*v.StartedAt)
	}
	if v.CompletedAt != nil {
		msg.CompletedAt = servicesvc.Ts(*v.CompletedAt)
	}
	if v.CancelledAt != nil {
		msg.CancelledAt = servicesvc.Ts(*v.CancelledAt)
	}
	return msg, nil
}

func toExecRun(v executionapp.Run) executiondomain.Run {
	return executiondomain.Run{
		State:                   executiondomain.RunState(v.State),
		InputBasisTargetSequence: v.InputBasisTargetSequence,
		Attempt:                 v.Attempt,
		LeaseHolderComputerID:   v.LeaseHolderComputerID,
		LeaseExpiresAt:          v.LeaseExpiresAt,
		Fence:                   v.Fence,
		PlacementDesiredRevision: v.PlacementDesiredRevision,
		ResultKind:              executiondomain.ResultKind(v.ResultKind),
		ResultID:                v.ResultID,
		ErrorCode:               v.ErrorCode,
		StartedAt:               v.StartedAt,
		CompletedAt:             v.CompletedAt,
		CancelledAt:             v.CancelledAt,
	}
}

func completeResponse(v executionapp.CompleteRunResult) (*runv1.CompleteRunResponse, error) {
	run, err := runToProto(v.Run)
	if err != nil || v.CommittedAt.IsZero() {
		return nil, servicesvc.ErrInternal
	}
	resp := &runv1.CompleteRunResponse{Run: run, CommittedAt: servicesvc.Ts(v.CommittedAt)}
	switch v.Kind {
	case executionapp.ResultMessage:
		if v.Message == nil || v.HeldDraft != nil {
			return nil, servicesvc.ErrInternal
		}
		msg, err := messagecodec.Message(*v.Message)
		if err != nil {
			return nil, err
		}
		resp.Result = &runv1.CompleteRunResponse_Message{Message: msg}
	case executionapp.ResultHeldDraft:
		if v.Message != nil || v.HeldDraft == nil {
			return nil, servicesvc.ErrInternal
		}
		draft, err := messagecodec.HeldDraft(*v.HeldDraft)
		if err != nil {
			return nil, err
		}
		resp.Result = &runv1.CompleteRunResponse_HeldDraft{HeldDraft: draft}
	default:
		return nil, servicesvc.ErrInternal
	}
	return resp, nil
}
