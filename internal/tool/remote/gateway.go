package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	artifactv1 "github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1/artifactv1connect"
	knowledgev1 "github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1/knowledgev1connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/work/v1/workv1connect"
	computerstate "github.com/abcdlsj/sumi/internal/computer/state"
	"github.com/abcdlsj/sumi/internal/provider"
	"github.com/abcdlsj/sumi/internal/tool"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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

func (remote *client) messageDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_message_send",
		Description: "Send a message to this Run's exact Space or Thread target after current runtime, Run, Grant, membership, and mention checks.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"body":{"type":"string","minLength":1,"maxLength":400000},"mentioned_agent_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":100}},"required":["body"],"additionalProperties":false}`),
		Capability:  "message.send",
		Scope:       "run_target",
		Validate:    validateSendMessage,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments sendMessageArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			current, err := remote.currentRun(ctx, run)
			if err != nil {
				return nil, err
			}
			mentions := make([]*spacev1.Principal, 0, len(arguments.MentionedAgentIDs))
			for _, agentID := range arguments.MentionedAgentIDs {
				mentions = append(mentions, &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agentID})
			}
			request, err := runtimeRequest(ctx, remote, run, &spacev1.SendMessageRequest{
				RequestId: toolRequestID(run, call), Target: proto.Clone(current.GetTarget()).(*spacev1.MessageTarget),
				Body: arguments.Body, MentionedPrincipals: mentions,
				RunProof: &grantv1.RunProof{RunId: run.RunID, Attempt: run.Attempt, Fence: run.Fence},
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.messages.SendMessage(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) workGetDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_work_get",
		Description: "Read one currently visible Work with constraints, acceptance criteria, assignments, approvals, and events.",
		Schema:      idSchema("work_id"),
		Capability:  "work.read",
		Scope:       "work",
		Validate:    validateWorkID,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments workIDArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &workv1.GetWorkRequest{WorkId: arguments.WorkID})
			if err != nil {
				return nil, err
			}
			response, err := remote.works.GetWork(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) workCreateDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_work_create",
		Description: "Create a Work from this Run's exact trigger after current runtime, Run, Grant, and source checks.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"parent_work_id":{"type":"string","format":"uuid"},"goal":{"type":"string","minLength":1,"maxLength":20000},"constraints":{"type":"array","maxItems":100,"items":{"type":"string","minLength":1,"maxLength":20000}},"acceptance_criteria":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string","minLength":1,"maxLength":20000}}},"required":["goal","constraints","acceptance_criteria"],"additionalProperties":false}`),
		Capability:  "work.create",
		Scope:       "organization",
		Validate:    validateWorkCreate,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments workCreateArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			current, err := remote.currentRun(ctx, run)
			if err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &workv1.CreateWorkRequest{
				RequestId: toolRequestID(run, call), ParentWorkId: arguments.ParentWorkID,
				SourceMessageId: current.GetTriggerMessageId(), SourceSpaceId: current.GetSpaceId(),
				SourceTarget:         proto.Clone(current.GetTarget()).(*spacev1.MessageTarget),
				SourceTargetSequence: current.GetTriggerTargetSequence(), Goal: arguments.Goal,
				Constraints: arguments.Constraints, AcceptanceCriteria: arguments.AcceptanceCriteria,
				RunProof: &grantv1.RunProof{RunId: run.RunID, Attempt: run.Attempt, Fence: run.Fence},
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.works.CreateWork(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) workAssignDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_work_assign",
		Description: "Assign an existing ready Agent to a Work after current runtime, Run, Grant, and Work checks.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"work_id":{"type":"string","format":"uuid"},"agent_id":{"type":"string","format":"uuid"},"role":{"type":"string","enum":["coordinator","contributor"]}},"required":["work_id","agent_id","role"],"additionalProperties":false}`),
		Capability:  "work.manage",
		Scope:       "work",
		Validate:    validateWorkAssign,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments workAssignArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &workv1.AssignWorkRequest{
				RequestId: toolRequestID(run, call), WorkId: arguments.WorkID,
				AgentId: arguments.AgentID, Role: assignmentRole(arguments.Role),
				RunProof: &grantv1.RunProof{RunId: run.RunID, Attempt: run.Attempt, Fence: run.Fence},
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.works.AssignWork(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) workTransitionDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_work_transition",
		Description: "Transition a Work after current runtime, Run, Grant, assignment, state, approval, child, and acceptance checks.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"work_id":{"type":"string","format":"uuid"},"to_state":{"type":"string","enum":["open","blocked","completed","failed","cancelled"]},"reason":{"type":"string","maxLength":20000},"result":{"type":"string","maxLength":400000},"criterion_results":{"type":"array","maxItems":100,"items":{"type":"object","properties":{"criterion_id":{"type":"string","format":"uuid"},"verdict":{"type":"string","enum":["passed","failed"]},"evidence":{"type":"string","maxLength":20000}},"required":["criterion_id","verdict","evidence"],"additionalProperties":false}}},"required":["work_id","to_state"],"additionalProperties":false}`),
		Capability:  "work.manage",
		Scope:       "work",
		Validate:    validateWorkTransition,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments workTransitionArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			results := make([]*workv1.WorkCriterionResultInput, 0, len(arguments.CriterionResults))
			for _, result := range arguments.CriterionResults {
				results = append(results, &workv1.WorkCriterionResultInput{
					CriterionId: result.CriterionID, Verdict: criterionVerdict(result.Verdict), Evidence: result.Evidence,
				})
			}
			request, err := runtimeRequest(ctx, remote, run, &workv1.TransitionWorkRequest{
				RequestId: toolRequestID(run, call), WorkId: arguments.WorkID, ToState: workState(arguments.ToState),
				Reason: arguments.Reason, Result: arguments.Result, CriterionResults: results,
				RunProof: &grantv1.RunProof{RunId: run.RunID, Attempt: run.Attempt, Fence: run.Fence},
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.works.TransitionWork(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) workApprovalDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_work_request_approval",
		Description: "Request an explicit Human approval on a Work after current runtime, Run, Grant, and Work checks.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"work_id":{"type":"string","format":"uuid"},"question":{"type":"string","minLength":1,"maxLength":20000}},"required":["work_id","question"],"additionalProperties":false}`),
		Capability:  "work.manage",
		Scope:       "work",
		Validate:    validateWorkApproval,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments workApprovalArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &workv1.RequestApprovalRequest{
				RequestId: toolRequestID(run, call), WorkId: arguments.WorkID, Question: arguments.Question,
				RunProof: &grantv1.RunProof{RunId: run.RunID, Attempt: run.Attempt, Fence: run.Fence},
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.works.RequestApproval(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) artifactGetDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_artifact_get",
		Description: "Read one currently visible Artifact version and its bounded metadata after current runtime and ACL checks.",
		Schema:      artifactReferenceSchema(),
		Capability:  "artifact.read",
		Scope:       "artifact",
		Validate:    validateArtifactReference,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments artifactReferenceArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &artifactv1.GetArtifactRequest{
				ArtifactId: arguments.ArtifactID, Version: arguments.Version,
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.artifacts.GetArtifact(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) artifactListDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_artifact_list",
		Description: "List currently visible Artifact metadata, optionally restricted to one Work.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"owning_work_id":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["limit"],"additionalProperties":false}`),
		Capability:  "artifact.read",
		Scope:       "artifact",
		Validate:    validateArtifactList,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments artifactListArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &artifactv1.ListArtifactsRequest{
				OwningWorkId: arguments.OwningWorkID, Limit: arguments.Limit,
			})
			if err != nil {
				return nil, err
			}
			response, err := remote.artifacts.ListArtifacts(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) artifactPublishDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_artifact_publish",
		Description: "Publish a bounded Artifact version with this Run's exact execution proof and trigger provenance.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"artifact_id":{"type":"string","format":"uuid"},"owning_work_id":{"type":"string","format":"uuid"},"name":{"type":"string","minLength":1,"maxLength":255},"media_type":{"type":"string","minLength":1,"maxLength":255},"summary":{"type":"string","maxLength":20000},"content_base64":{"type":"string","minLength":1,"maxLength":699052}},"required":["owning_work_id","name","media_type","summary","content_base64"],"additionalProperties":false}`),
		Capability:  "artifact.manage",
		Scope:       "work",
		Validate:    validateArtifactPublish,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments artifactPublishArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			content, err := base64.StdEncoding.DecodeString(arguments.ContentBase64)
			if err != nil || len(content) == 0 || len(content) > maximumArtifactBytes {
				return nil, errors.New("artifact content is invalid")
			}
			current, err := remote.currentRun(ctx, run)
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(content)
			metadata := &artifactv1.PublishArtifactMetadata{
				RequestId: toolRequestID(run, call), ArtifactId: arguments.ArtifactID,
				OwningWorkId: arguments.OwningWorkID, Name: arguments.Name,
				MediaType: arguments.MediaType, Summary: arguments.Summary,
				Execution:    &artifactv1.ArtifactExecutionInput{RunId: run.RunID, Attempt: run.Attempt, Fence: run.Fence},
				Sources:      []*artifactv1.ArtifactSourceInput{{Source: &artifactv1.ArtifactSourceInput_MessageId{MessageId: current.GetTriggerMessageId()}}},
				DeclaredSize: int64(len(content)), DeclaredDigest: digest[:],
			}
			authorization, err := runtimeAuthorization(ctx, remote, run, &artifactv1.PublishArtifactRequest{})
			if err != nil {
				return nil, err
			}
			stream := remote.artifacts.PublishArtifact(ctx)
			stream.RequestHeader().Set("Authorization", authorization)
			if err := stream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Metadata{Metadata: metadata}}); err != nil {
				return nil, err
			}
			for offset := 0; offset < len(content); offset += 64 * 1024 {
				end := min(offset+64*1024, len(content))
				if err := stream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Chunk{Chunk: content[offset:end]}}); err != nil {
					return nil, err
				}
			}
			response, err := stream.CloseAndReceive()
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

