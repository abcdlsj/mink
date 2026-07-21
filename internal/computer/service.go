package computer

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/connectapi"
	"github.com/abcdlsj/sumi/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const connectivityTTL = 30 * time.Second

type Service struct {
	store *store.Store
	now   func() time.Time
}

func New(database *store.Store) *Service {
	return &Service{store: database, now: time.Now}
}

func (s *Service) CreateComputerPairing(ctx context.Context, request *connect.Request[computerv1.CreateComputerPairingRequest]) (*connect.Response[computerv1.CreateComputerPairingResponse], error) {
	actor, err := authority.Subject(ctx)
	if err != nil {
		return nil, err
	}
	requestID, err := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
	if err != nil {
		return nil, err
	}
	if err := pairingTokenValid(request.Msg.GetPairingToken()); err != nil {
		return nil, err
	}
	expiresAt := request.Msg.GetExpiresAt()
	if expiresAt == nil || expiresAt.CheckValid() != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("pairing expiry is invalid"))
	}
	pairing, err := s.store.CreateComputerPairing(ctx, store.CreateComputerPairingParams{
		RequestID: requestID,
		Actor:     actor,
		Token:     request.Msg.GetPairingToken(),
		ExpiresAt: expiresAt.AsTime(),
		Now:       s.now(),
	})
	if err := pairingError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&computerv1.CreateComputerPairingResponse{
		PairingId: pairing.ID,
		ExpiresAt: timestamppb.New(pairing.ExpiresAt),
	}), nil
}

func (s *Service) RegisterComputer(ctx context.Context, request *connect.Request[computerv1.RegisterComputerRequest]) (*connect.Response[computerv1.RegisterComputerResponse], error) {
	params, err := registerParams(request.Msg, s.now())
	if err != nil {
		return nil, err
	}
	var computer store.Computer
	if request.Msg.GetPairingToken() == "" {
		computer, err = s.store.RecoverComputer(ctx, params)
	} else {
		requestID, idErr := connectapi.CanonicalID(request.Msg.GetRequestId(), "request id")
		if idErr != nil {
			return nil, idErr
		}
		if err := pairingTokenValid(request.Msg.GetPairingToken()); err != nil {
			return nil, err
		}
		computer, err = s.store.PairComputer(ctx, store.PairComputerParams{
			RequestID:         requestID,
			PairingToken:      request.Msg.GetPairingToken(),
			RegistrationKey:   params.RegistrationKey,
			Name:              params.Name,
			OS:                params.OS,
			Arch:              params.Arch,
			SandboxCapability: params.SandboxCapability,
			Now:               params.Now,
		})
	}
	if errors.Is(err, store.ErrComputerNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
	if err := pairingError(err); err != nil {
		return nil, err
	}
	return connect.NewResponse(&computerv1.RegisterComputerResponse{Computer: computerMessage(computer, s.now())}), nil
}

func (s *Service) HeartbeatComputer(ctx context.Context, request *connect.Request[computerv1.HeartbeatComputerRequest]) (*connect.Response[computerv1.HeartbeatComputerResponse], error) {
	id, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	if err := registrationKeyValid(request.Msg.GetRegistrationKey()); err != nil {
		return nil, err
	}
	capability, err := sandboxCapability(request.Msg.GetSandboxCapability())
	if err != nil {
		return nil, err
	}
	computer, err := s.store.HeartbeatComputer(ctx, id, request.Msg.GetRegistrationKey(), capability, s.now())
	if errors.Is(err, store.ErrComputerNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
	if errors.Is(err, store.ErrRegistrationKeyMismatch) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("computer credentials do not match"))
	}
	if errors.Is(err, store.ErrSandboxCapabilityInvalid) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("sandbox capability is invalid"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&computerv1.HeartbeatComputerResponse{Computer: computerMessage(computer, s.now())}), nil
}

