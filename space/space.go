package space

import "time"

type Kind string

const (
	KindChannel    Kind = "channel"
	KindDirectChat Kind = "direct_chat"
	KindAgentDM    Kind = "agent_dm"
)

type Space struct {
	ID               string                       `json:"id"`
	Kind             Kind                         `json:"kind"`
	Title            string                       `json:"title,omitempty"`
	Participants     []Participant                `json:"participants"`
	Messages         []Message                    `json:"messages"`
	AgentModes       map[string]string            `json:"agent_modes,omitempty"`
	ThreadAgentModes map[string]map[string]string `json:"thread_agent_modes,omitempty"`
	CreatedAt        time.Time                    `json:"created_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
}
