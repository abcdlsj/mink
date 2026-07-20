package computerhost

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
	"github.com/abcdlsj/sumi/internal/workspace"
)

type Config struct {
	ServerURL       string
	DataRoot        string
	RegistrationKey string
	Name            string
	OS              computerv1.OperatingSystem
	Arch            computerv1.Architecture
	HTTPClient      *http.Client
}

type Host struct {
	config     Config
	computers  computerv1connect.ComputerServiceClient
	placements placementv1connect.PlacementServiceClient
}

type SyncResult struct {
	ComputerID  string
	Assignments int
	Active      int
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
	registered, err := h.computers.RegisterComputer(ctx, connect.NewRequest(&computerv1.RegisterComputerRequest{
		RegistrationKey: h.config.RegistrationKey,
		Name:            h.config.Name,
		Os:              h.config.OS,
		Arch:            h.config.Arch,
	}))
	if err != nil {
		return SyncResult{}, fmt.Errorf("register computer: %w", err)
	}
	computerID := registered.Msg.GetComputer().GetId()
	assignments, err := h.placements.ListComputerAssignments(ctx, connect.NewRequest(&placementv1.ListComputerAssignmentsRequest{
		ComputerId:      computerID,
		RegistrationKey: h.config.RegistrationKey,
	}))
	if err != nil {
		return SyncResult{ComputerID: computerID}, fmt.Errorf("list assignments: %w", err)
	}

	result := SyncResult{ComputerID: computerID, Assignments: len(assignments.Msg.GetAssignments())}
	var syncErrors []error
	for _, assignment := range assignments.Msg.GetAssignments() {
		provisionError := h.provisionAndAcknowledge(ctx, assignment)
		if provisionError == nil {
			result.Active++
			continue
		}
		result.Failed++
		syncErrors = append(syncErrors, provisionError)
	}
	return result, errors.Join(syncErrors...)
}

func (h *Host) provisionAndAcknowledge(ctx context.Context, assignment *placementv1.AgentPlacement) error {
	_, provisionError := workspace.Provision(h.config.DataRoot, assignment.GetAgentId())
	result := placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_ACTIVE
	errorCode := ""
	if provisionError != nil {
		result = placementv1.AcknowledgementResult_ACKNOWLEDGEMENT_RESULT_FAILED
		errorCode = workspace.ErrorCode(provisionError)
	}
	_, acknowledgementError := h.placements.AcknowledgeAgentPlacement(ctx, connect.NewRequest(&placementv1.AcknowledgeAgentPlacementRequest{
		ComputerId:      assignment.GetComputerId(),
		RegistrationKey: h.config.RegistrationKey,
		AgentId:         assignment.GetAgentId(),
		Generation:      assignment.GetGeneration(),
		Result:          result,
		ErrorCode:       errorCode,
	}))
	if acknowledgementError != nil {
		return fmt.Errorf("acknowledge agent %s generation %d: %w", assignment.GetAgentId(), assignment.GetGeneration(), acknowledgementError)
	}
	if provisionError != nil {
		return provisionError
	}
	return nil
}
