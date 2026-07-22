package delivery

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	execution "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/abcdlsj/sumi/internal/store"
)

type DeliveryResult struct {
	Fact             execution.Delivery
	Sequence         uint64
	InboxItemID      string
	TriggerMessageID string
	SpaceID          string
	CreatedAt        time.Time
}

type DeliveryListResult struct {
	Deliveries     []DeliveryResult
	NextSequence   uint64
	ActiveDelivery *DeliveryResult
	ActiveRun      *execution.Run
	ActiveLaunch   *execution.Launch
}

type MessageTargetView struct {
	Kind string
	ID   string
}

type MessageView struct {
	ID                string
	RequestID         string
	SpaceID           string
	Target            MessageTargetView
	TargetSequence    uint64
	AuthorKind        authoritydomain.PrincipalKind
	AuthorID          string
	Body              string
	MentionedAgentIDs []string
	CreatedAt         time.Time
}

type HeldDraftView struct {
	Sequence            uint64
	ID                  string
	AgentID             string
	InboxItemID         string
	PredecessorDraftID  string
	SpaceID             string
	Target              MessageTargetView
	BasisTargetSequence uint64
	Body                string
	MentionedAgentIDs   []string
	HeldReason          string
	State               string
	ResolutionAction    string
	ResultKind          string
	ResultID            string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type CompleteRunResult struct {
	Run         execution.Run
	Kind        execution.ResultKind
	Message     *MessageView
	HeldDraft   *HeldDraftView
	CommittedAt time.Time
}

func mapDelivery(value *store.Delivery) *DeliveryResult {
	if value == nil {
		return nil
	}
	return &DeliveryResult{
		Fact: executionDelivery(*value), Sequence: value.Sequence, InboxItemID: value.InboxItemID,
		TriggerMessageID: value.TriggerMessageID, SpaceID: value.SpaceID, CreatedAt: value.CreatedAt,
	}
}

func mapDeliveries(values []store.Delivery) []DeliveryResult {
	result := make([]DeliveryResult, 0, len(values))
	for index := range values {
		result = append(result, *mapDelivery(&values[index]))
	}
	return result
}

func mapRun(value *store.Run) *execution.Run {
	if value == nil {
		return nil
	}
	result := executionRun(*value)
	return &result
}

func mapLaunch(value *store.RunLaunch) *execution.Launch {
	if value == nil {
		return nil
	}
	result := executionLaunch(*value)
	return &result
}

func mapMessage(value *store.Message) *MessageView {
	if value == nil {
		return nil
	}
	return &MessageView{
		ID: value.ID, RequestID: value.RequestID, SpaceID: value.SpaceID,
		Target: MessageTargetView{Kind: value.Target.Kind, ID: value.Target.ID}, TargetSequence: value.TargetSequence,
		AuthorKind: value.Author.Kind, AuthorID: value.Author.ID, Body: value.Body,
		MentionedAgentIDs: append([]string(nil), value.MentionedAgentIDs...), CreatedAt: value.CreatedAt,
	}
}

func mapHeldDraft(value *store.HeldDraft) *HeldDraftView {
	if value == nil {
		return nil
	}
	return &HeldDraftView{
		Sequence: value.Sequence, ID: value.ID, AgentID: value.AgentID, InboxItemID: value.InboxItemID,
		PredecessorDraftID: value.PredecessorDraftID, SpaceID: value.SpaceID,
		Target: MessageTargetView{Kind: value.Target.Kind, ID: value.Target.ID}, BasisTargetSequence: value.BasisTargetSequence,
		Body: value.Body, MentionedAgentIDs: append([]string(nil), value.MentionedAgentIDs...), HeldReason: value.HeldReason,
		State: value.State, ResolutionAction: value.ResolutionAction, ResultKind: value.ResultKind, ResultID: value.ResultID,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func mapCompleteRun(value store.CompleteRunResult) CompleteRunResult {
	return CompleteRunResult{
		Run: executionRun(value.Run), Kind: execution.ResultKind(value.Kind), Message: mapMessage(value.Message),
		HeldDraft: mapHeldDraft(value.HeldDraft), CommittedAt: value.CommittedAt,
	}
}

func storeMessageView(value MessageView) store.Message {
	return store.Message{
		ID: value.ID, RequestID: value.RequestID, SpaceID: value.SpaceID,
		Target: store.MessageTarget{Kind: value.Target.Kind, ID: value.Target.ID}, TargetSequence: value.TargetSequence,
		Author: store.Principal{Kind: value.AuthorKind, ID: value.AuthorID}, Body: value.Body,
		MentionedAgentIDs: append([]string(nil), value.MentionedAgentIDs...), CreatedAt: value.CreatedAt,
	}
}

func storeHeldDraftView(value HeldDraftView) store.HeldDraft {
	return store.HeldDraft{
		Sequence: value.Sequence, ID: value.ID, AgentID: value.AgentID, InboxItemID: value.InboxItemID,
		PredecessorDraftID: value.PredecessorDraftID, SpaceID: value.SpaceID,
		Target: store.MessageTarget{Kind: value.Target.Kind, ID: value.Target.ID}, BasisTargetSequence: value.BasisTargetSequence,
		Body: value.Body, MentionedAgentIDs: append([]string(nil), value.MentionedAgentIDs...), HeldReason: value.HeldReason,
		State: value.State, ResolutionAction: value.ResolutionAction, ResultKind: value.ResultKind, ResultID: value.ResultID,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}
