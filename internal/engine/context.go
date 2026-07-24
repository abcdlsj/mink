package engine

import (
	"errors"

	agentv1 "github.com/abcdlsj/sumi/gen/go/sumi/agent/v1"
	computerruntime "github.com/abcdlsj/sumi/internal/computer/runtime"
)

type ContextAssembler struct {
	Profile    *agentv1.AgentProfile
	Runtime    *agentv1.AgentRuntimeSpec
	HostPolicy string
}

func (assembler ContextAssembler) Build(execution computerruntime.Execution) (RunInput, error) {
	if assembler.Profile == nil || assembler.Runtime == nil || assembler.Profile.GetAgentId() != execution.AgentID || assembler.Runtime.GetAgentId() != execution.AgentID {
		return RunInput{}, errors.New("engine context does not match the run agent")
	}
	toolKinds := make([]string, 0, 4)
	if policy := assembler.Runtime.GetToolPolicy(); policy != nil {
		if policy.GetMessage() {
			toolKinds = append(toolKinds, "message")
		}
		if policy.GetWork() {
			toolKinds = append(toolKinds, "work")
		}
		if policy.GetArtifact() {
			toolKinds = append(toolKinds, "artifact")
		}
		if policy.GetKnowledge() {
			toolKinds = append(toolKinds, "knowledge")
		}
	}
	messages := make([]Message, 0, len(execution.Messages))
	for _, message := range execution.Messages {
		messages = append(messages, Message{
			ID: message.ID, TargetSequence: message.TargetSequence, AuthorKind: message.AuthorKind,
			AuthorID: message.AuthorID, Body: message.Body,
		})
	}
	return RunInput{
		Run: RunRef{
			AgentID: execution.AgentID, ComputerID: execution.ComputerID,
			DesiredRevision: execution.PlacementDesiredRevision, RunID: execution.RunID,
			Attempt: execution.Attempt, Fence: execution.Fence,
		},
		Profile: Profile{
			Revision: assembler.Profile.GetRevision(), DisplayName: assembler.Profile.GetDisplayName(),
			Role: assembler.Profile.GetRole(), Mission: assembler.Profile.GetMission(), Instructions: assembler.Profile.GetInstructions(),
		},
		Runtime: Runtime{
			Engine: assembler.Runtime.GetEngine().String(), Provider: assembler.Runtime.GetProviderProtocol().String(),
			Endpoint: assembler.Runtime.GetProviderEndpoint(), Model: assembler.Runtime.GetModel(),
			Sandbox: assembler.Runtime.GetSandboxProvider().String(), EnabledToolKind: toolKinds,
		},
		Target: Target{
			SpaceID: execution.SpaceID, ThreadID: execution.ThreadRootMessageID,
			HeadSequence: execution.BasisTargetSequence,
		},
		Messages: messages, CurrentInput: execution.CurrentInput, HostPolicy: assembler.HostPolicy,
	}, nil
}
