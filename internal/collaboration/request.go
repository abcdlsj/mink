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
	"github.com/abcdlsj/sumi/internal/servicesvc"
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
			return messageAuthentication{}, servicesvc.ErrUnauth
		}
	}
	if h, ok := resolved.Human(); ok {
		return messageAuthentication{actor: h}, nil
	}
	if a, ok := resolved.Agent(); ok {
		return messageAuthentication{actor: a.Principal, runtime: a}, nil
	}
	return messageAuthentication{}, servicesvc.ErrUnauth
}

func (s *Service) buildMemberParams(ctx context.Context, requestIDValue, spaceIDValue string, memberValue *spacev1.Principal) (ChangeMemberCommand, error) {
	ids, err := s.spaceMutationIDs(ctx, requestIDValue, spaceIDValue)
	if err != nil {
		return ChangeMemberCommand{}, err
	}
	member, err := parsePrincipal(memberValue, ids.actor.OrganizationID)
	if err != nil {
		return ChangeMemberCommand{}, err
	}
	return ChangeMemberCommand{
		RequestID: ids.requestID, Actor: ids.actor, SpaceID: ids.spaceID,
		Member: member, Now: s.now(),
	}, nil
}

func (s *Service) buildArchiveParams(ctx context.Context, requestIDValue, spaceIDValue string) (ChangeSpaceArchiveCommand, error) {
	ids, err := s.spaceMutationIDs(ctx, requestIDValue, spaceIDValue)
	if err != nil {
		return ChangeSpaceArchiveCommand{}, err
	}
	return ChangeSpaceArchiveCommand{
		RequestID: ids.requestID, Actor: ids.actor, SpaceID: ids.spaceID, Now: s.now(),
	}, nil
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

func parsePrincipal(v *spacev1.Principal, orgID string) (PrincipalRef, error) {
	if v == nil {
		return PrincipalRef{}, servicesvc.InvalArg("principal is required")
	}
	kind, ok := kindToDomain[v.GetKind()]
	if !ok {
		return PrincipalRef{}, servicesvc.InvalArg("principal kind is invalid")
	}
	id, err := connectid.CanonicalID(v.GetId(), "principal id")
	if err != nil {
		return PrincipalRef{}, err
	}
	return PrincipalRef{Kind: kind, ID: id, OrganizationID: orgID}, nil
}

func parsePrincipalList(values []*spacev1.Principal, orgID string) ([]PrincipalRef, error) {
	principals := make([]PrincipalRef, 0, len(values))
	for _, v := range values {
		p, err := parsePrincipal(v, orgID)
		if err != nil {
			return nil, err
		}
		principals = append(principals, p)
	}
	return principals, nil
}

func parseTarget(v *spacev1.MessageTarget) (MessageTargetRef, error) {
	if v == nil {
		return MessageTargetRef{}, servicesvc.InvalArg("message target is required")
	}
	switch t := v.GetTarget().(type) {
	case *spacev1.MessageTarget_SpaceId:
		id, err := connectid.CanonicalID(t.SpaceId, "space id")
		if err != nil {
			return MessageTargetRef{}, err
		}
		return MessageTargetRef{Kind: TargetSpace, ID: id}, nil
	case *spacev1.MessageTarget_ThreadRootMessageId:
		id, err := connectid.CanonicalID(t.ThreadRootMessageId, "thread root message id")
		if err != nil {
			return MessageTargetRef{}, err
		}
		return MessageTargetRef{Kind: TargetThread, ID: id}, nil
	default:
		return MessageTargetRef{}, servicesvc.InvalArg("message target is invalid")
	}
}

func validateGroupName(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return servicesvc.InvalArg("group name must not contain surrounding whitespace")
	}
	size := utf8.RuneCountInString(name)
	if size < 1 || size > 100 {
		return servicesvc.InvalArg("group name must contain 1 to 100 characters")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return servicesvc.InvalArg("group name cannot contain control characters")
		}
	}
	return nil
}

func validateBody(body string) error {
	if !utf8.ValidString(body) {
		return servicesvc.InvalArg("message body must be valid UTF-8")
	}
	size := utf8.RuneCountInString(body)
	if size < 1 || size > maxMessageBodyRunes {
		return servicesvc.InvalArg("message body must contain 1 to 400000 characters")
	}
	return nil
}
