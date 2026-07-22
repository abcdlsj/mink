package delivery

import "github.com/abcdlsj/sumi/internal/store"

func validateActiveFacts(result store.ListDeliveriesResult) error {
	if result.ActiveRun == nil && result.ActiveLaunch != nil {
		return internalError()
	}
	if (result.ActiveRun == nil) != (result.ActiveDelivery == nil) {
		return internalError()
	}
	if result.ActiveRun != nil {
		if result.ActiveDelivery.ID != result.ActiveRun.DeliveryID || result.ActiveDelivery.AgentID != result.ActiveRun.AgentID ||
			result.ActiveDelivery.State != store.DeliveryStateAccepted {
			return internalError()
		}
		if (result.ActiveRun.State == store.RunStateAccepted && result.ActiveLaunch != nil) ||
			(result.ActiveRun.State == store.RunStateRunning && result.ActiveLaunch == nil) ||
			result.ActiveRun.State == store.RunStateCompleted {
			return internalError()
		}
	}
	if result.ActiveLaunch != nil {
		if result.ActiveLaunch.RunID != result.ActiveRun.ID || result.ActiveLaunch.AgentID != result.ActiveRun.AgentID ||
			result.ActiveLaunch.ClosedAt != nil || result.ActiveLaunch.CloseReason != "" {
			return internalError()
		}
	}
	return nil
}

func validateCompleteResult(result store.CompleteRunResult) error {
	switch result.Kind {
	case store.InboxResultMessage:
		if result.Message == nil || result.HeldDraft != nil || result.Run.ResultKind != store.InboxResultMessage || result.Run.ResultID != result.Message.ID {
			return internalError()
		}
	case store.InboxResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil || result.Run.ResultKind != store.InboxResultHeldDraft || result.Run.ResultID != result.HeldDraft.ID {
			return internalError()
		}
	default:
		return internalError()
	}
	return nil
}
