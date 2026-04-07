package agent

import "fmt"

const MaxDelegationDepth = 3

// DelegatePayload is the payload for delegate:request messages.
type DelegatePayload struct {
	TaskID       string   `json:"task_id"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities,omitempty"`
	TargetAgent  string   `json:"target_agent,omitempty"`
	ReplyTo      string   `json:"reply_to"`
	Depth        int      `json:"depth"`
}

// DelegateAckPayload is the payload for delegate:ack messages.
type DelegateAckPayload struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	AgentID string `json:"agent_id"`
}

// DelegateResultPayload is the payload for delegate:result messages.
type DelegateResultPayload struct {
	TaskID  string `json:"task_id"`
	RunID   string `json:"run_id"`
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

var ErrDelegationTooDeep = fmt.Errorf("delegation depth exceeds maximum (%d)", MaxDelegationDepth)

func CheckDelegationDepth(depth int) error {
	if depth > MaxDelegationDepth {
		return ErrDelegationTooDeep
	}
	return nil
}
