package driver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const ContractVersion = "sumi.run.v1"

const (
	KindNative Kind = "native"
	KindCodex  Kind = "codex"
	KindClaude Kind = "claude"
)

type Kind string

func (k Kind) Valid() bool {
	return k == KindNative || k == KindCodex || k == KindClaude
}

type Capability struct {
	Streaming bool `json:"streaming"`
	Tools     bool `json:"tools"`
	Resume    bool `json:"resume"`
	Cancel    bool `json:"cancel"`
	Steering  bool `json:"steering"`
}

func Capabilities(k Kind) (Capability, error) {
	switch k {
	case KindNative:
		return Capability{Streaming: true, Tools: true, Resume: true, Cancel: true, Steering: true}, nil
	case KindCodex, KindClaude:
		return Capability{Streaming: true, Tools: true, Cancel: true}, nil
	default:
		return Capability{}, fmt.Errorf("unsupported driver %q", k)
	}
}

type Target struct {
	SpaceID      string `json:"space_id"`
	ThreadID     string `json:"thread_id,omitempty"`
	HeadSequence uint64 `json:"head_sequence"`
}

type Source struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type RunInput struct {
	AgentID      string     `json:"agent_id"`
	ComputerID   string     `json:"computer_id"`
	Generation   uint64     `json:"placement_generation"`
	DeliveryID   string     `json:"delivery_id"`
	RunID        string     `json:"run_id"`
	LaunchID     string     `json:"launch_id"`
	Fence        uint64     `json:"fence"`
	Workspace    string     `json:"workspace"`
	Capabilities Capability `json:"capabilities"`
	Target       Target     `json:"target"`
	WorkID       string     `json:"work_id,omitempty"`
	WorkGoal     string     `json:"work_goal,omitempty"`
	MemoryIndex  []string   `json:"memory_index,omitempty"`
	Sources      []Source   `json:"sources,omitempty"`
	CurrentInput string     `json:"current_input"`
	HostPolicy   string     `json:"host_policy"`
}

type Section struct {
	Name   string `json:"name"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"`
}

type Prompt struct {
	Version  string    `json:"version"`
	Sections []Section `json:"sections"`
	Target   Target    `json:"target"`
	Run      RunRef    `json:"run"`
}

type RunRef struct {
	AgentID      string     `json:"agent_id"`
	ComputerID   string     `json:"computer_id"`
	Generation   uint64     `json:"placement_generation"`
	DeliveryID   string     `json:"delivery_id"`
	RunID        string     `json:"run_id"`
	LaunchID     string     `json:"launch_id"`
	Fence        uint64     `json:"fence"`
	Workspace    string     `json:"workspace"`
	Capabilities Capability `json:"capabilities"`
}

func (input RunInput) Prompt() (Prompt, error) {
	if err := input.validate(); err != nil {
		return Prompt{}, err
	}
	sections := []Section{
		{Name: "host_policy", Text: input.HostPolicy, Source: "host"},
		{Name: "agent_identity", Text: input.AgentID, Source: "server"},
		{Name: "placement", Text: placementText(input), Source: "server"},
		{Name: "capabilities", Text: capabilityText(input.Capabilities), Source: "host"},
	}
	if input.WorkID != "" {
		sections = append(sections, Section{Name: "work_context", Text: input.WorkGoal, Source: input.WorkID})
	}
	sections = append(sections, Section{Name: "target_context", Text: targetText(input.Target), Source: input.Target.SpaceID})
	if len(input.MemoryIndex) > 0 {
		sections = append(sections, Section{Name: "memory_index", Text: strings.Join(input.MemoryIndex, "\n"), Source: "agent-home"})
	}
	for _, source := range input.Sources {
		sections = append(sections, Section{Name: "retrieved_source", Text: source.Text, Source: source.ID})
	}
	sections = append(sections, Section{Name: "current_input", Text: input.CurrentInput, Source: "trigger"})
	return Prompt{Version: ContractVersion, Sections: sections, Target: input.Target, Run: RunRef{
		AgentID: input.AgentID, ComputerID: input.ComputerID, Generation: input.Generation,
		DeliveryID: input.DeliveryID, RunID: input.RunID, LaunchID: input.LaunchID,
		Fence: input.Fence, Workspace: input.Workspace, Capabilities: input.Capabilities,
	}}, nil
}

