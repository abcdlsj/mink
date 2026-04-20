package collab

import (
	"encoding/json"
	"sync"

	"github.com/abcdlsj/mink/app"
)

const (
	taskStarted  = "delegate.started"
	taskFinished = "delegate.finished"
	taskFailed   = "delegate.failed"
)

type manager struct {
	app   *app.App
	mu    sync.Mutex
	tasks map[string]*task
	teams map[string]map[string]string
}

type task struct {
	id     string
	output string
	err    error
	done   chan struct{}
}

type spawnArgs struct {
	Task         string `json:"task"`
	ShareContext bool   `json:"share_context"`
	DirectOutput bool   `json:"direct_output"`
	Runtime      string `json:"runtime"`
}

type delegateArgs struct {
	Task         string   `json:"task"`
	Target       string   `json:"target"`
	Capabilities []string `json:"capabilities"`
	ShareContext bool     `json:"share_context"`
	DirectOutput bool     `json:"direct_output"`
}

type inviteArgs struct {
	AgentID         string `json:"agent_id"`
	RoleName        string `json:"role_name"`
	RoleDescription string `json:"role_description"`
	Task            string `json:"task"`
}

type mentionArgs struct {
	AgentID  string `json:"agent_id"`
	Question string `json:"question"`
}

type pollArgs struct {
	TaskID string `json:"task_id"`
}

type specialistArgs struct {
	RoleName        string   `json:"role_name"`
	RoleDescription string   `json:"role_description"`
	ProfileHint     string   `json:"profile_hint"`
	Capabilities    []string `json:"capabilities"`
	Task            string   `json:"task"`
	AgentID         string   `json:"agent_id"`
}

func newManager(a *app.App) *manager {
	return &manager{
		app:   a,
		tasks: map[string]*task{},
		teams: map[string]map[string]string{},
	}
}

func decode[T any](name string, args json.RawMessage, dst *T) error {
	return parseError(name, json.Unmarshal(args, dst))
}
