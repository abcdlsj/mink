package collab

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/abcdlsj/sumi/app"
)

type manager struct {
	app   *app.App
	path  string
	sem   chan struct{}
	queue chan struct{}

	mu    sync.Mutex
	tasks map[string]*task
	teams map[string]map[string]string
}

type task struct {
	id     string
	source string
	output string
	err    error
	ctx    context.Context
	cancel context.CancelFunc
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

type cancelArgs struct {
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
	cfg := a.Config()
	m := &manager{
		app:   a,
		path:  cfg.CollabTeamsPath(),
		sem:   make(chan struct{}, cfg.Collab.MaxConcurrent),
		queue: make(chan struct{}, cfg.Collab.QueueDepth),
		tasks: map[string]*task{},
		teams: map[string]map[string]string{},
	}
	m.loadTeams()
	return m
}

func decode[T any](name string, args json.RawMessage, dst *T) error {
	return parseError(name, json.Unmarshal(args, dst))
}
