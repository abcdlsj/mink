package app

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	store    *store.Store
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
	cfg.Normalize()
	db, err := store.Open(cfg.DataRoot())
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
	a.tools.SetGuard(tool.NewPolicyGuard(cfg.Workspace, cfg.PermissionsPath()))
	a.bus.OnPublish(func(ev bus.Event) {
		_ = db.AppendEvent(ev)
	})
	a.router = command.NewRouter(a.cmds)
	a.skills = skill.NewLoader(cfg.Workspace)
	skill.RegisterTools(a.tools, a.skills)
	a.provider, err = newProvider(cfg)
	if err != nil {
		return nil, err
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
		summary, err := a.compactSession(ctx, s)
		if err != nil {
			return "", err
		}
		if err := a.sessions.Save(s); err != nil {
			return "", err
		}
		a.bus.Publish(bus.Event{
			Type:      bus.SessionCompacted,
			Source:    source,
			SessionID: s.ID,
			Text:      summary,
		})
		return "compacted session: " + summary, nil
	}))
}

func (a *App) compactSession(ctx context.Context, s *session.Session) (string, error) {
	return a.compactSessionKeep(ctx, s, 8)
}

func (a *App) compactSessionKeep(ctx context.Context, s *session.Session, keep int) (string, error) {
	if len(s.Messages) == 0 {
		return "empty session", nil
	}
	summary, err := a.buildCompactSummary(ctx, s)
	if err != nil {
		return "", err
	}
	s.Compact(summary, keep)
	return s.Summary, nil
}

func (a *App) buildCompactSummary(ctx context.Context, s *session.Session) (string, error) {
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
	if a.provider != nil {
		resp, err := a.provider.Chat(ctx, []msg.Message{
			{Role: "system", Content: "Summarize the conversation for future continuation. Keep it short and factual."},
			{Role: "user", Content: b.String()},
		}, nil)
		if err == nil {
			return strings.TrimSpace(resp.Content), nil
		}
	}
	return heuristicSummary(s.Messages), nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *App) autoCompact(ctx context.Context, source, runtime string, s *session.Session) error {
	if !a.shouldAutoCompact(runtime, s) {
		return nil
	}
	summary, err := a.compactSessionKeep(ctx, s, a.cfg.Compact.KeepRecentMessages)
	if err != nil {
		return err
	}
	if err := a.sessions.Save(s); err != nil {
		return err
	}
	a.bus.Publish(bus.Event{
		Type:      bus.SessionCompacted,
		Source:    source,
		SessionID: s.ID,
		Text:      summary,
	})
	return nil
}

func (a *App) shouldAutoCompact(runtime string, s *session.Session) bool {
	if !a.cfg.Compact.Auto || s == nil || len(s.Messages) == 0 {
		return false
	}
	if isExternalDriverRuntime(runtime) {
		return false
	}
	if n := a.cfg.Compact.TriggerMessages; n > 0 && len(s.Messages) >= n {
		return true
	}
	if n := a.compactTokenLimit(); n > 0 && estimateMessages(s.Messages) >= n {
		return true
	}
	return false
}

func (a *App) compactTokenLimit() int {
	mc := a.cfg.Active
	if mc.ContextWindow > 0 {
		limit := mc.ContextWindow - maxInt(mc.MaxTokens, a.cfg.MaxTokens) - a.cfg.Compact.ReserveTokens
		if limit > 0 {
			return limit
		}
	}
	if a.cfg.Compact.TriggerTokens > 0 {
		return a.cfg.Compact.TriggerTokens
	}
	return 0
}

func isExternalDriverRuntime(runtime string) bool {
	switch strings.TrimSpace(runtime) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func heuristicSummary(msgs []msg.Message) string {
	var b strings.Builder
	start := 0
	if len(msgs) > 12 {
		start = len(msgs) - 12
	}
	for _, m := range msgs[start:] {
		text := primaryText(m)
		if text == "" {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(trimText(text, 160))
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "Conversation compacted at " + time.Now().Format(time.RFC3339)
	}
	return out
}

func primaryText(m msg.Message) string {
	switch {
	case strings.TrimSpace(m.Content) != "":
		return m.Content
	case strings.TrimSpace(m.Reasoning) != "":
		return m.Reasoning
	case len(m.ToolCalls) > 0:
		var parts []string
		for _, tc := range m.ToolCalls {
			parts = append(parts, tc.Name+"("+strings.TrimSpace(string(tc.Args))+")")
		}
		return strings.Join(parts, "; ")
	case len(m.ToolResults) > 0:
		var parts []string
		for _, tr := range m.ToolResults {
			part := tr.Content
			if strings.TrimSpace(tr.Error) != "" {
				part = "error: " + tr.Error
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, "; ")
	default:
		return ""
	}
}

func estimateMessages(msgs []msg.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessage(m)
	}
	return total
}

func estimateMessage(m msg.Message) int {
	n := len([]rune(m.Content)) + len([]rune(m.Reasoning))
	for _, tc := range m.ToolCalls {
		n += len([]rune(tc.Name)) + len([]rune(string(tc.Args)))
	}
	for _, tr := range m.ToolResults {
		n += len([]rune(tr.Content)) + len([]rune(tr.Error))
	}
	if n == 0 {
		return 0
	}
	return n/4 + 1
}

func trimText(s string, n int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n <= 3 {
		return "..."
	}
	return string(rs[:n-3]) + "..."
}
