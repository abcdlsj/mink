package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	computerv1 "github.com/abcdlsj/sumi/gen/go/sumi/computer/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/computer/v1/computerv1connect"
	placementv1 "github.com/abcdlsj/sumi/gen/go/sumi/placement/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/placement/v1/placementv1connect"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/workspace"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	ServerURL           string
	DataRoot            string
	RegistrationKey     string
	Name                string
	OS                  computerv1.OperatingSystem
	Arch                computerv1.Architecture
	HTTPClient          *http.Client
	State               *computerstate.State
	CapabilityInventory *computerv1.CapabilityInventoryDeclaration
}

type Host struct {
	config     Config
	computers  computerv1connect.ComputerServiceClient
	placements placementv1connect.PlacementServiceClient
}

type SyncResult struct {
	ComputerID  string
	Assignments int
	Ready       int
	Failed      int
}

func New(config Config) *Host {
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Host{
		config:     config,
		computers:  computerv1connect.NewComputerServiceClient(client, config.ServerURL),
		placements: placementv1connect.NewPlacementServiceClient(client, config.ServerURL),
	}
}

func (h *Host) SyncOnce(ctx context.Context) (SyncResult, error) {
	identity, hasIdentity, err := h.identity(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	if !hasIdentity {
		return SyncResult{}, errors.New("computer identity is unavailable; pairing is required")
	}
	inventory, err := h.capabilityInventory()
	if err != nil {
		return SyncResult{}, err
	}
	heartbeat, err := h.computers.HeartbeatComputer(ctx, connect.NewRequest(&computerv1.HeartbeatComputerRequest{
		ComputerId: identity.ComputerID, RegistrationKey: identity.RegistrationKey,
		CapabilityInventory: inventory,
	}))
	if err != nil {
		return SyncResult{}, fmt.Errorf("heartbeat computer: %w", err)
	}
	computerID := heartbeat.Msg.GetComputer().GetId()
	if computerID != identity.ComputerID {
		return SyncResult{}, errors.New("heartbeat computer does not match persisted identity")
	}
	assignments, err := h.placements.ListComputerAssignments(ctx, connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId:      computerID,
		RegistrationKey: identity.RegistrationKey,
	}))
	if err != nil {
		return SyncResult{ComputerID: computerID}, fmt.Errorf("list assignments: %w", err)
	}

	result := SyncResult{ComputerID: computerID, Assignments: len(assignments.Msg.GetAssignments())}
	var syncErrors []error
	for _, assignment := range assignments.Msg.GetAssignments() {
		provisionError := h.provisionAndAcknowledge(ctx, assignment, identity.RegistrationKey)
		if provisionError == nil {
			result.Ready++
			continue
		}
		result.Failed++
		syncErrors = append(syncErrors, provisionError)
	}
	return result, errors.Join(syncErrors...)
}

func (h *Host) capabilityInventory() (*computerv1.CapabilityInventoryDeclaration, error) {
	if h.config.CapabilityInventory != nil {
		return proto.Clone(h.config.CapabilityInventory).(*computerv1.CapabilityInventoryDeclaration), nil
	}
	return CapabilityInventory()
}

func (h *Host) identity(ctx context.Context) (computerstate.Identity, bool, error) {
	if h.config.State == nil {
		return computerstate.Identity{}, false, nil
	}
	identity, found, err := h.config.State.Identity(ctx)
	if err != nil {
		return computerstate.Identity{}, false, fmt.Errorf("read computer identity: %w", err)
	}
	if found && identity.ServerURL != h.config.ServerURL {
		return computerstate.Identity{}, false, errors.New("computer identity belongs to a different Server")
	}
	return identity, found, nil
}

func (h *Host) provisionAndAcknowledge(ctx context.Context, assignment *placementv1.AgentPlacement, registrationKey string) error {
	_, provisionError := workspace.Provision(h.config.DataRoot, assignment.GetAgentId())
	result := placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_READY
	errorCode := ""
	if provisionError != nil {
		result = placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED
		errorCode = workspace.ErrorCode(provisionError)
	}
	_, acknowledgementError := h.placements.AcknowledgeAgentPlacement(ctx, connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId:      assignment.GetComputerId(),
		RegistrationKey: registrationKey,
		AgentId:         assignment.GetAgentId(),
		DesiredRevision: assignment.GetDesiredRevision(),
		Result:          result,
		ErrorCode:       errorCode,
	}))
	if acknowledgementError != nil {
		return fmt.Errorf("acknowledge agent %s desired revision %d: %w", assignment.GetAgentId(), assignment.GetDesiredRevision(), acknowledgementError)
	}
	if provisionError != nil {
		return provisionError
	}
	return nil
}
