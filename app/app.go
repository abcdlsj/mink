package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/abcdlsj/mink/agent"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/skill"
	"github.com/abcdlsj/mink/store"
	"github.com/abcdlsj/mink/tool"
)

type Plugin func(*App) error

type Entrypoint func(context.Context, *App, []string) error
type Service func(context.Context, *App) error

type App struct {
	cfg      config.Config
	bus      *bus.Bus
	store    *store.DB
	provider llm.Provider
	sessions *session.Manager
	tools    *tool.Registry
	cmds     *command.Registry
	router   *command.Router
	skills   *skill.Loader
	runtimes map[string]agent.RuntimeFactory
	entries  map[string]Entrypoint
	services map[string]Service
}

func New(cfg config.Config) (*App, error) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg:      cfg,
		bus:      bus.New(),
		store:    db,
		sessions: session.NewManager(db),
		tools:    tool.NewRegistry(cfg.Workspace),
		cmds:     command.NewRegistry(),
		runtimes: map[string]agent.RuntimeFactory{},
		entries:  map[string]Entrypoint{},
		services: map[string]Service{},
	}
	a.router = command.NewRouter(a.cmds)
	a.skills = skill.NewLoader(cfg.Workspace)
	skill.RegisterTools(a.tools, a.skills)
	if cfg.Ready() {
		provider, err := llm.NewProvider(llm.Config{
			Provider:  cfg.Provider,
			APIKey:    cfg.APIKey,
			BaseURL:   cfg.BaseURL,
			Model:     cfg.Model,
			Headers:   cfg.Headers,
			MaxTokens: cfg.MaxTokens,
		})
		if err != nil {
			return nil, err
		}
		a.provider = provider
	}
	a.RegisterRuntime("native", agent.NewNative)
	a.registerBuiltinCommands()
	a.RegisterEntrypoint("cli", runCLI)
	return a, nil
}

func (a *App) Close() error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Close()
}

func (a *App) Use(ps ...Plugin) error {
	for _, p := range ps {
		if p == nil {
			continue
		}
		if err := p(a); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) Run(ctx context.Context, args []string) error {
	for _, svc := range a.services {
		if err := svc(ctx, a); err != nil {
			return err
		}
	}
	name := "cli"
	if len(args) > 0 {
		if _, ok := a.entries[args[0]]; ok {
			name = args[0]
			args = args[1:]
		}
	}
	entry := a.entries[name]
	if entry == nil {
		return fmt.Errorf("entrypoint not found: %s", name)
	}
	return entry(ctx, a, args)
}

func (a *App) RegisterTool(t tool.Tool) {
	a.tools.Register(t)
}

func (a *App) RegisterCommand(c command.Command) {
	a.cmds.Register(c)
}

func (a *App) RegisterRuntime(name string, f agent.RuntimeFactory) {
	a.runtimes[name] = f
}

func (a *App) RegisterEntrypoint(name string, f Entrypoint) {
	a.entries[name] = f
}

func (a *App) RegisterService(name string, f Service) {
	a.services[name] = f
}

func (a *App) Bus() *bus.Bus {
	return a.bus
}

func (a *App) Config() config.Config {
	return a.cfg
}

func (a *App) HandleInput(ctx context.Context, source, input string) (string, error) {
	return a.handleInput(ctx, source, a.cfg.Runtime, input)
}

func (a *App) HandleInputWithRuntime(ctx context.Context, source, runtime, input string) (string, error) {
	return a.handleInput(ctx, source, runtime, input)
}

func (a *App) handleInput(ctx context.Context, source, runtime, input string) (string, error) {
	ctx = command.WithSource(ctx, source)
	if out, ok, err := a.router.Route(ctx, input); ok {
		a.bus.Publish(bus.Event{
			Type:   bus.CommandHandled,
			Source: source,
			Text:   strings.TrimSpace(out),
			Err:    errString(err),
		})
		return out, err
	}
	if command.IsCommand(input) {
		out, ok, err := a.runShellShortcut(ctx, source, input)
		if ok {
			a.bus.Publish(bus.Event{
				Type:   bus.CommandHandled,
				Source: source,
				Text:   strings.TrimSpace(out),
				Err:    errString(err),
			})
			return out, err
		}
	}
	s, err := a.sessions.Current(source)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(runtime) == "" {
		runtime = a.cfg.Runtime
	}
	f := a.runtimes[runtime]
	if f == nil {
		f = a.runtimes["native"]
	}
	if f == nil {
		return "", fmt.Errorf("runtime not found: %s", runtime)
	}
	rt, err := f(&agent.RuntimeEnv{
		Provider:  a.provider,
		Tools:     a.tools,
		Workspace: a.cfg.Workspace,
		Prompt:    a.cfg.Prompt,
		MaxSteps:  8,
	})
	if err != nil {
		return "", err
	}
	a.bus.Publish(bus.Event{Type: bus.TurnStarted, Source: source, SessionID: s.ID})
	err = rt.Run(ctx, &agent.Turn{
		Source:  source,
		Input:   input,
		Session: s,
		Bus:     a.bus,
	})
	if err != nil {
		a.bus.Publish(bus.Event{Type: bus.TurnError, Source: source, SessionID: s.ID, Err: err.Error()})
		return "", err
	}
	if err := a.sessions.Save(s); err != nil {
		return "", err
	}
	a.bus.Publish(bus.Event{Type: bus.SessionUpdated, Source: source, SessionID: s.ID})
	a.bus.Publish(bus.Event{Type: bus.TurnFinished, Source: source, SessionID: s.ID})
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == "assistant" {
			return s.Messages[i].Content, nil
		}
	}
	return "", nil
}

