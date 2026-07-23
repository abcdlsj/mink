package collaboration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	sharedauthentication "github.com/abcdlsj/sumi/internal/authentication"
	"github.com/abcdlsj/sumi/internal/authority"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
	"github.com/abcdlsj/sumi/internal/transport/connectid"
)

type spaceMutationIDs struct {
	requestID string
	actor     PrincipalRef
	spaceID   string
}

type messageAuthentication struct {
	actor   PrincipalRef
	runtime authorityapp.RuntimeAuthentication
}

func (s *Service) authenticateMessage(ctx context.Context, header http.Header, mutation bool) (messageAuthentication, error) {
	resolved, err := sharedauthentication.Resolve(ctx, s.store, header, mutation, s.origin, s.now())
	if err != nil {
		switch {
		case errors.Is(err, sharedauthentication.ErrSameOrigin):
			return messageAuthentication{}, connect.NewError(connect.CodePermissionDenied, errors.New("same-origin browser request required"))
		case errors.Is(err, sharedauthentication.ErrUnavailable):
			return messageAuthentication{}, connect.NewError(connect.CodeUnavailable, errors.New("message authentication is unavailable"))
		default:
			return messageAuthentication{}, connect.NewError(connect.CodeUnauthenticated, errors.New("message actor authentication invalid"))
		}
	}
	if human, ok := resolved.Human(); ok {
		return messageAuthentication{actor: human}, nil
	}
	if agent, ok := resolved.Agent(); ok {
		return messageAuthentication{actor: agent.Principal, runtime: agent}, nil
	}
	return messageAuthentication{}, connect.NewError(connect.CodeUnauthenticated, errors.New("message actor authentication invalid"))
}

func (s *Service) memberParams(ctx context.Context, requestIDValue, spaceIDValue string, memberValue *spacev1.Principal) (ChangeMemberCommand, error) {
	ids, err := s.spaceMutationIDs(ctx, requestIDValue, spaceIDValue)
	if err != nil {
		return ChangeMemberCommand{}, err
	}
	member, err := principalParams(memberValue, ids.actor.OrganizationID)
	if err != nil {
		return ChangeMemberCommand{}, err
	}
	return ChangeMemberCommand{RequestID: ids.requestID, Actor: ids.actor, SpaceID: ids.spaceID, Member: member, Now: s.now()}, nil
}

func (s *Service) archiveParams(ctx context.Context, requestIDValue, spaceIDValue string) (ChangeSpaceArchiveCommand, error) {
	ids, err := s.spaceMutationIDs(ctx, requestIDValue, spaceIDValue)
	if err != nil {
		return ChangeSpaceArchiveCommand{}, err
	}
	return ChangeSpaceArchiveCommand{RequestID: ids.requestID, Actor: ids.actor, SpaceID: ids.spaceID, Now: s.now()}, nil
}

func (s *Service) spaceMutationIDs(ctx context.Context, requestIDValue, spaceIDValue string) (spaceMutationIDs, error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return spaceMutationIDs{}, err
	}
	requestID, err := connectid.CanonicalID(requestIDValue, "request id")
	if err != nil {
		return spaceMutationIDs{}, err
	}
	spaceID, err := connectid.CanonicalID(spaceIDValue, "space id")
	if err != nil {
		return spaceMutationIDs{}, err
	}
	return spaceMutationIDs{requestID: requestID, actor: actor, spaceID: spaceID}, nil
}

func principalParams(value *spacev1.Principal, organizationID string) (PrincipalRef, error) {
	if value == nil {
		return PrincipalRef{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal is required"))
	}
	kind := authoritydomain.PrincipalKind("")
	switch value.GetKind() {
	case spacev1.PrincipalKind_PRINCIPAL_KIND_HUMAN:
		kind = authoritydomain.PrincipalHuman
	case spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT:
		kind = authoritydomain.PrincipalAgent
	}
	if kind == "" {
		return PrincipalRef{}, connect.NewError(connect.CodeInvalidArgument, errors.New("principal kind is invalid"))
	}
	id, err := connectid.CanonicalID(value.GetId(), "principal id")
	if err != nil {
		return PrincipalRef{}, err
	}
	return PrincipalRef{Kind: kind, ID: id, OrganizationID: organizationID}, nil
}

func principalListParams(values []*spacev1.Principal, organizationID string) ([]PrincipalRef, error) {
	principals := make([]PrincipalRef, 0, len(values))
	for _, value := range values {
		principal, err := principalParams(value, organizationID)
		if err != nil {
			return nil, err
		}
		principals = append(principals, principal)
	}
	return principals, nil
}

func targetParams(value *spacev1.MessageTarget) (MessageTargetRef, error) {
	if value == nil {
		return MessageTargetRef{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is required"))
	}
	switch target := value.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		id, err := connectid.CanonicalID(target.SpaceId, "space id")
		if err != nil {
			return MessageTargetRef{}, err
		}
		return MessageTargetRef{Kind: TargetSpace, ID: id}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		id, err := connectid.CanonicalID(target.ThreadRootMessageId, "thread root message id")
		if err != nil {
			return MessageTargetRef{}, err
		}
		return MessageTargetRef{Kind: TargetThread, ID: id}, nil
	default:
		return MessageTargetRef{}, connect.NewError(connect.CodeInvalidArgument, errors.New("message target is invalid"))
	}
}

func groupNameValid(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("group name must not contain surrounding whitespace"))
	}
	size := utf8.RuneCountInString(name)
	if size < 1 || size > 100 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("group name must contain 1 to 100 characters"))
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("group name cannot contain control characters"))
		}
	}
	return nil
}

func messageBodyValid(body string) error {
	if !utf8.ValidString(body) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must be valid UTF-8"))
	}
	size := utf8.RuneCountInString(body)
	if size < 1 || size > maxMessageBodyRunes {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("message body must contain 1 to 400000 characters"))
	}
	return nil
}