func (remote *client) artifactFetchDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_artifact_fetch",
		Description: "Fetch one currently visible Artifact version up to 512 KiB and return its content as base64.",
		Schema:      artifactReferenceSchema(),
		Capability:  "artifact.read",
		Scope:       "artifact",
		Validate:    validateArtifactReference,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments artifactReferenceArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &artifactv1.FetchArtifactRequest{
				ArtifactId: arguments.ArtifactID, Version: arguments.Version,
			})
			if err != nil {
				return nil, err
			}
			stream, err := remote.artifacts.FetchArtifact(ctx, request)
			if err != nil {
				return nil, err
			}
			var view *artifactv1.ArtifactView
			content := make([]byte, 0)
			for stream.Receive() {
				switch payload := stream.Msg().GetPayload().(type) {
				case *artifactv1.FetchArtifactResponse_Metadata:
					if view != nil || payload.Metadata == nil || payload.Metadata.GetView() == nil {
						return nil, errors.New("artifact fetch metadata is invalid")
					}
					view = payload.Metadata.GetView()
				case *artifactv1.FetchArtifactResponse_Chunk:
					if view == nil || len(payload.Chunk) == 0 || len(content)+len(payload.Chunk) > maximumArtifactBytes {
						return nil, errors.New("artifact fetch exceeds the tool result bound")
					}
					content = append(content, payload.Chunk...)
				default:
					return nil, errors.New("artifact fetch frame is invalid")
				}
			}
			if err := stream.Err(); err != nil {
				return nil, err
			}
			if view == nil {
				return nil, errors.New("artifact fetch metadata is missing")
			}
			viewJSON, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(view)
			if err != nil {
				return nil, err
			}
			return json.Marshal(struct {
				View          json.RawMessage `json:"view"`
				ContentBase64 string          `json:"content_base64"`
			}{View: viewJSON, ContentBase64: base64.StdEncoding.EncodeToString(content)})
		},
	}
}

