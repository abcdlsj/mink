package msgcodec

import (
	"errors"
	"unicode/utf8"

	"connectrpc.com/connect"
	inboxv1 "github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	collaborationapp "github.com/abcdlsj/sumi/internal/collaboration/application"
	collaborationdomain "github.com/abcdlsj/sumi/internal/collaboration/domain"
	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	idpkg "github.com/abcdlsj/sumi/internal/transport/id"
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

func MentionedPrincipals(values []*spacev1.Principal) ([]authoritydomain.Principal, error) {
	if len(values) > maxMentions {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned principal count must be at most 64"))
	}
	seen := make(map[authoritydomain.Principal]struct{}, len(values))
	result := make([]authoritydomain.Principal, 0, len(values))
	for _, value := range values {
		if value == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned principal is required"))
		}
		cid, err := idpkg.CanonicalID(value.GetId(), "mentioned principal id")
		if err != nil {
			return nil, err
		}
		principal := authoritydomain.Principal{ID: cid}
		switch value.GetKind() {
		case spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN:
			principal.Kind = authoritydomain.PrincipalHuman
		case spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT:
			principal.Kind = authoritydomain.PrincipalAgent
		default:
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned principal kind is invalid"))
		}
		if _, ok := seen[principal]; ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("mentioned principals must be unique"))
		}
		seen[principal] = struct{}{}
		result = append(result, principal)
	}
	return result, nil
}

func ParseTarget(value *spacev1.MessageTarget) (collaborationapp.MessageTarget, error) {
	if value == nil {
		return collaborationapp.MessageTarget{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is required"))
	}
	switch target := value.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		cid, err := idpkg.CanonicalID(target.SpaceId, "space id")
		if err != nil {
			return collaborationapp.MessageTarget{}, err
		}
		return collaborationapp.MessageTarget{Kind: collaborationdomain.TargetSpace, ID: cid}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		cid, err := idpkg.CanonicalID(target.ThreadRootMessageId, "thread root message id")
		if err != nil {
			return collaborationapp.MessageTarget{}, err
		}
		return collaborationapp.MessageTarget{Kind: collaborationdomain.TargetThread, ID: cid}, nil
	default:
		return collaborationapp.MessageTarget{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is invalid"))
	}
}

func Message(value collaborationapp.Message) (*spacev1.Message, error) {
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
	mentions, err := Principals(value.MentionedPrincipals)
	if err != nil {
		return nil, internalError()
	}
	switch value.Target.Kind {
	case collaborationdomain.TargetSpace:
		if value.Target.ID != value.SpaceID {
			return nil, internalError()
		}
	case collaborationdomain.TargetThread:
		if !canonicalID(value.Target.ID) {
			return nil, internalError()
		}
	default:
		return nil, internalError()
	}
	message := &spacev1.Message{
		Id: value.ID, RequestId: value.RequestID, SpaceId: value.SpaceID,
		TargetSequence: value.TargetSequence, Author: author, Body: value.Body,
		MentionedPrincipals: mentions, CreatedAt: timestamppb.New(value.CreatedAt),
	}
	if value.Target.Kind == collaborationdomain.TargetThread {
		message.ThreadRootMessageId = value.Target.ID
	}
	return message, nil
}

func HeldDraft(value executionapp.HeldDraft) (*inboxv1.HeldDraft, error) {
	if !canonicalID(value.ID) || !canonicalID(value.InboxItemID) || !canonicalID(value.SpaceID) ||
		(value.PredecessorDraftID != "" && !canonicalID(value.PredecessorDraftID)) {
		return nil, internalError()
	}
	if err := ValidateBody(value.Body); err != nil {
		return nil, internalError()
	}
	mentions, err := Principals(value.MentionedPrincipals)
	if err != nil {
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
	case executionapp.HeldDraftStateHeld:
		if value.ResolutionAction != "" || value.ResultKind != "" || value.ResultID != "" {
			return nil, internalError()
		}
	case executionapp.HeldDraftStateCancelled:
		if value.ResolutionAction != executionapp.DraftResolutionCancel || value.ResultKind != "" || value.ResultID != "" {
			return nil, internalError()
		}
	case executionapp.HeldDraftStateSent:
		if value.ResolutionAction != executionapp.DraftResolutionRetry || value.ResultKind != executionapp.ResultMessage || !canonicalID(value.ResultID) {
			return nil, internalError()
		}
	case executionapp.HeldDraftStateSuperseded:
		if value.ResolutionAction != executionapp.DraftResolutionRetry || value.ResultKind != executionapp.ResultHeldDraft || !canonicalID(value.ResultID) {
			return nil, internalError()
		}
	case executionapp.HeldDraftStateRetargeted:
		if value.ResolutionAction != executionapp.DraftResolutionRetarget ||
			(value.ResultKind != executionapp.ResultMessage && value.ResultKind != executionapp.ResultHeldDraft) || !canonicalID(value.ResultID) {
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
		MentionedPrincipals: mentions, State: state, ResolutionAction: action,
		CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt),
	}
	if value.ResultKind == executionapp.ResultMessage {
		message.ResultRef = &inboxv1.HeldDraft_ResultMessageId{ResultMessageId: value.ResultID}
	} else if value.ResultKind == executionapp.ResultHeldDraft {
		message.ResultRef = &inboxv1.HeldDraft_ResultHeldDraftId{ResultHeldDraftId: value.ResultID}
	}
	return message, nil
}

func Target(value collaborationapp.MessageTarget) (*spacev1.MessageTarget, error) {
	if !canonicalID(value.ID) {
		return nil, internalError()
	}
	message := &spacev1.MessageTarget{}
	switch value.Kind {
	case collaborationdomain.TargetSpace:
		message.Target = &spacev1.MessageTarget_SpaceId{SpaceId: value.ID}
	case collaborationdomain.TargetThread:
		message.Target = &spacev1.MessageTarget_ThreadRootMessageId{ThreadRootMessageId: value.ID}
	default:
		return nil, internalError()
	}
	return message, nil
}

func Principal(value authoritydomain.Principal) (*spacev1.Principal, error) {
	if !canonicalID(value.ID) {
		return nil, internalError()
	}
	switch value.Kind {
	case authoritydomain.PrincipalHuman:
		return &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN, Id: value.ID}, nil
	case authoritydomain.PrincipalAgent:
		return &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: value.ID}, nil
	default:
		return nil, internalError()
	}
}

