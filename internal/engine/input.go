package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const ContractVersion = "sumi.run.v1"

const (
	maxSystemContractRunes = 4_000
	maxHostPolicyRunes     = 20_000
	maxProfileRunes        = 60_000
	maxWorkRunes           = 40_000
	maxMessages            = 200
	maxMessageRunes        = 20_000
	maxMessagesRunes       = 200_000
	maxMemoryEntries       = 100
	maxMemoryEntryRunes    = 2_000
	maxMemoryRunes         = 20_000
	maxSources             = 20
	maxSourceRunes         = 20_000
	maxSourcesRunes        = 100_000
	maxCurrentInputRunes   = 200_000
	maxContextRunes        = 512_000
)

type Profile struct {
	Revision     uint64 `json:"revision"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`
	Mission      string `json:"mission"`
	Instructions string `json:"instructions"`
}

type Runtime struct {
	Engine          string   `json:"engine"`
	Provider        string   `json:"provider,omitempty"`
	Endpoint        string   `json:"endpoint,omitempty"`
	Model           string   `json:"model,omitempty"`
	Sandbox         string   `json:"sandbox"`
	EnabledToolKind []string `json:"enabled_tool_kinds,omitempty"`
}

type Target struct {
	SpaceID      string `json:"space_id"`
	ThreadID     string `json:"thread_id,omitempty"`
	HeadSequence uint64 `json:"head_sequence"`
}

type Message struct {
	ID             string `json:"id"`
	TargetSequence uint64 `json:"target_sequence"`
	AuthorKind     string `json:"author_kind"`
	AuthorID       string `json:"author_id"`
	Body           string `json:"body"`
}

type Work struct {
	ID          string `json:"id"`
	Goal        string `json:"goal"`
	Constraints string `json:"constraints,omitempty"`
	Acceptance  string `json:"acceptance,omitempty"`
}

type Source struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Revision uint64 `json:"revision"`
	Text     string `json:"text"`
}

type RunRef struct {
	AgentID         string `json:"agent_id"`
	ComputerID      string `json:"computer_id"`
	DesiredRevision uint64 `json:"placement_desired_revision"`
	RunID           string `json:"run_id"`
	Attempt         uint64 `json:"attempt"`
	Fence           uint64 `json:"fence"`
}