func (s *Service) GetComputer(ctx context.Context, request *connect.Request[computerv1.GetComputerRequest]) (*connect.Response[computerv1.GetComputerResponse], error) {
	id, err := connectapi.CanonicalID(request.Msg.GetComputerId(), "computer id")
	if err != nil {
		return nil, err
	}
	computer, err := s.store.GetComputer(ctx, id)
	if errors.Is(err, store.ErrComputerNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("computer not found"))
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&computerv1.GetComputerResponse{Computer: computerMessage(computer, s.now())}), nil
}

func (s *Service) ListComputers(ctx context.Context, _ *connect.Request[computerv1.ListComputersRequest]) (*connect.Response[computerv1.ListComputersResponse], error) {
	computers, err := s.store.ListComputers(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	response := &computerv1.ListComputersResponse{Computers: make([]*computerv1.Computer, 0, len(computers))}
	now := s.now()
	for _, computer := range computers {
		response.Computers = append(response.Computers, computerMessage(computer, now))
	}
	return connect.NewResponse(response), nil
}

func pairingTokenValid(token string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("pairing token is invalid"))
	}
	return nil
}

func pairingError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrComputerPairingInvalid):
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer pairing is invalid or expired"))
	case errors.Is(err, store.ErrComputerPairingConflict):
		return connect.NewError(connect.CodeAlreadyExists, errors.New("computer pairing request conflicts with existing data"))
	case errors.Is(err, store.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("computer pairing denied"))
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

func registerParams(request *computerv1.RegisterComputerRequest, now time.Time) (store.RegisterComputerParams, error) {
	if err := registrationKeyValid(request.GetRegistrationKey()); err != nil {
		return store.RegisterComputerParams{}, err
	}
	if err := displayNameValid(request.GetName()); err != nil {
		return store.RegisterComputerParams{}, err
	}
	os, ok := operatingSystem(request.GetOs())
	if !ok {
		return store.RegisterComputerParams{}, connect.NewError(connect.CodeInvalidArgument, errors.New("operating system must be macos or linux"))
	}
	arch, ok := architecture(request.GetArch())
	if !ok {
		return store.RegisterComputerParams{}, connect.NewError(connect.CodeInvalidArgument, errors.New("architecture must be arm64 or amd64"))
	}
	capability, err := sandboxCapability(request.GetSandboxCapability())
	if err != nil {
		return store.RegisterComputerParams{}, err
	}
	return store.RegisterComputerParams{
		RegistrationKey:   request.GetRegistrationKey(),
		Name:              request.GetName(),
		OS:                os,
		Arch:              arch,
		SandboxCapability: capability,
		Now:               now,
	}, nil
}

func registrationKeyValid(key string) error {
	if key == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is required"))
	}
	if len(key) > 256 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("registration key is too long"))
	}
	return nil
}

func displayNameValid(name string) error {
	if !utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name is invalid"))
	}
	size := utf8.RuneCountInString(name)
	if size < 1 || size > 100 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name must contain 1 to 100 characters"))
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("computer name is invalid"))
		}
	}
	return nil
}

func operatingSystem(value computerv1.OperatingSystem) (string, bool) {
	switch value {
	case computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS:
		return "macos", true
	case computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX:
		return "linux", true
	default:
		return "", false
	}
}

func architecture(value computerv1.Architecture) (string, bool) {
	switch value {
	case computerv1.Architecture_ARCHITECTURE_ARM64:
		return "arm64", true
	case computerv1.Architecture_ARCHITECTURE_AMD64:
		return "amd64", true
	default:
		return "", false
	}
}