func (input RunInput) validate() error {
	if err := required("agent id", input.AgentID); err != nil {
		return err
	}
	if err := required("computer id", input.ComputerID); err != nil {
		return err
	}
	if err := required("workspace", input.Workspace); err != nil {
		return err
	}
	if err := required("host policy", input.HostPolicy); err != nil {
		return err
	}
	if err := required("space id", input.Target.SpaceID); err != nil {
		return err
	}
	if err := required("current input", input.CurrentInput); err != nil {
		return err
	}
	if input.Generation == 0 {
		return errors.New("placement generation is required")
	}
	if err := required("delivery id", input.DeliveryID); err != nil {
		return err
	}
	if err := required("run id", input.RunID); err != nil {
		return err
	}
	if err := required("launch id", input.LaunchID); err != nil {
		return err
	}
	if input.Fence == 0 {
		return errors.New("run fence is required")
	}
	if input.Target.HeadSequence == 0 {
		return errors.New("target head sequence is required")
	}
	if !input.Capabilities.Valid() {
		return errors.New("driver capabilities are invalid")
	}
	if utf8.RuneCountInString(input.CurrentInput) > 400_000 {
		return errors.New("current input is too large")
	}
	for _, source := range input.Sources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Kind) == "" {
			return errors.New("retrieved source identity is required")
		}
	}
	if input.WorkID != "" && strings.TrimSpace(input.WorkGoal) == "" {
		return errors.New("work goal is required")
	}
	return nil
}

func required(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func (c Capability) Valid() bool {
	return c.Streaming || c.Tools || c.Resume || c.Cancel || c.Steering
}

func capabilityText(value Capability) string {
	return fmt.Sprintf("streaming=%t tools=%t resume=%t cancel=%t steering=%t", value.Streaming, value.Tools, value.Resume, value.Cancel, value.Steering)
}

func targetText(value Target) string {
	if value.ThreadID == "" {
		return fmt.Sprintf("space=%s head=%d", value.SpaceID, value.HeadSequence)
	}
	return fmt.Sprintf("space=%s thread=%s head=%d", value.SpaceID, value.ThreadID, value.HeadSequence)
}

func placementText(input RunInput) string {
	return fmt.Sprintf("computer=%s generation=%d workspace=%s", input.ComputerID, input.Generation, input.Workspace)
}

type CommandKind string

const (
	CommandPrompt CommandKind = "prompt"
	CommandSteer  CommandKind = "steer"
	CommandSpawn  CommandKind = "spawn"
	CommandFork   CommandKind = "fork"
)

type Command struct {
	Kind      CommandKind `json:"kind"`
	Input     *RunInput   `json:"input,omitempty"`
	Text      string      `json:"text,omitempty"`
	ChildName string      `json:"child_name,omitempty"`
}

func (c Command) validate() error {
	switch c.Kind {
	case CommandPrompt:
		if c.Input == nil {
			return errors.New("prompt input is required")
		}
		return c.Input.validate()
	case CommandSteer:
		if strings.TrimSpace(c.Text) == "" {
			return errors.New("steering text is required")
		}
		return nil
	case CommandSpawn, CommandFork:
		if strings.TrimSpace(c.ChildName) == "" {
			return errors.New("child name is required")
		}
		return nil
	default:
		return fmt.Errorf("unsupported command %q", c.Kind)
	}
}

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
)

type UsageSource string

const (
	UsageProviderReported UsageSource = "provider_reported"
	UsageEstimated        UsageSource = "estimated"
	UsageUnavailable      UsageSource = "unavailable"
)

type Usage struct {
	InputTokens  *uint64     `json:"input_tokens,omitempty"`
	CachedInput  *uint64     `json:"cached_input_tokens,omitempty"`
	CacheWritten *uint64     `json:"cache_written_tokens,omitempty"`
	OutputTokens *uint64     `json:"output_tokens,omitempty"`
	Reasoning    *uint64     `json:"reasoning_tokens,omitempty"`
	TotalTokens  *uint64     `json:"total_tokens,omitempty"`
	Source       UsageSource `json:"source"`
}

type ModelCall struct {
	CallID   string `json:"call_id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Attempt  uint32 `json:"attempt"`
	Kind     string `json:"kind"`
	Usage    *Usage `json:"usage,omitempty"`
}

type TurnResult struct {
	Outcome           Outcome     `json:"outcome"`
	Body              string      `json:"body"`
	MentionedAgentIDs []string    `json:"mentioned_agent_ids,omitempty"`
	Calls             []ModelCall `json:"calls,omitempty"`
}

func (r TurnResult) validate() error {
	if r.Outcome != OutcomeSucceeded && r.Outcome != OutcomeFailed {
		return errors.New("turn outcome is invalid")
	}
	return nil
}

type EventKind string

const (
	EventStarted    EventKind = "started"
	EventDelta      EventKind = "delta"
	EventToolCall   EventKind = "tool_call"
	EventToolResult EventKind = "tool_result"
	EventDiagnostic EventKind = "diagnostic"
)

type Event struct {
	Sequence uint64     `json:"sequence"`
	Kind     EventKind  `json:"kind"`
	Text     string     `json:"text,omitempty"`
	Tool     string     `json:"tool,omitempty"`
	Call     *ModelCall `json:"call,omitempty"`
}

func (e Event) validate() error {
	if e.Kind == "" {
		return errors.New("event kind is required")
	}
	if e.Kind == EventToolCall && strings.TrimSpace(e.Tool) == "" {
		return errors.New("tool call name is required")
	}
	return nil
}

type EventSink interface {
	Publish(Event) error
}

type Engine interface {
	Execute(context.Context, Command, EventSink) (TurnResult, error)
}