type RunInput struct {
	Run          RunRef    `json:"run"`
	Profile      Profile   `json:"agent_profile"`
	Runtime      Runtime   `json:"runtime_spec"`
	Target       Target    `json:"target"`
	Work         *Work     `json:"work,omitempty"`
	Messages     []Message `json:"messages,omitempty"`
	MemoryIndex  []string  `json:"memory_index,omitempty"`
	Sources      []Source  `json:"retrieved_sources,omitempty"`
	CurrentInput string    `json:"current_input"`
	HostPolicy   string    `json:"host_policy"`
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

func (input RunInput) Prompt() (Prompt, error) {
	if err := input.Validate(); err != nil {
		return Prompt{}, err
	}
	sections := []Section{
		{Name: "system_contract", Text: SystemContract, Source: SystemContractVersion},
		{Name: "host_policy", Text: input.HostPolicy, Source: "computer"},
		jsonSection("agent_profile", input.Profile, fmt.Sprintf("agent:%s@%d", input.Run.AgentID, input.Profile.Revision)),
		jsonSection("runtime_spec", input.Runtime, fmt.Sprintf("placement:%d", input.Run.DesiredRevision)),
		jsonSection("target", input.Target, input.Target.SpaceID),
	}
	if input.Work != nil {
		sections = append(sections, jsonSection("work", input.Work, input.Work.ID))
	}
	if len(input.Messages) > 0 {
		sections = append(sections, jsonSection("recent_messages", input.Messages, input.Target.SpaceID))
	}
	if len(input.MemoryIndex) > 0 {
		sections = append(sections, jsonSection("memory_index", input.MemoryIndex, "agent-memory"))
	}
	for _, source := range input.Sources {
		sections = append(sections, jsonSection("retrieved_source", source, source.ID))
	}
	sections = append(sections, Section{Name: "current_input", Text: input.CurrentInput, Source: "trigger"})
	return Prompt{Version: ContractVersion, Sections: sections, Target: input.Target, Run: input.Run}, nil
}

func (input RunInput) Validate() error {
	total, err := requiredText("system contract", SystemContract, maxSystemContractRunes)
	if err != nil {
		return err
	}
	for name, value := range map[string]string{
		"agent id": input.Run.AgentID, "computer id": input.Run.ComputerID,
		"run id": input.Run.RunID, "space id": input.Target.SpaceID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if input.Run.DesiredRevision == 0 || input.Run.Attempt == 0 || input.Run.Fence == 0 || input.Target.HeadSequence == 0 || input.Profile.Revision == 0 {
		return errors.New("run revision, attempt, fence, target head, and profile revision are required")
	}
	for name, value := range map[string]string{
		"profile display name": input.Profile.DisplayName, "profile role": input.Profile.Role,
		"profile mission": input.Profile.Mission, "runtime engine": input.Runtime.Engine,
		"runtime sandbox": input.Runtime.Sandbox,
	} {
		count, err := requiredText(name, value, maxProfileRunes)
		if err != nil {
			return err
		}
		total += count
	}
	for name, value := range map[string]string{"profile instructions": input.Profile.Instructions, "runtime provider": input.Runtime.Provider, "runtime endpoint": input.Runtime.Endpoint, "runtime model": input.Runtime.Model} {
		count, err := optionalText(name, value, maxProfileRunes)
		if err != nil {
			return err
		}
		total += count
	}
	count, err := requiredText("host policy", input.HostPolicy, maxHostPolicyRunes)
	if err != nil {
		return err
	}
	total += count
	count, err = requiredText("current input", input.CurrentInput, maxCurrentInputRunes)
	if err != nil {
		return err
	}
	total += count
	if input.Work != nil {
		if strings.TrimSpace(input.Work.ID) == "" {
			return errors.New("work id is required")
		}
		for _, value := range []string{input.Work.Goal, input.Work.Constraints, input.Work.Acceptance} {
			count, err := optionalText("work context", value, maxWorkRunes)
			if err != nil {
				return err
			}
			total += count
		}
	}
	if len(input.Messages) > maxMessages {
		return errors.New("recent messages have too many entries")
	}
	messageTotal := 0
	for _, message := range input.Messages {
		if strings.TrimSpace(message.ID) == "" || strings.TrimSpace(message.AuthorKind) == "" || strings.TrimSpace(message.AuthorID) == "" || message.TargetSequence == 0 {
			return errors.New("recent message facts are incomplete")
		}
		count, err := requiredText("recent message body", message.Body, maxMessageRunes)
		if err != nil {
			return err
		}
		messageTotal += count
		if messageTotal > maxMessagesRunes {
			return errors.New("recent messages are too large")
		}
	}
	total += messageTotal
	if len(input.MemoryIndex) > maxMemoryEntries {
		return errors.New("memory index has too many entries")
	}
	memoryTotal := 0
	for _, entry := range input.MemoryIndex {
		count, err := requiredText("memory index entry", entry, maxMemoryEntryRunes)
		if err != nil {
			return err
		}
		memoryTotal += count
		if memoryTotal > maxMemoryRunes {
			return errors.New("memory index is too large")
		}
	}
	total += memoryTotal
	if len(input.Sources) > maxSources {
		return errors.New("retrieved sources have too many entries")
	}
	sourceTotal := 0
	for _, source := range input.Sources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Kind) == "" || source.Revision == 0 {
			return errors.New("retrieved source facts are incomplete")
		}
		count, err := requiredText("retrieved source text", source.Text, maxSourceRunes)
		if err != nil {
			return err
		}
		sourceTotal += count
		if sourceTotal > maxSourcesRunes {
			return errors.New("retrieved sources are too large")
		}
	}
	total += sourceTotal
	if total > maxContextRunes {
		return errors.New("run context is too large")
	}
	return nil
}

func (prompt Prompt) Text() string {
	var builder strings.Builder
	for _, section := range prompt.Sections {
		fmt.Fprintf(&builder, "<sumi:%s source=%q>\n%s\n</sumi:%s>\n\n", section.Name, section.Source, section.Text, section.Name)
	}
	return builder.String()
}

func jsonSection(name string, value any, source string) Section {
	payload, _ := json.Marshal(value)
	return Section{Name: name, Text: string(payload), Source: source}
}

func requiredText(name, value string, maximum int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	return optionalText(name, value, maximum)
}

func optionalText(name, value string, maximum int) (int, error) {
	if !utf8.ValidString(value) {
		return 0, fmt.Errorf("%s must be valid UTF-8", name)
	}
	runes := utf8.RuneCountInString(value)
	if runes > maximum {
		return 0, fmt.Errorf("%s is too large", name)
	}
	return runes, nil
}
