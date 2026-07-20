package organization

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	organizationv1 "github.com/abcdlsj/sumi/gen/go/sumi/organization/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service struct {
	store *store.Store
	now   func() time.Time
}

func New(database *store.Store) *Service {
	return &Service{store: database, now: time.Now}
}

func (s *Service) GetOrganization(ctx context.Context, _ *connect.Request[organizationv1.GetOrganizationRequest]) (*connect.Response[organizationv1.GetOrganizationResponse], error) {
	subject, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	organization, err := s.store.GetOrganization(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if organization.ID != subject.OrganizationID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("organization access denied"))
	}
	return connect.NewResponse(&organizationv1.GetOrganizationResponse{Organization: organizationMessage(organization)}), nil
}

func (s *Service) CreateHuman(ctx context.Context, request *connect.Request[organizationv1.CreateHumanRequest]) (*connect.Response[organizationv1.CreateHumanResponse], error) {
	subject, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	name := request.Msg.GetName()
	if name != strings.TrimSpace(name) || !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("human name must contain 1 to 100 characters without surrounding whitespace"))
	}
	role, ok := humanRoleName(request.Msg.GetRole())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("human role must be owner or member"))
	}
	if !authority.ValidCredential(request.Msg.GetCredential()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("human credential must be a high-entropy base64url value"))
	}
	human, err := s.store.CreateHuman(ctx, store.CreateHumanParams{RequestID: requestID, Actor: subject, Name: name, Role: role, Credential: request.Msg.GetCredential(), Now: s.now()})
	if errors.Is(err, store.ErrPermissionDenied) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("human creation denied"))
	}
	if errors.Is(err, store.ErrHumanRequestConflict) || errors.Is(err, store.ErrHumanNameExists) || errors.Is(err, store.ErrHumanCredentialExists) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("human request conflicts with an existing identity"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&organizationv1.CreateHumanResponse{Human: humanMessage(human)}), nil
}

func (s *Service) GetHuman(ctx context.Context, request *connect.Request[organizationv1.GetHumanRequest]) (*connect.Response[organizationv1.GetHumanResponse], error) {
	subject, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	id, err := connectapi.CanonicalID(request.Msg.GetHumanId(), "human id")
	if err != nil {
		return nil, err
	}
	human, err := s.store.GetHuman(ctx, id)
	if errors.Is(err, store.ErrHumanNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("human not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if human.OrganizationID != subject.OrganizationID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("human access denied"))
	}
	return connect.NewResponse(&organizationv1.GetHumanResponse{Human: humanMessage(human)}), nil
}

func (s *Service) ListHumans(ctx context.Context, _ *connect.Request[organizationv1.ListHumansRequest]) (*connect.Response[organizationv1.ListHumansResponse], error) {
	subject, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	humans, err := s.store.ListHumans(ctx, subject.OrganizationID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &organizationv1.ListHumansResponse{Humans: make([]*organizationv1.Human, 0, len(humans))}
	for _, human := range humans {
		response.Humans = append(response.Humans, humanMessage(human))
	}
	return connect.NewResponse(response), nil
}

func (s *Service) SetHumanStatus(ctx context.Context, request *connect.Request[organizationv1.SetHumanStatusRequest]) (*connect.Response[organizationv1.SetHumanStatusResponse], error) {
	subject, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	humanID, err := connectapi.CanonicalID(request.Msg.GetHumanId(), "human id")
	if err != nil {
		return nil, err
	}
	status, ok := humanStatusName(request.Msg.GetStatus())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("human status must be active or disabled"))
	}
	human, err := s.store.SetHumanStatus(ctx, store.SetHumanStatusParams{RequestID: requestID, Actor: subject, HumanID: humanID, Status: status, Now: s.now()})
	if errors.Is(err, store.ErrPermissionDenied) || errors.Is(err, store.ErrLastOwner) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("human status change denied"))
	}
	if errors.Is(err, store.ErrHumanNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("human not found"))
	}
	if errors.Is(err, store.ErrHumanStatusConflict) {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("request id already used for another human status"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&organizationv1.SetHumanStatusResponse{Human: humanMessage(human)}), nil
}

func organizationMessage(organization store.Organization) *organizationv1.Organization {
	return &organizationv1.Organization{Id: organization.ID, Name: organization.Name, BootstrapHumanId: organization.BootstrapHumanID, CreatedAt: timestamppb.New(organization.CreatedAt)}
}

func humanMessage(human store.Human) *organizationv1.Human {
	role := organizationv1.HumanRole_HUMAN_ROLE_UNSPECIFIED
	if human.Role == "owner" {
		role = organizationv1.HumanRole_HUMAN_ROLE_OWNER
	} else if human.Role == "member" {
		role = organizationv1.HumanRole_HUMAN_ROLE_MEMBER
	}
	status := organizationv1.HumanStatus_HUMAN_STATUS_UNSPECIFIED
	if human.Status == "active" {
		status = organizationv1.HumanStatus_HUMAN_STATUS_ACTIVE
	} else if human.Status == "disabled" {
		status = organizationv1.HumanStatus_HUMAN_STATUS_DISABLED
	}
	return &organizationv1.Human{Id: human.ID, OrganizationId: human.OrganizationID, Name: human.Name, Role: role, Status: status, CreatedAt: timestamppb.New(human.CreatedAt), UpdatedAt: timestamppb.New(human.UpdatedAt)}
}

func humanRoleName(role organizationv1.HumanRole) (string, bool) {
	switch role {
	case organizationv1.HumanRole_HUMAN_ROLE_OWNER:
		return "owner", true
	case organizationv1.HumanRole_HUMAN_ROLE_MEMBER:
		return "member", true
	default:
		return "", false
	}
}

func humanStatusName(status organizationv1.HumanStatus) (string, bool) {
	switch status {
	case organizationv1.HumanStatus_HUMAN_STATUS_ACTIVE:
		return "active", true
	case organizationv1.HumanStatus_HUMAN_STATUS_DISABLED:
		return "disabled", true
	default:
		return "", false
	}
}
