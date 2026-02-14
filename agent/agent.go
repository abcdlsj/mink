package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/config"
	"github.com/abcdlsj/mink/hook"
	"github.com/abcdlsj/mink/llm"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Agent struct {
	id          string
	p           llm.Provider
	reg     *tool.Registry
	session *session.Session
	bus     *bus.Bus
	hooks   *hook.Manager
	prompt  string
	subAgent    bool
	cfg         config.Config
	stream      bool
	tok         *tokenEstimator
	base        tokenBaseline
	interrupted bool
	cancelFn    context.CancelFunc
	mu          sync.Mutex
}

type tokenBaseline struct {
	msgCount int
	total    int
	source   string
	valid    bool
}

type AgentDeps struct {
	Bus       *bus.Bus
	Provider  llm.Provider
	Hooks     *hook.Manager
	ToolGuard tool.Guard
	Prompt    string
	Config    config.Config
}

func (d *AgentDeps) newAgent(id string, sess *session.Session, subAgent bool) *Agent {
	return New(id, d.Provider, sess,
		WithBus(d.Bus),
		WithHooks(d.Hooks),
		WithToolGuard(d.ToolGuard),
		WithPrompt(d.Prompt),
		WithConfig(d.Config),
		WithSubAgent(subAgent),
	)
}

type Option func(*Agent)

func WithHooks(h *hook.Manager) Option { return func(a *Agent) { a.hooks = h } }
func WithToolGuard(g tool.Guard) Option {
	return func(a *Agent) {
		if a.reg != nil {
			a.reg.SetGuard(g)
		}
	}
}
func WithPrompt(p string) Option           { return func(a *Agent) { a.prompt = p } }
func WithSubAgent(v bool) Option           { return func(a *Agent) { a.subAgent = v } }
func WithBus(b *bus.Bus) Option            { return func(a *Agent) { a.bus = b } }
func WithRegistry(r *tool.Registry) Option { return func(a *Agent) { a.reg = r } }
func WithConfig(c config.Config) Option {
	return func(a *Agent) {
		a.cfg = c
		a.stream = c.Stream
		a.ensureTokenEstimator()
	}
}
func WithStream(s bool) Option { return func(a *Agent) { a.stream = s } }

func New(id string, p llm.Provider, s *session.Session, opts ...Option) *Agent {
	a := &Agent{
		id:      id,
		p:       p,
		session: s,
		reg:     tool.NewRegistry(),
		hooks:   hook.NewManager(),
	}
	for _, opt := range opts {
		opt(a)
	}
	a.ensureTokenEstimator()
	if a.bus != nil {
		a.reg.Register(tool.NewSpawn(a.bus, id))
		bg := tool.NewBackground(a.bus, id)
		if a.cfg.Timeout.Background > 0 {
			bg.SetTimeout(a.cfg.Timeout.Background)
		}
		a.reg.Register(bg)
	}
	if a.reg.Get("brave_search") == nil {
		a.reg.Register(tool.NewBraveSearch(a.cfg.BraveAPIKey))
	}
	return a
}

func (a *Agent) ID() string                { return a.id }
func (a *Agent) Session() *session.Session { return a.session }
func (a *Agent) Tools() *tool.Registry     { return a.reg }

func (a *Agent) Interrupt() {
	a.mu.Lock()
	a.interrupted = true
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.mu.Unlock()
}

func (a *Agent) IsInterrupted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interrupted
}

func (a *Agent) ResetInterrupt() {
	a.mu.Lock()
	a.interrupted = false
	a.cancelFn = nil
	a.mu.Unlock()
}