func (a *App) runShellShortcut(ctx context.Context, source, input string) (string, bool, error) {
	cmd := strings.TrimSpace(strings.TrimPrefix(input, "!"))
	if cmd == "" {
		return "", false, nil
	}
	if a.tools.Get("bash") == nil {
		return "", false, nil
	}
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	id := uuid.New().String()[:8]
	a.bus.Publish(bus.Event{
		Type:       bus.ToolCallStarted,
		Source:     source,
		ToolCallID: id,
		Tool:       "bash",
		Input:      string(args),
	})
	out, err := a.tools.Run(ctx, "bash", args)
	if err != nil {
		a.bus.Publish(bus.Event{
			Type:       bus.ToolCallFailed,
			Source:     source,
			ToolCallID: id,
			Tool:       "bash",
			Input:      string(args),
			Output:     out,
			Err:        err.Error(),
		})
		return out, true, err
	}
	a.bus.Publish(bus.Event{
		Type:       bus.ToolCallFinished,
		Source:     source,
		ToolCallID: id,
		Tool:       "bash",
		Input:      string(args),
		Output:     out,
	})
	return out, true, nil
}

func (a *App) switchModel(provider, model string) error {
	next := a.cfg
	next.Resolve(provider, model)
	if !next.Ready() {
		return fmt.Errorf("provider %s is not configured", provider)
	}
	p, err := llm.NewProvider(llm.Config{
		Provider:  next.Provider,
		APIKey:    next.APIKey,
		BaseURL:   next.BaseURL,
		Model:     next.Model,
		Headers:   next.Headers,
		MaxTokens: next.MaxTokens,
	})
	if err != nil {
		return err
	}
	a.cfg = next
	a.provider = p
	a.bus.Publish(bus.Event{
		Type:   bus.ModelChanged,
		Source: "system",
		Text:   a.currentModel(),
	})
	return nil
}

func (a *App) currentModel() string {
	if a.cfg.Provider == "" {
		return "(unconfigured)"
	}
	return a.cfg.Provider + " / " + a.cfg.Model
}