func computerMessage(computer store.Computer, now time.Time) *computerv1.Computer {
	os := computerv1.OperatingSystem_OPERATING_SYSTEM_UNSPECIFIED
	if computer.OS == "macos" {
		os = computerv1.OperatingSystem_OPERATING_SYSTEM_MACOS
	} else if computer.OS == "linux" {
		os = computerv1.OperatingSystem_OPERATING_SYSTEM_LINUX
	}
	arch := computerv1.Architecture_ARCHITECTURE_UNSPECIFIED
	if computer.Arch == "arm64" {
		arch = computerv1.Architecture_ARCHITECTURE_ARM64
	} else if computer.Arch == "amd64" {
		arch = computerv1.Architecture_ARCHITECTURE_AMD64
	}
	expiresAt := computer.LastSeenAt.Add(connectivityTTL)
	return &computerv1.Computer{
		Id:                         computer.ID,
		Name:                       computer.Name,
		Os:                         os,
		Arch:                       arch,
		CreatedAt:                  timestamppb.New(computer.CreatedAt),
		LastSeenAt:                 timestamppb.New(computer.LastSeenAt),
		Online:                     now.Before(expiresAt),
		ConnectivityExpiresAt:      timestamppb.New(expiresAt),
		SandboxCapability:          sandboxCapabilityMessage(computer.SandboxCapability),
		SandboxDeclarationRevision: computer.SandboxDeclarationRevision,
	}
}

func sandboxCapability(value *computerv1.SandboxCapability) (store.SandboxCapability, error) {
	if value == nil || (value.GetProvider() == computerv1.SandboxProvider_SANDBOX_PROVIDER_UNSPECIFIED &&
		value.GetIsolation() == computerv1.SandboxIsolation_SANDBOX_ISOLATION_UNSPECIFIED &&
		value.GetWorkspaceAccess() == computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_UNSPECIFIED &&
		value.GetProcessControl() == computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_UNSPECIFIED &&
		value.GetFilesystemIsolation() == computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_UNSPECIFIED &&
		value.GetNetworkIsolation() == computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_UNSPECIFIED &&
		value.GetSecretMaterialization() == computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_UNSPECIFIED &&
		value.GetDaemonCrashCleanup() == computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_UNSPECIFIED) {
		return store.UnknownSandboxCapability(), nil
	}
	if value.GetProvider() == computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL &&
		value.GetIsolation() == computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL &&
		value.GetWorkspaceAccess() == computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE &&
		value.GetProcessControl() == computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP &&
		value.GetFilesystemIsolation() == computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE &&
		value.GetNetworkIsolation() == computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE &&
		value.GetSecretMaterialization() == computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT &&
		value.GetDaemonCrashCleanup() == computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE {
		return store.TrustedLocalSandboxCapability(), nil
	}
	return store.SandboxCapability{}, connect.NewError(connect.CodeInvalidArgument, errors.New("sandbox capability must be entirely unknown or the complete trusted-local declaration"))
}

func sandboxCapabilityMessage(value store.SandboxCapability) *computerv1.SandboxCapability {
	if value == store.TrustedLocalSandboxCapability() {
		return trustedLocalSandboxCapabilityMessage()
	}
	return &computerv1.SandboxCapability{}
}

func trustedLocalSandboxCapabilityMessage() *computerv1.SandboxCapability {
	return &computerv1.SandboxCapability{
		Provider:              computerv1.SandboxProvider_SANDBOX_PROVIDER_TRUSTED_LOCAL,
		Isolation:             computerv1.SandboxIsolation_SANDBOX_ISOLATION_TRUSTED_LOCAL,
		WorkspaceAccess:       computerv1.SandboxWorkspaceAccess_SANDBOX_WORKSPACE_ACCESS_DIRECT_READ_WRITE,
		ProcessControl:        computerv1.SandboxProcessControl_SANDBOX_PROCESS_CONTROL_CONTEXT_PROCESS_GROUP,
		FilesystemIsolation:   computerv1.SandboxFilesystemIsolation_SANDBOX_FILESYSTEM_ISOLATION_NONE,
		NetworkIsolation:      computerv1.SandboxNetworkIsolation_SANDBOX_NETWORK_ISOLATION_NONE,
		SecretMaterialization: computerv1.SandboxSecretMaterialization_SANDBOX_SECRET_MATERIALIZATION_EPHEMERAL_ENVIRONMENT,
		DaemonCrashCleanup:    computerv1.SandboxDaemonCrashCleanup_SANDBOX_DAEMON_CRASH_CLEANUP_NONE,
	}
}
