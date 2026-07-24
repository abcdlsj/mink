package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1/artifactv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1/knowledgev1connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/work/v1/workv1connect"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/tool"
)

const (
	maximumMessageRunes  = 400_000
	maximumWorkRunes     = 400_000
	maximumSearchRunes   = 2_000
	maximumArtifactBytes = 512 * 1024
)

type Config struct {
	ServerURL                string
	HTTPClient               *http.Client
	State                    *computerstate.State
	AgentID                  string
	ComputerID               string
	PlacementDesiredRevision uint64
	ToolPolicy               *agentv1.RuntimeToolPolicy
	Timeout                  time.Duration
	MaxCallsPerRun           uint32
	MaxArgumentBytes         int
	MaxResultBytes           int
}

type client struct {
	config    Config
	runs      runv1connect.RunServiceClient
	messages  spacev1connect.CollaborationServiceClient
	works     workv1connect.WorkServiceClient
	artifacts artifactv1connect.ArtifactServiceClient
	knowledge knowledgev1connect.KnowledgeServiceClient
}

func NewGateway(config Config) (*tool.Gateway, error) {
	if config.ServerURL == "" || config.State == nil || config.AgentID == "" || config.ComputerID == "" || config.PlacementDesiredRevision == 0 || config.ToolPolicy == nil {
		return nil, errors.New("remote tool gateway binding is incomplete")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	remote := &client{
		config:    config,
		runs:      runv1connect.NewRunServiceClient(httpClient, config.ServerURL),
		messages:  spacev1connect.NewCollaborationServiceClient(httpClient, config.ServerURL),
		works:     workv1connect.NewWorkServiceClient(httpClient, config.ServerURL),
		artifacts: artifactv1connect.NewArtifactServiceClient(httpClient, config.ServerURL),
		knowledge: knowledgev1connect.NewKnowledgeServiceClient(httpClient, config.ServerURL),
	}
	definitions := make([]tool.Definition, 0, 11)
	if config.ToolPolicy.GetMessage() {
		definitions = append(definitions, remote.messageDefinition())
	}
	if config.ToolPolicy.GetWork() {
		definitions = append(definitions,
			remote.workGetDefinition(), remote.workCreateDefinition(), remote.workAssignDefinition(),
			remote.workTransitionDefinition(), remote.workApprovalDefinition(),
		)
	}
	if config.ToolPolicy.GetArtifact() {
		definitions = append(definitions,
			remote.artifactGetDefinition(), remote.artifactListDefinition(),
			remote.artifactPublishDefinition(), remote.artifactFetchDefinition(),
		)
	}
	if config.ToolPolicy.GetKnowledge() {
		definitions = append(definitions, remote.knowledgeDefinition())
	}
	return tool.NewGateway(tool.Config{
		Authorizer:       remote,
		Store:            config.State,
		Definitions:      definitions,
		Timeout:          config.Timeout,
		MaxCallsPerRun:   config.MaxCallsPerRun,
		MaxArgumentBytes: config.MaxArgumentBytes,
		MaxResultBytes:   config.MaxResultBytes,
	})
}

func (remote *client) Authorize(ctx context.Context, run tool.RunContext, _, _ string) error {
	_, err := remote.currentRun(ctx, run)
	return err
}

func (remote *client) currentRun(ctx context.Context, run tool.RunContext) (*runv1.Run, error) {
	request, err := runtimeRequest(ctx, remote, run, &runv1.GetRunRequest{RunId: run.RunID})
	if err != nil {
		return nil, err
	}
	response, err := remote.runs.GetRun(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("read current run authorization: %w", err)
	}
	current := response.Msg.GetRun()
	if current == nil || current.GetState() != runv1.RunState_RUN_STATE_RUNNING ||
		current.GetAgentId() != run.AgentID || current.GetLeaseHolderComputerId() != run.ComputerID ||
		current.GetPlacementDesiredRevision() != run.PlacementDesiredRevision ||
		current.GetAttempt() != run.Attempt || current.GetFence() != run.Fence ||
		current.GetLeaseExpiresAt() == nil || !time.Now().Before(current.GetLeaseExpiresAt().AsTime()) {
		return nil, errors.New("run authorization is stale")
	}
	return current, nil
}

func runtimeRequest[Request any](ctx context.Context, remote *client, run tool.RunContext, message *Request) (*connect.Request[Request], error) {
	session, found, err := remote.config.State.RuntimeSession(ctx, run.AgentID)
	if err != nil {
		return nil, fmt.Errorf("read runtime session: %w", err)
	}
	if !found || session.AgentID != run.AgentID || session.ComputerID != run.ComputerID ||
		session.PlacementDesiredRevision != run.PlacementDesiredRevision || !time.Now().Before(session.ExpiresAt) {
		return nil, errors.New("runtime session is stale")
	}
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+session.Token)
	return request, nil
}

func runtimeAuthorization[Request any](ctx context.Context, remote *client, run tool.RunContext, message *Request) (string, error) {
	request, err := runtimeRequest(ctx, remote, run, message)
	if err != nil {
		return "", err
	}
	return request.Header().Get("Authorization"), nil
}