func (remote *client) knowledgeDefinition() tool.Definition {
	return tool.Definition{
		Name:        "sumi_knowledge_search",
		Description: "Search the rebuildable Knowledge projection; every Message, Work, and Artifact citation is rechecked against current ACL facts.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":2000},"limit":{"type":"integer","minimum":1,"maximum":20}},"required":["query","limit"],"additionalProperties":false}`),
		Capability:  "knowledge.search",
		Scope:       "organization",
		Validate:    validateKnowledgeSearch,
		Execute: func(ctx context.Context, run tool.RunContext, call provider.ToolCall) (json.RawMessage, error) {
			var arguments knowledgeArguments
			if err := decode(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			request, err := runtimeRequest(ctx, remote, run, &knowledgev1.SearchKnowledgeRequest{Query: arguments.Query, Limit: arguments.Limit})
			if err != nil {
				return nil, err
			}
			response, err := remote.knowledge.SearchKnowledge(ctx, request)
			if err != nil {
				return nil, err
			}
			return marshal(response.Msg)
		},
	}
}

type sendMessageArguments struct {
	Body              string   `json:"body"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
}

type workIDArguments struct {
	WorkID string `json:"work_id"`
}

type workCreateArguments struct {
	ParentWorkID       string   `json:"parent_work_id"`
	Goal               string   `json:"goal"`
	Constraints        []string `json:"constraints"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

type workAssignArguments struct {
	WorkID  string `json:"work_id"`
	AgentID string `json:"agent_id"`
	Role    string `json:"role"`
}

type criterionResultArguments struct {
	CriterionID string `json:"criterion_id"`
	Verdict     string `json:"verdict"`
	Evidence    string `json:"evidence"`
}

type workTransitionArguments struct {
	WorkID           string                     `json:"work_id"`
	ToState          string                     `json:"to_state"`
	Reason           string                     `json:"reason"`
	Result           string                     `json:"result"`
	CriterionResults []criterionResultArguments `json:"criterion_results"`
}

type workApprovalArguments struct {
	WorkID   string `json:"work_id"`
	Question string `json:"question"`
}

type knowledgeArguments struct {
	Query string `json:"query"`
	Limit uint32 `json:"limit"`
}

type artifactReferenceArguments struct {
	ArtifactID string `json:"artifact_id"`
	Version    uint64 `json:"version"`
}

type artifactListArguments struct {
	OwningWorkID string `json:"owning_work_id"`
	Limit        uint32 `json:"limit"`
}

type artifactPublishArguments struct {
	ArtifactID    string `json:"artifact_id"`
	OwningWorkID  string `json:"owning_work_id"`
	Name          string `json:"name"`
	MediaType     string `json:"media_type"`
	Summary       string `json:"summary"`
	ContentBase64 string `json:"content_base64"`
}

func validateSendMessage(raw json.RawMessage) error {
	var arguments sendMessageArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if textInvalid(arguments.Body, maximumMessageRunes, false) || len(arguments.MentionedAgentIDs) > 100 {
		return errors.New("message arguments are invalid")
	}
	seen := make(map[string]struct{}, len(arguments.MentionedAgentIDs))
	for _, agentID := range arguments.MentionedAgentIDs {
		if !canonicalID(agentID) {
			return errors.New("mentioned Agent ID is invalid")
		}
		if _, duplicate := seen[agentID]; duplicate {
			return errors.New("mentioned Agent ID is duplicated")
		}
		seen[agentID] = struct{}{}
	}
	return nil
}

func validateWorkID(raw json.RawMessage) error {
	var arguments workIDArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) {
		return errors.New("work ID is invalid")
	}
	return nil
}

func validateWorkCreate(raw json.RawMessage) error {
	var arguments workCreateArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if (arguments.ParentWorkID != "" && !canonicalID(arguments.ParentWorkID)) ||
		textInvalid(arguments.Goal, 20_000, false) || len(arguments.Constraints) > 100 ||
		len(arguments.AcceptanceCriteria) == 0 || len(arguments.AcceptanceCriteria) > 100 {
		return errors.New("work create arguments are invalid")
	}
	for _, value := range append(append([]string(nil), arguments.Constraints...), arguments.AcceptanceCriteria...) {
		if textInvalid(value, 20_000, false) {
			return errors.New("work text is invalid")
		}
	}
	return nil
}

func validateWorkAssign(raw json.RawMessage) error {
	var arguments workAssignArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) || !canonicalID(arguments.AgentID) || assignmentRole(arguments.Role) == workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_UNSPECIFIED {
		return errors.New("work assignment arguments are invalid")
	}
	return nil
}

func validateWorkTransition(raw json.RawMessage) error {
	var arguments workTransitionArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) || workState(arguments.ToState) == workv1.WorkState_WORK_STATE_UNSPECIFIED ||
		textInvalid(arguments.Reason, 20_000, true) || textInvalid(arguments.Result, maximumWorkRunes, true) || len(arguments.CriterionResults) > 100 {
		return errors.New("work transition arguments are invalid")
	}
	for _, result := range arguments.CriterionResults {
		if !canonicalID(result.CriterionID) || criterionVerdict(result.Verdict) == workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_UNSPECIFIED || textInvalid(result.Evidence, 20_000, true) {
			return errors.New("work criterion result is invalid")
		}
	}
	return nil
}

func validateWorkApproval(raw json.RawMessage) error {
	var arguments workApprovalArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.WorkID) || textInvalid(arguments.Question, 20_000, false) {
		return errors.New("work approval arguments are invalid")
	}
	return nil
}

func validateKnowledgeSearch(raw json.RawMessage) error {
	var arguments knowledgeArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if textInvalid(arguments.Query, maximumSearchRunes, false) || arguments.Limit == 0 || arguments.Limit > 20 {
		return errors.New("knowledge search arguments are invalid")
	}
	return nil
}

func validateArtifactReference(raw json.RawMessage) error {
	var arguments artifactReferenceArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if !canonicalID(arguments.ArtifactID) {
		return errors.New("artifact reference is invalid")
	}
	return nil
}

func validateArtifactList(raw json.RawMessage) error {
	var arguments artifactListArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if (arguments.OwningWorkID != "" && !canonicalID(arguments.OwningWorkID)) || arguments.Limit == 0 || arguments.Limit > 20 {
		return errors.New("artifact list arguments are invalid")
	}
	return nil
}

func validateArtifactPublish(raw json.RawMessage) error {
	var arguments artifactPublishArguments
	if err := decode(raw, &arguments); err != nil {
		return err
	}
	if (arguments.ArtifactID != "" && !canonicalID(arguments.ArtifactID)) || !canonicalID(arguments.OwningWorkID) ||
		textInvalid(arguments.Name, 255, false) || textInvalid(arguments.MediaType, 255, false) ||
		textInvalid(arguments.Summary, 20_000, true) || arguments.ContentBase64 == "" || len(arguments.ContentBase64) > 699_052 {
		return errors.New("artifact publish arguments are invalid")
	}
	content, err := base64.StdEncoding.DecodeString(arguments.ContentBase64)
	if err != nil || len(content) == 0 || len(content) > maximumArtifactBytes {
		return errors.New("artifact content is invalid")
	}
	return nil
}

func decode(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("tool arguments contain trailing data")
	}
	return nil
}

func textInvalid(value string, maximum int, optional bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return true
	}
	return !optional && strings.TrimSpace(value) == ""
}

func canonicalID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

func toolRequestID(run tool.RunContext, call provider.ToolCall) string {
	payload := fmt.Sprintf("sumi.tool.v1\x00%s\x00%d\x00%d\x00%s\x00%s", run.RunID, run.Attempt, run.Fence, call.Name, call.ID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(payload)).String()
}

func marshal(message proto.Message) (json.RawMessage, error) {
	payload, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	return payload, nil
}

func idSchema(field string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"%s":{"type":"string","format":"uuid"}},"required":["%s"],"additionalProperties":false}`, field, field))
}

func artifactReferenceSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"artifact_id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":0}},"required":["artifact_id","version"],"additionalProperties":false}`)
}

func workState(value string) workv1.WorkState {
	switch value {
	case "open":
		return workv1.WorkState_WORK_STATE_OPEN
	case "blocked":
		return workv1.WorkState_WORK_STATE_BLOCKED
	case "completed":
		return workv1.WorkState_WORK_STATE_COMPLETED
	case "failed":
		return workv1.WorkState_WORK_STATE_FAILED
	case "cancelled":
		return workv1.WorkState_WORK_STATE_CANCELLED
	default:
		return workv1.WorkState_WORK_STATE_UNSPECIFIED
	}
}

func criterionVerdict(value string) workv1.WorkCriterionVerdict {
	switch value {
	case "passed":
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_PASSED
	case "failed":
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_FAILED
	default:
		return workv1.WorkCriterionVerdict_WORK_CRITERION_VERDICT_UNSPECIFIED
	}
}

func assignmentRole(value string) workv1.WorkAssignmentRole {
	switch value {
	case "coordinator":
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_COORDINATOR
	case "contributor":
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_CONTRIBUTOR
	default:
		return workv1.WorkAssignmentRole_WORK_ASSIGNMENT_ROLE_UNSPECIFIED
	}
}

var _ tool.Authorizer = (*client)(nil)