func (a *Agent) Run(ctx context.Context, src, input string) (retErr error) {
	defer func() {
		if err := a.session.Flush(); err != nil {
			if retErr == nil {
				retErr = err
				return
			}
			retErr = fmt.Errorf("%w; flush: %v", retErr, err)
		}
	}()

	ctx = bus.WithSource(ctx, src)
	a.ResetInterrupt()

	// Create cancellable context for interrupt support
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()

	if a.shouldAutoCompact() {
		if _, err := a.Compact(ctx, src, ""); err != nil && a.bus != nil {
			_ = a.bus.Pub(bus.Msg{
				Type:    bus.TypeAssistant,
				From:    a.id,
				To:      src,
				Payload: fmt.Sprintf("compact warning: %v", err),
			})
		}
	}

	timeout := time.Duration(a.cfg.Timeout.Agent) * time.Second
	if timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, timeout)
		defer timeoutCancel()
	}

	a.session.Add(msg.Message{Role: "user", Content: input})

	if a.bus != nil {
		go a.watchInterrupt()
	}

	maxSteps := a.cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 100
	}

	for i := 0; i < maxSteps; i++ {
		if a.IsInterrupted() {
			a.session.Add(msg.Message{Role: "system", Content: "[User interrupted]"})
			return nil
		}
		if ctx.Err() != nil {
			if a.IsInterrupted() {
				a.session.Add(msg.Message{Role: "system", Content: "[User interrupted]"})
				return nil
			}
			return fmt.Errorf("agent timeout: %w", ctx.Err())
		}
		done, err := a.step(ctx, src)
		if err != nil {
			if a.IsInterrupted() || ctx.Err() == context.Canceled {
				a.session.Add(msg.Message{Role: "system", Content: "[User interrupted]"})
				return nil
			}
			return err
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("max steps reached: %d", maxSteps)
}

func (a *Agent) shouldAutoCompact() bool {
	if !a.cfg.Compact.Auto {
		return false
	}
	msgs := a.session.Messages()

	tokenThreshold := a.cfg.Compact.TriggerTokens
	if tokenThreshold <= 0 {
		tokenThreshold = 12000
	}
	if total, _ := a.sessionTokenTotal(msgs); total >= tokenThreshold {
		return true
	}

	msgThreshold := a.cfg.Compact.TriggerMessages
	if msgThreshold <= 0 {
		msgThreshold = 80
	}
	return len(msgs) >= msgThreshold
}

func (a *Agent) Compact(ctx context.Context, src, note string) (string, error) {
	msgs := a.session.Messages()
	if len(msgs) == 0 {
		return "", nil
	}

	keepRecent := a.cfg.Compact.KeepRecentMessages
	if keepRecent <= 0 {
		keepRecent = 20
	}
	if keepRecent >= len(msgs) {
		return "", nil
	}

	cut := len(msgs) - keepRecent

	oldMsgs := msgs[:cut]
	recentMsgs := msgs[cut:]

	var hist strings.Builder
	hist.WriteString("Summarize the conversation history below for future context retention.\n")
	hist.WriteString("Keep goals, decisions, constraints, file paths, pending tasks, and unresolved issues.\n")
	hist.WriteString("Be concise and structured with bullet points.\n\n")
	if note != "" {
		hist.WriteString("Additional instruction:\n")
		hist.WriteString(note)
		hist.WriteString("\n\n")
	}
	hist.WriteString("History:\n")
	for _, m := range oldMsgs {
		role := m.Role
		content := strings.TrimSpace(m.Content)
		if content == "" {
			if len(m.ToolCalls) > 0 {
				content = fmt.Sprintf("(tool calls: %d)", len(m.ToolCalls))
			} else if len(m.ToolResults) > 0 {
				content = fmt.Sprintf("(tool results: %d)", len(m.ToolResults))
			}
		}
		if content == "" {
			continue
		}
		if len([]rune(content)) > 1200 {
			content = string([]rune(content)[:1200]) + "..."
		}
		fmt.Fprintf(&hist, "[%s]\n%s\n\n", role, content)
	}

	llmTimeout := time.Duration(a.cfg.Timeout.LLM) * time.Second
	compactCtx := ctx
	if llmTimeout > 0 {
		var cancel context.CancelFunc
		compactCtx, cancel = context.WithTimeout(ctx, llmTimeout)
		defer cancel()
	}

	resp, err := a.p.Chat(compactCtx, []msg.Message{
		{Role: "system", Content: "You produce compact, factual context summaries for coding assistants."},
		{Role: "user", Content: hist.String()},
	}, nil)
	if err != nil {
		return "", err
	}

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		summary = "(empty summary)"
	}

	newMsgs := make([]msg.Message, 0, len(recentMsgs)+1)
	newMsgs = append(newMsgs, msg.Message{
		Role:    "system",
		Content: "[Conversation Summary]\n" + summary,
	})
	newMsgs = append(newMsgs, recentMsgs...)
	a.session.Replace(newMsgs)
	a.base = tokenBaseline{}

	oldTotal, _ := a.sessionTokenTotal(msgs)
	newTotal, _ := a.sessionTokenTotal(newMsgs)
	msgText := fmt.Sprintf(
		"[Session compacted] kept recent %d/%d messages (estimated tokens: %d -> %d)",
		len(recentMsgs),
		len(msgs),
		oldTotal,
		newTotal,
	)
	if a.bus != nil {
		_ = a.bus.Pub(bus.Msg{
			Type:    bus.TypeSessionCompact,
			From:    a.id,
			To:      src,
			Payload: msgText,
		})
	}

	return msgText, nil
}

