package tool

import (
	"encoding/json"
	"strings"
	"time"
)

type ActionProposal struct {
	Intent    string    `json:"intent,omitempty"`
	Target    string    `json:"target,omitempty"`
	Risk      string    `json:"risk,omitempty"`
	Preview   string    `json:"preview,omitempty"`
	Rollback  string    `json:"rollback,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func ProposalFor(call Call) ActionProposal {
	p := ActionProposal{
		Preview:   call.Action,
		ExpiresAt: time.Now().Add(time.Minute),
	}
	switch call.Tool {
	case "bash":
		p.Intent = "Run shell command"
		p.Target = shellTarget(call.Action)
		p.Risk = string(RiskShell)
		p.Rollback = "Manual recovery may be required"
	case "read":
		p.Intent = "Read sensitive file"
		p.Target = strings.TrimSpace(strings.TrimPrefix(call.Action, "read "))
		p.Risk = "sensitive_read"
		p.Rollback = "No filesystem change"
	case "write", "edit":
		p.Intent = actionName(call.Tool) + " file"
		p.Target = strings.TrimSpace(strings.TrimPrefix(call.Action, call.Tool+" "))
		p.Risk = "filesystem_write"
		p.Rollback = "Restore file from version control or backup"
	default:
		p.Intent = "Run action"
		p.Target = call.Tool
		p.Risk = string(RiskSafe)
	}
	return p
}

func actionName(tool string) string {
	if tool == "write" {
		return "Write"
	}
	if tool == "edit" {
		return "Edit"
	}
	return "Run"
}

func (p ActionProposal) JSON() string {
	data, _ := json.Marshal(p)
	return string(data)
}

func shellTarget(action string) string {
	cmd := strings.TrimSpace(strings.TrimPrefix(action, "bash "))
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "shell"
	}
	return fields[0]
}
