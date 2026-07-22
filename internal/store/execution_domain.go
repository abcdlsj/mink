package store

import execution "github.com/abcdlsj/sumi/internal/execution/domain"

func executionDelivery(value Delivery) execution.Delivery {
	return execution.Delivery{
		ID: value.ID, AgentID: value.AgentID, TargetKind: string(value.Target.Kind), TargetID: value.Target.ID,
		TriggerTargetSequence: value.TriggerTargetSequence, State: execution.DeliveryState(value.State),
		AcceptedAt: value.AcceptedAt, CompletedAt: value.CompletedAt,
	}
}

func executionRun(value Run) execution.Run {
	return execution.Run{
		ID: value.ID, DeliveryID: value.DeliveryID, AgentID: value.AgentID,
		BasisTargetSequence: value.BasisTargetSequence, State: execution.RunState(value.State),
		Outcome: execution.Outcome(value.Outcome), ResultKind: execution.ResultKind(value.ResultKind), ResultID: value.ResultID,
		AcceptedAt: value.AcceptedAt, StartedAt: value.StartedAt, CompletedAt: value.CompletedAt,
	}
}

func executionLaunch(value RunLaunch) execution.Launch {
	return execution.Launch{
		ID: value.ID, RunID: value.RunID, AgentID: value.AgentID, HolderComputerID: value.HolderComputerID,
		HolderPlacementGeneration: value.HolderPlacementGeneration, Fence: value.Fence,
		ClaimedAt: value.ClaimedAt, ExpiresAt: value.ExpiresAt, ClosedAt: value.ClosedAt,
		CloseReason: execution.CloseReason(value.CloseReason),
	}
}

func executionLaunchOrNil(value *RunLaunch) *execution.Launch {
	if value == nil {
		return nil
	}
	converted := executionLaunch(*value)
	return &converted
}

func executionActiveFacts(result ListDeliveriesResult) execution.ActiveFacts {
	facts := execution.ActiveFacts{}
	if result.ActiveDelivery != nil {
		value := executionDelivery(*result.ActiveDelivery)
		facts.Delivery = &value
	}
	if result.ActiveRun != nil {
		value := executionRun(*result.ActiveRun)
		facts.Run = &value
	}
	if result.ActiveLaunch != nil {
		value := executionLaunch(*result.ActiveLaunch)
		facts.Launch = &value
	}
	return facts
}
