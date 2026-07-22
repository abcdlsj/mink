package delivery

import (
	"github.com/abcdlsj/sumi/internal/execution"
)

func validateActiveFacts(result DeliveryListResult) error {
	var delivery *execution.Delivery
	if result.ActiveDelivery != nil {
		delivery = &result.ActiveDelivery.Fact
	}
	if err := execution.ValidateActiveFacts(execution.ActiveFacts{
		Delivery: delivery,
		Run:      result.ActiveRun,
		Launch:   result.ActiveLaunch,
	}); err != nil {
		return internalError()
	}
	return nil
}

func validateCompleteResult(result CompleteRunResult) error {
	messageID, heldDraftID := "", ""
	if result.Message != nil {
		messageID = result.Message.ID
	}
	if result.HeldDraft != nil {
		heldDraftID = result.HeldDraft.ID
	}
	if err := execution.ValidateResult(result.Run, result.Kind, result.Run.ResultID, messageID, heldDraftID); err != nil {
		return internalError()
	}
	switch result.Kind {
	case execution.ResultMessage:
		if result.Message == nil || result.HeldDraft != nil {
			return internalError()
		}
	case execution.ResultHeldDraft:
		if result.Message != nil || result.HeldDraft == nil {
			return internalError()
		}
	default:
		return internalError()
	}
	return nil
}
