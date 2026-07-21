package agentmessage

import (
	"errors"
	"unicode/utf8"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxMessageRunes = 400_000
	maxMentions     = 64
)

func ValidateBody(body string) error {
	if !utf8.ValidString(body) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must be valid UTF-8"))
	}
	size := utf8.RuneCountInString(body)
	if size < 1 || size > maxMessageRunes {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must contain 1 to 400000 characters"))
	}
	return nil
}

func MentionedAgentIDs(values []string) ([]string, error) {
	if len(values) > maxMentions {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned agent count must be at most 64"))
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		id, err := connectapi.CanonicalID(value, "mentioned agent id")
		if err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned agent ids must be unique"))
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func ParseTarget(value *spacev1.MessageTarget) (store.MessageTarget, error) {
	if value == nil {
		return store.MessageTarget{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is required"))
	}
	switch target := value.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		id, err := connectapi.CanonicalID(target.SpaceId, "space id")
		if err != nil {
			return store.MessageTarget{}, err
		}
		return store.MessageTarget{Kind: store.MessageTargetSpace, ID: id}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		id, err := connectapi.CanonicalID(target.ThreadRootMessageId, "thread root message id")
		if err != nil {
			return store.MessageTarget{}, err
		}
		return store.MessageTarget{Kind: store.MessageTargetThread, ID: id}, nil
	default:
		return store.MessageTarget{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is invalid"))
	}
}

func Message(value store.Message) (*spacev1.Message, error) {
	if !canonicalID(value.ID) || !canonicalID(value.RequestID) || !canonicalID(value.SpaceID) || !canonicalID(value.Author.ID) {
		return nil, internalError()
	}
	author, err := Principal(value.Author)
	if err != nil {
		return nil, err
	}
	if err := ValidateBody(value.Body); err != nil {
		return nil, internalError()
	}
	if _, err := MentionedAgentIDs(value.MentionedAgentIDs); err != nil {
		return nil, internalError()
	}
	switch value.Target.Kind {
	case store.MessageTargetSpace:
		if value.Target.ID != value.SpaceID {
			return nil, internalError()
		}
	case store.MessageTargetThread:
		if !canonicalID(value.Target.ID) {
			return nil, internalError()
		}
	default:
		return nil, internalError()
	}
	message := &spacev1.Message{
		Id: value.ID, RequestId: value.RequestID, SpaceId: value.SpaceID,
		TargetSequence: value.TargetSequence, Author: author, Body: value.Body,
		MentionedAgentIds: value.MentionedAgentIDs, CreatedAt: timestamppb.New(value.CreatedAt),
	}
	if value.Target.Kind == store.MessageTargetThread {
		message.ThreadRootMessageId = value.Target.ID
	}
	return message, nil
}

func HeldDraft(value store.HeldDraft) (*inboxv1.HeldDraft, error) {
	if !canonicalID(value.ID) || !canonicalID(value.InboxItemID) || !canonicalID(value.SpaceID) ||
		(value.PredecessorDraftID != "" && !canonicalID(value.PredecessorDraftID)) {
		return nil, internalError()
	}
	if err := ValidateBody(value.Body); err != nil {
		return nil, internalError()
	}
	if _, err := MentionedAgentIDs(value.MentionedAgentIDs); err != nil {
		return nil, internalError()
	}
	state := HeldDraftState(value.State)
	if state == inboxv1.HeldDraftState_HELD_DRAFT_STATE_UNSPECIFIED {
		return nil, internalError()
	}
	action := DraftResolutionAction(value.ResolutionAction)
	if value.ResolutionAction != "" && action == inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_UNSPECIFIED {
		return nil, internalError()
	}
	switch value.State {
	case store.HeldDraftStateHeld:
		if value.ResolutionAction != "" || value.ResultKind != "" || value.ResultID != "" {
			return nil, internalError()
		}
	case store.HeldDraftStateCancelled:
		if value.ResolutionAction != store.DraftResolutionCancel || value.ResultKind != "" || value.ResultID != "" {
			return nil, internalError()
		}
	case store.HeldDraftStateSent:
		if value.ResolutionAction != store.DraftResolutionRetry || value.ResultKind != store.InboxResultMessage || !canonicalID(value.ResultID) {
			return nil, internalError()
		}
	case store.HeldDraftStateSuperseded:
		if value.ResolutionAction != store.DraftResolutionRetry || value.ResultKind != store.InboxResultHeldDraft || !canonicalID(value.ResultID) {
			return nil, internalError()
		}
	case store.HeldDraftStateRetargeted:
		if value.ResolutionAction != store.DraftResolutionRetarget ||
			(value.ResultKind != store.InboxResultMessage && value.ResultKind != store.InboxResultHeldDraft) || !canonicalID(value.ResultID) {
			return nil, internalError()
		}
	default:
		return nil, internalError()
	}
	target, err := Target(value.Target)
	if err != nil {
		return nil, err
	}
	message := &inboxv1.HeldDraft{
		Sequence: value.Sequence, Id: value.ID, InboxItemId: value.InboxItemID,
		PredecessorDraftId: value.PredecessorDraftID, SpaceId: value.SpaceID,
		Target: target, BasisTargetSequence: value.BasisTargetSequence, Body: value.Body,
		MentionedAgentIds: value.MentionedAgentIDs, State: state, ResolutionAction: action,
		CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
	}
	if value.ResultKind == store.InboxResultMessage {
		message.ResultRef = &inboxv1.HeldDraft_ResultMessageId{ResultMessageId: value.ResultID}
	} else if value.ResultKind == store.InboxResultHeldDraft {
		message.ResultRef = &inboxv1.HeldDraft_ResultHeldDraftId{ResultHeldDraftId: value.ResultID}
	}
	return message, nil
}

func Target(value store.MessageTarget) (*spacev1.MessageTarget, error) {
	if !canonicalID(value.ID) {
		return nil, internalError()
	}
	message := &spacev1.MessageTarget{}
	switch value.Kind {
	case store.MessageTargetSpace:
		message.Target = &spacev1.MessageTarget_SpaceId{SpaceId: value.ID}
	case store.MessageTargetThread:
		message.Target = &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: value.ID}
	default:
		return nil, internalError()
	}
	return message, nil
}

func Principal(value store.Principal) (*spacev1.Principal, error) {
	if !canonicalID(value.ID) {
		return nil, internalError()
	}
	switch value.Kind {
	case "human":
		return &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: value.ID}, nil
	case "agent":
		return &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: value.ID}, nil
	default:
		return nil, internalError()
	}
}

func HeldDraftState(value string) inboxv1.HeldDraftState {
	switch value {
	case store.HeldDraftStateHeld:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_HELD
	case store.HeldDraftStateSent:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_SENT
	case store.HeldDraftStateCancelled:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_CANCELLED
	case store.HeldDraftStateSuperseded:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_SUPERSEDED
	case store.HeldDraftStateRetargeted:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_RETARGETED
	default:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_UNSPECIFIED
	}
}

func DraftResolutionAction(value string) inboxv1.DraftResolutionAction {
	switch value {
	case store.DraftResolutionRetry:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETRY
	case store.DraftResolutionCancel:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL
	case store.DraftResolutionRetarget:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETARGET
	default:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_UNSPECIFIED
	}
}

func canonicalID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func internalError() error {
	return connect.NewError(connect.CodeInternal, errors.New("agent message fact invalid"))
}
