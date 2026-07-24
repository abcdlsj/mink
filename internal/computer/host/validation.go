package host

import (
	"context"
	"crypto/sha256"
	"errors"

	"connectrpc.com/connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/google/uuid"
)

func (d *Daemon) invalidateRuntimeOnUnauthenticated(ctx context.Context, session computerstate.RuntimeSession, err error) {
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		_ = d.config.State.DeleteRuntimeSession(ctx, session.AgentID, session.ComputerID, session.PlacementDesiredRevision)
		d.config.RuntimeSupervisor.Stop(session.AgentID, session.PlacementDesiredRevision)
	}
}

func validateListedRun(run *runv1.Run, agentID string) error {
	if run == nil || !validUUID(run.GetId()) || run.GetAgentId() != agentID || run.GetTarget() == nil ||
		!validUUID(run.GetInboxItemId()) || !validUUID(run.GetTriggerMessageId()) || !validUUID(run.GetSpaceId()) ||
		run.GetTriggerTargetSequence() == 0 || run.GetCreatedAt() == nil || run.GetCreatedAt().CheckValid() != nil {
		return errors.New("run facts are invalid")
	}
	switch run.GetState() {
	case runv1.RunState_RUN_STATE_QUEUED:
		if run.GetInputBasisTargetSequence() != 0 || run.GetAttempt() != 0 || run.GetLeaseHolderComputerId() != "" ||
			run.GetLeaseExpiresAt() != nil || run.GetFence() != 0 || run.GetPlacementDesiredRevision() != 0 {
			return errors.New("queued run binding is invalid")
		}
		return nil
	case runv1.RunState_RUN_STATE_RUNNING:
		return validateRunningRunFacts(run)
	default:
		return errors.New("listed run state is invalid")
	}
}

func validateRunningRun(run *runv1.Run, session computerstate.RuntimeSession) error {
	if err := validateRunningRunFacts(run); err != nil {
		return err
	}
	if run.GetAgentId() != session.AgentID || run.GetLeaseHolderComputerId() != session.ComputerID ||
		run.GetPlacementDesiredRevision() != session.PlacementDesiredRevision {
		return errors.New("run holder binding is invalid")
	}
	return nil
}

func validateRunningRunFacts(run *runv1.Run) error {
	if run == nil || run.GetState() != runv1.RunState_RUN_STATE_RUNNING || !validUUID(run.GetId()) ||
		!validUUID(run.GetAgentId()) || !validUUID(run.GetLeaseHolderComputerId()) || run.GetInputBasisTargetSequence() == 0 ||
		run.GetAttempt() == 0 || run.GetFence() == 0 || run.GetPlacementDesiredRevision() == 0 ||
		run.GetLeaseExpiresAt() == nil || run.GetLeaseExpiresAt().CheckValid() != nil ||
		run.GetStartedAt() == nil || run.GetStartedAt().CheckValid() != nil || run.GetCompletedAt() != nil || run.GetCancelledAt() != nil {
		return errors.New("running run facts are invalid")
	}
	return nil
}

func validateCompleteResponse(response *runv1.CompleteRunResponse, event computerstate.OutboxEvent) error {
	if response == nil || response.GetCommittedAt() == nil || response.GetCommittedAt().CheckValid() != nil ||
		(response.GetMessage() == nil && response.GetHeldDraft() == nil) {
		return errors.New("completion response facts are invalid")
	}
	run := response.GetRun()
	wantState := runv1.RunState_RUN_STATE_SUCCEEDED
	if event.Outcome == computerstate.CompletionFailed {
		wantState = runv1.RunState_RUN_STATE_FAILED
	}
	if run == nil || run.GetId() != event.RunID || run.GetAgentId() != event.AgentID || run.GetAttempt() != event.Attempt ||
		run.GetFence() != event.Fence || run.GetPlacementDesiredRevision() != event.PlacementDesiredRevision ||
		run.GetState() != wantState || run.GetErrorCode() != event.ErrorCode || run.GetCompletedAt() == nil ||
		run.GetCompletedAt().CheckValid() != nil || run.GetUsage().GetInputUnits() != event.UsageInputUnits ||
		run.GetUsage().GetOutputUnits() != event.UsageOutputUnits {
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
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
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
	if completion.Outcome != computerruntime.OutcomeSucceeded && completion.Outcome != computerruntime.OutcomeFailed {
		return errors.New("completion outcome is invalid")
	}
	if completion.Outcome == computerruntime.OutcomeSucceeded && completion.ErrorCode != "" {
		return errors.New("successful completion has an error code")
	}
	if completion.Outcome == computerruntime.OutcomeFailed && completion.ErrorCode == "" {
		return errors.New("failed completion requires an error code")
	}
	if len(completion.ErrorCode) > 255 {
		return errors.New("completion error code is too long")
	}
	return computerstate.ValidateCompletionPayload(completion.Body, completion.MentionedAgentIDs)
}

func outcomeName(outcome computerruntime.Outcome) computerstate.CompletionOutcome {
	if outcome == computerruntime.OutcomeSucceeded {
		return computerstate.CompletionSucceeded
	}
	return computerstate.CompletionFailed
}

func outcomeValue(outcome computerstate.CompletionOutcome) runv1.RunOutcome {
	if outcome == computerstate.CompletionSucceeded {
		return runv1.RunOutcome_RUN_OUTCOME_SUCCEEDED
	}
	return runv1.RunOutcome_RUN_OUTCOME_FAILED
}
