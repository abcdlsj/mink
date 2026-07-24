package remote

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"

	artifactv1 "github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	knowledgev1 "github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	workv1 "github.com/abcdlsj/sumi/gen/go/sumi/work/v1"
	"github.com/abcdlsj/sumi/internal/provider"
	"github.com/abcdlsj/sumi/internal/tool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

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
