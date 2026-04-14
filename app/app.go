package app

import (
	"context"
	"errors"
	"sync"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	mcron "github.com/abcdlsj/mink/cron"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/platform"
	"github.com/abcdlsj/mink/platform/cliapp"
	"github.com/abcdlsj/mink/platform/telegrambot"
	"github.com/abcdlsj/mink/platform/webapp"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
)

var (
	ErrClosed         = errors.New("mink app is closed")
	ErrAPIKeyRequired = errors.New("need api key")

	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Options struct {
	Config    config.Config
	Bus       *bus.Bus
	Provider  llm.Provider
	Hooks     *hook.Manager
	Workspace string
}

type sourceState struct {
	teamID    string
	threadID  string
	section   string
	sessionID string
}

type App struct {
	cfg      config.Config
	bus      *bus.Bus
	p        llm.Provider
	sm       *session.Manager
	rt       *rtsqlite.DB
	mw       *memory.Watcher
	hooks    *hook.Manager
	cmdReg   *command.Registry
	router   *command.Router
	guard    *command.GuardMux
	sup      *agent.Supervisor
	disp     *agent.Dispatcher
	reg      *agent.Registry
	hb       *agent.HeartbeatManager
	cron     *mcron.Scheduler
	adapters []platform.Adapter

	cli       *cliapp.CLI
	web       *webapp.Web
	telegram  *telegrambot.Telegram
	workspace string

	sources map[string]*sourceState

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
	closed  bool
}