func Principals(values []authoritydomain.Principal) ([]*spacev1.Principal, error) {
	result := make([]*spacev1.Principal, 0, len(values))
	seen := make(map[authoritydomain.Principal]struct{}, len(values))
	for _, value := range values {
		value.OrganizationID = ""
		if _, exists := seen[value]; exists {
			return nil, internalError()
		}
		seen[value] = struct{}{}
		principal, err := Principal(value)
		if err != nil {
			return nil, err
		}
		result = append(result, principal)
	}
	if len(result) > maxMentions {
		return nil, internalError()
	}
	return result, nil
}

func HeldDraftState(value string) inboxv1.HeldDraftState {
	switch value {
	case executionapp.HeldDraftStateHeld:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_HELD
	case executionapp.HeldDraftStateSent:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_SENT
	case executionapp.HeldDraftStateCancelled:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_CANCELLED
	case executionapp.HeldDraftStateSuperseded:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_SUPERSEDED
	case executionapp.HeldDraftStateRetargeted:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_RETARGETED
	default:
		return inboxv1.HeldDraftState_HELD_DRAFT_STATE_UNSPECIFIED
	}
}

func DraftResolutionAction(value string) inboxv1.DraftResolutionAction {
	switch value {
	case executionapp.DraftResolutionRetry:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_RETRY
	case executionapp.DraftResolutionCancel:
		return inboxv1.DraftResolutionAction_DRAFT_RESOLUTION_ACTION_CANCEL
	case executionapp.DraftResolutionRetarget:
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