func (a *App) registerBuiltinCommands() {
	a.RegisterCommand(command.NewFuncCmd("help", "show help", func(ctx context.Context, args []string) (string, error) {
		var b strings.Builder
		b.WriteString("Commands:\n")
		for _, c := range a.cmds.All() {
			b.WriteString("  !" + c.Name() + " - " + c.Desc() + "\n")
		}
		return strings.TrimSpace(b.String()), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("tools", "list tools", func(ctx context.Context, args []string) (string, error) {
		var b strings.Builder
		b.WriteString("Tools:\n")
		for _, t := range a.tools.All() {
			b.WriteString("  " + t.Name() + " - " + t.Desc() + "\n")
		}
		return strings.TrimSpace(b.String()), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("model", "show or set model: !model [provider model]", func(ctx context.Context, args []string) (string, error) {
		if len(args) == 0 {
			return a.currentModel(), nil
		}
		if len(args) != 2 {
			return "usage: !model <provider> <model>", nil
		}
		if err := a.switchModel(args[0], args[1]); err != nil {
			return "", err
		}
		return "switched model to " + a.currentModel(), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("models", "list detected model options", func(ctx context.Context, args []string) (string, error) {
		opts := config.Detect()
		if len(opts) == 0 {
			return "no configured model providers detected", nil
		}
		var b strings.Builder
		b.WriteString("Detected models:\n")
		for _, opt := range opts {
			b.WriteString("  " + opt.Provider + " / " + opt.Model + " [" + opt.Source + "]\n")
		}
		return strings.TrimSpace(b.String()), nil
	}))

	a.RegisterCommand(command.NewFuncCmd("session", "manage sessions", func(ctx context.Context, args []string) (string, error) {
		source := command.SourceFrom(ctx)
		if len(args) == 0 {
			return "usage: !session [list|current|new|switch <id>]", nil
		}
		switch args[0] {
		case "list":
			sessions, err := a.sessions.List()
			if err != nil {
				return "", err
			}
			if len(sessions) == 0 {
				return "no sessions", nil
			}
			var b strings.Builder
			for _, s := range sessions {
				line := s.ID
				if s.Title != "" {
					line += " [" + s.Title + "]"
				}
				b.WriteString(line + "\n")
			}
			return strings.TrimSpace(b.String()), nil
		case "current":
			s, err := a.sessions.Current(source)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s [%s]", s.ID, s.Title), nil
		case "new":
			s, err := a.sessions.New(source)
			if err != nil {
				return "", err
			}
			return "new session: " + s.ID, nil
		case "switch":
			if len(args) < 2 {
				return "usage: !session switch <id>", nil
			}
			s, err := a.sessions.Switch(source, args[1])
			if err != nil {
				return "", err
			}
			return "switched session: " + s.ID, nil
		default:
			return "usage: !session [list|current|new|switch <id>]", nil
		}
	}))

	a.RegisterCommand(command.NewFuncCmd("compact", "summarize and compact current session", func(ctx context.Context, args []string) (string, error) {
		source := command.SourceFrom(ctx)
		s, err := a.sessions.Current(source)
		if err != nil {
			return "", err
		}
		if a.provider == nil {
			return "", fmt.Errorf("model is not configured")
		}
		summary, err := a.compactSession(ctx, s)
		if err != nil {
			return "", err
		}
		if err := a.sessions.Save(s); err != nil {
			return "", err
		}
		return "compacted session: " + summary, nil
	}))
}

func (a *App) compactSession(ctx context.Context, s *session.Session) (string, error) {
	if len(s.Messages) == 0 {
		return "empty session", nil
	}
	var b strings.Builder
	for _, m := range s.Messages {
		switch m.Role {
		case "user", "assistant":
			b.WriteString(m.Role + ": " + m.Content + "\n")
		case "tool":
			for _, tr := range m.ToolResults {
				b.WriteString("tool: " + tr.Content + "\n")
			}
		}
	}
	resp, err := a.provider.Chat(ctx, []msg.Message{
		{Role: "system", Content: "Summarize the conversation for future continuation. Keep it short and factual."},
		{Role: "user", Content: b.String()},
	}, nil)
	if err != nil {
		return "", err
	}
	s.Compact(strings.TrimSpace(resp.Content), 8)
	return s.Summary, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