func (a *Agent) TokenUsage() msg.TokenUsage {
	msgs := a.session.Messages()

	total, source := a.sessionTokenTotal(msgs)
	input := 0
	output := 0
	system := 0
	toolTok := 0

	for _, m := range msgs {
		est := a.estimateMessageTokens(m)
		switch m.Role {
		case "user":
			input += est
		case "assistant":
			output += est
		case "system":
			system += est
		case "tool":
			toolTok += est
		default:
			input += est
		}
	}

	return msg.TokenUsage{
		Messages: len(msgs),
		Total:    total,
		Input:    input,
		Output:   output,
		System:   system,
		Tool:     toolTok,
		Source:   source,
	}
}

func (a *Agent) ensureTokenEstimator() {
	if a.tok == nil {
		a.tok = newTokenEstimator(a.cfg.Model)
		return
	}
	a.tok.setModel(a.cfg.Model)
}

func (a *Agent) estimateTokens(msgs []msg.Message) int {
	a.ensureTokenEstimator()
	return a.tok.messages(msgs)
}

func (a *Agent) estimateMessageTokens(m msg.Message) int {
	a.ensureTokenEstimator()
	return a.tok.message(m)
}

func (a *Agent) sessionTokenTotal(msgs []msg.Message) (int, string) {
	fallback := a.estimateTokens(msgs)
	source := "tiktoken-go"

	if !a.base.valid {
		return fallback, source
	}
	if len(msgs) < a.base.msgCount {
		a.base = tokenBaseline{}
		return fallback, source
	}

	total := a.base.total
	if len(msgs) > a.base.msgCount {
		total += a.estimateTokens(msgs[a.base.msgCount:])
	}
	if total > 0 {
		if a.base.source != "" {
			source = a.base.source + "+tiktoken-go"
		}
		return total, source
	}
	return fallback, source
}

func (a *Agent) updateTokenBaseline(msgs, sysMsgs []msg.Message, usage *llm.TokenUsage) {
	if usage == nil || usage.InputTokens <= 0 {
		return
	}

	input := usage.InputTokens
	if len(sysMsgs) > 0 {
		sysTokens := a.estimateTokens(sysMsgs)
		if sysTokens > 0 && input > sysTokens {
			input -= sysTokens
		}
	}
	if input <= 0 {
		return
	}

	source := strings.TrimSpace(usage.InputSource)
	if source == "" {
		source = "provider.input_tokens"
	}
	a.base = tokenBaseline{
		msgCount: len(msgs),
		total:    input,
		source:   source,
		valid:    true,
	}
}

func (a *Agent) watchInterrupt() {
	if a.bus == nil {
		return
	}

	ch := make(chan bus.Msg, 1)
	a.bus.Subscribe(bus.TypeInterrupt, ch)
	defer a.bus.Unsubscribe(bus.TypeInterrupt, ch)

	for {
		select {
		case m := <-ch:
			if m.To == a.id || m.To == bus.AddrBroadcast {
				a.Interrupt()
				return
			}
		case <-time.After(100 * time.Millisecond):
			if a.IsInterrupted() {
				return
			}
		}
	}
}
