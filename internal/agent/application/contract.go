package application

import (
	"time"

	authoritydomain "github.com/abcdlsj/sumi/internal/authority/domain"
)

type Agent struct {
	ID        string
	Handle    string
	Profile   Profile
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Profile struct {
	AgentID      string
	Revision     uint64
	DisplayName  string
	Role         string
	Mission      string
	Instructions string
	CreatedAt    time.Time
}

type EngineKind string

const (
	EngineBuiltin       EngineKind = "builtin"
	EngineCodexAdapter  EngineKind = "codex_adapter"
	EngineClaudeAdapter EngineKind = "claude_adapter"
)

type ProviderProtocol string

const (
	ProviderOpenAIResponses   ProviderProtocol = "openai_responses"
	ProviderAnthropicMessages ProviderProtocol = "anthropic_messages"
)

type ToolPolicy struct {
	Message   bool
	Work      bool
	Artifact  bool
	Knowledge bool
}

type RuntimeSpec struct {
	AgentID                 string
	Revision                uint64
	Engine                  EngineKind
	ProviderProtocol        ProviderProtocol
	ProviderEndpoint        string
	Model                   string
	CredentialBindingHandle string
	SandboxProvider         string
	MaxRunDuration          time.Duration
	MaxOutputBytes          uint64
	ToolPolicy              ToolPolicy
	CreatedAt               time.Time
}

type CreateCommand struct {
	RequestID    string
	Actor        authoritydomain.Principal
	Handle       string
	DisplayName  string
	Role         string
	Mission      string
	Instructions string
	Now          time.Time
}

type UpdateProfileCommand struct {
	RequestID        string
	Actor            authoritydomain.Principal
	AgentID          string
	ExpectedRevision uint64
	DisplayName      string
	Role             string
	Mission          string
	Instructions     string
	Now              time.Time
}

type UpdateRuntimeSpecCommand struct {
	RequestID               string
	Actor                   authoritydomain.Principal
	AgentID                 string
	ExpectedRevision        uint64
	Engine                  EngineKind
	ProviderProtocol        ProviderProtocol
	ProviderEndpoint        string
	Model                   string
	CredentialBindingHandle string
	SandboxProvider         string
	MaxRunDuration          time.Duration
	MaxOutputBytes          uint64
	ToolPolicy              ToolPolicy
	Now                     time.Time
}
