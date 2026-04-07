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
	"github.com/abcdlsj/mink/memory"
	"github.com/abcdlsj/mink/msg"
	"github.com/abcdlsj/mink/runlog"
	rtsqlite "github.com/abcdlsj/mink/runtime/store/sqlite"
	"github.com/abcdlsj/mink/session"
	"github.com/abcdlsj/mink/tool"
)

type Agent struct {
	id               string
	p                llm.Provider
	sel              *llm.Sel
	nextModel        string
	reg              *tool.Registry
	guard            tool.Guard
	extraTools       []tool.Tool
	session          *session.Session
	bus              *bus.Bus
	hooks            *hook.Manager
	prompt           string
	subAgent         bool
	cfg              config.Config
	stream           bool
	tok              *tokenEstimator
	base             tokenBaseline
	turnToolHistory  map[string]turnToolRecord
	turnStateVersion int
	sessionDir       string
	rt               *rtsqlite.DB
	mem              *memory.Store
	trace            *runlog.Logger
	interrupted      bool
	cancelFn         context.CancelFunc
	mu               sync.Mutex
}

type tokenBaseline struct {
	msgCount int
	total    int
	source   string
	valid    bool
}

type AgentDeps struct {
	Bus        *bus.Bus
	Provider   llm.Provider
	Sel        *llm.Sel
	Hooks      *hook.Manager
	ToolGuard  tool.Guard
	CronTool   tool.Tool
	Prompt     string
	Config     config.Config
	SessionDir string
	RuntimeDB  *rtsqlite.DB
	Memory     *memory.Store
}

func (d *AgentDeps) newAgent(id string, sess *session.Session, subAgent bool) *Agent {
	return New(id, d.Provider, sess,
		WithBus(d.Bus),
		WithSel(d.Sel),
		WithHooks(d.Hooks),
		WithToolGuard(d.ToolGuard),
		WithCronTool(d.CronTool),
		WithPrompt(d.Prompt),
		WithConfig(d.Config),
		WithSubAgent(subAgent),
		WithSessionDir(d.SessionDir),
		WithRuntimeDB(d.RuntimeDB),
		WithMemoryStore(d.Memory),
	)
}

type Option func(*Agent)

func WithHooks(h *hook.Manager) Option { return func(a *Agent) { a.hooks = h } }
func WithToolGuard(g tool.Guard) Option {
	return func(a *Agent) {
		a.guard = g
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
func WithStream(s bool) Option       { return func(a *Agent) { a.stream = s } }
func WithSessionDir(d string) Option { return func(a *Agent) { a.sessionDir = d } }
func WithSel(s *llm.Sel) Option      { return func(a *Agent) { a.sel = s } }
func WithRuntimeDB(db *rtsqlite.DB) Option {
	return func(a *Agent) { a.rt = db }
}
func WithMemoryStore(mem *memory.Store) Option {
	return func(a *Agent) { a.mem = mem }
}

func New(id string, p llm.Provider, s *session.Session, opts ...Option) *Agent {
	a := &Agent{
		id:        id,
		p:         p,
		session:   s,
		nextModel: "default",
		hooks:     hook.NewManager(),
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.reg == nil {
		if a.sel != nil {
			a.reg = tool.NewRegistry(a.sel)
		} else {
			a.reg = tool.NewRegistry(nil)
		}
	}
	if a.guard != nil {
		a.reg.SetGuard(a.guard)
	}
	for _, extra := range a.extraTools {
		a.reg.Register(extra)
	}
	if a.bus != nil {
		a.reg.Register(tool.NewSpawn(a.bus, id))
		bg := tool.NewBackground(a.bus, id)
		if a.cfg.Timeout.Background > 0 {
			bg.SetTimeout(a.cfg.Timeout.Background)
		}
		a.reg.Register(bg)
	}
	a.reg.Register(tool.NewBraveSearch(a.cfg.Key("BRAVE_API_KEY")))
	a.ensureTokenEstimator()
	a.initTrace()
	return a
}

func WithCronTool(t tool.Tool) Option {
	return func(a *Agent) {
		if t == nil {
			return
		}
		a.extraTools = append(a.extraTools, t)
		if a.reg != nil {
			a.reg.Register(t)
		}
	}
}

func WithExtraTool(t tool.Tool) Option {
	return func(a *Agent) {
		if t == nil {
			return
		}
		a.extraTools = append(a.extraTools, t)
		if a.reg != nil {
			a.reg.Register(t)
		}
	}
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

func (a *Agent) Run(ctx context.Context, src, input string) error {
	a.applyStreamForSource(src)
	return a.run(ctx, src, "user", input)
}

func (a *Agent) RunSystem(ctx context.Context, src, input string) error {
	a.applyStreamForSource(src)
	return a.run(ctx, src, "system", input)
}

func (a *Agent) applyStreamForSource(src string) {
	if strings.HasPrefix(src, "telegram:") {
		a.stream = a.cfg.TelegramStream
	} else {
		a.stream = a.cfg.Stream
	}
}

func (a *Agent) run(ctx context.Context, src, role, input string) (retErr error) {
	a.logSessionStart(src)
	defer a.logSessionEnd()
	defer a.flushSession(&retErr)

	ctx, cancel := a.prepareRunContext(ctx, src)
	defer cancel()
	a.maybeAutoCompact(ctx, src)
	ctx, cancel = a.withAgentTimeout(ctx)
	defer cancel()

	a.session.Add(msg.Message{Role: role, Content: input})
	a.resetTurnState()
	a.logUserInput(role, input)
	a.appendEvent(ctx, "input.received", role, map[string]any{"content": input})

	if a.bus != nil {
		go a.watchInterrupt(ctx)
	}

	return a.runSteps(ctx, src)
}

func (a *Agent) flushSession(retErr *error) {
	if err := a.session.Flush(); err != nil {
		a.logWarn("session_flush_error", map[string]any{"error": err.Error()})
		if *retErr == nil {
			*retErr = err
			return
		}
		*retErr = fmt.Errorf("%w; flush: %v", *retErr, err)
	}
}

func (a *Agent) prepareRunContext(ctx context.Context, src string) (context.Context, context.CancelFunc) {
	ctx = bus.WithSource(ctx, src)
	a.ResetInterrupt()

	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()
	return ctx, cancel
}

func (a *Agent) maybeAutoCompact(ctx context.Context, src string) {
	if !a.shouldAutoCompact() {
		return
	}
	if _, err := a.Compact(ctx, src, ""); err != nil {
		a.publishCompactWarning(src, err)
	}
}

func (a *Agent) publishCompactWarning(src string, err error) {
	if err == nil || a.bus == nil {
		return
	}
	a.logWarn("session_compact_warning", map[string]any{"error": err.Error()})
	_ = a.bus.Pub(bus.Msg{
		Type:    bus.TypeAssistant,
		From:    a.id,
		To:      src,
		Payload: fmt.Sprintf("compact warning: %v", err),
	})
}

func (a *Agent) withAgentTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := time.Duration(a.cfg.Timeout.Agent) * time.Second
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (a *Agent) runSteps(ctx context.Context, src string) error {
	maxSteps := a.cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 100
	}

	for i := 0; i < maxSteps; i++ {
		if err := a.checkRunState(ctx); err != nil {
			return err
		}
		done, err := a.runSingleStep(ctx, src, i)
		if err != nil {
			return a.normalizeRunError(ctx, err)
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("max steps reached: %d", maxSteps)
}

func (a *Agent) checkRunState(ctx context.Context) error {
	if a.IsInterrupted() {
		return a.finishInterruptedRun()
	}
	if err := ctx.Err(); err != nil {
		if a.IsInterrupted() || err == context.Canceled {
			return a.finishInterruptedRun()
		}
		return fmt.Errorf("agent timeout: %w", err)
	}
	return nil
}

func (a *Agent) runSingleStep(ctx context.Context, src string, stepNum int) (bool, error) {
	stepStart := time.Now()
	a.logStepStart(stepNum)
	done, err := a.step(ctx, src, stepNum)
	a.logStepEnd(stepNum, time.Since(stepStart), err)
	return done, err
}

func (a *Agent) normalizeRunError(ctx context.Context, err error) error {
	if a.IsInterrupted() || ctx.Err() == context.Canceled {
		return a.finishInterruptedRun()
	}
	return err
}

func (a *Agent) finishInterruptedRun() error {
	a.logInterrupt("user interrupted")
	a.session.Add(msg.Message{Role: "system", Content: "[User interrupted]"})
	return nil
}

func (a *Agent) shouldAutoCompact() bool {
	if !a.cfg.Compact.Auto {
		return false
	}
	msgs := a.viewMessages()

	tokenThreshold := a.compactTrigger()
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
	view := a.session.View()
	if len(view.Messages) == 0 {
		return "", nil
	}
	msgs := a.session.Messages()

	keepRecent := a.cfg.Compact.KeepRecentMessages
	if keepRecent <= 0 {
		keepRecent = 20
	}
	if len(msgs) > 0 && keepRecent >= len(msgs) && len(view.Messages) == len(msgs) {
		return "", nil
	}

	cut := findCompactCut(msgs, keepRecent)
	recentMsgs := msgs[cut:]
	base := len(view.Messages) - len(msgs)
	if base < 0 {
		base = 0
	}
	history := base + cut
	if history > len(view.Messages) {
		history = len(view.Messages)
	}
	oldMsgs := view.Messages[:history]
	if len(oldMsgs) == 0 {
		return "", nil
	}

	hist := buildCompactHistoryPrompt(oldMsgs, note)
	if strings.TrimSpace(hist) == "" {
		return "", nil
	}

	llmTimeout := time.Duration(a.cfg.Timeout.LLM) * time.Second
	compactCtx := ctx
	if llmTimeout > 0 {
		var cancel context.CancelFunc
		compactCtx, cancel = context.WithTimeout(ctx, llmTimeout)
		defer cancel()
	}

	compactSys := "You produce compact, factual context summaries for coding assistants."
	start := time.Now()
	corrID := a.logLLMRequest(-1, 2, false)
	resp, err := a.p.Chat(compactCtx, []msg.Message{
		{Role: "system", Content: compactSys},
		{Role: "user", Content: hist},
	}, nil)
	if err != nil {
		a.logLLMError(-1, corrID, err, time.Since(start))
		return "", err
	}
	a.logLLMResponse(-1, corrID, resp, time.Since(start))

	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		summary = "(empty summary)"
	}

	a.session.AddAnchor(session.AnchorSummary, summary, note, cut)
	a.base = tokenBaseline{}
	a.rememberSummary(ctx, src, summary, note)

	oldTotal, _ := a.sessionTokenTotal(view.Messages)
	newView := a.session.View()
	newTotal, _ := a.sessionTokenTotal(newView.Messages)
	msgText := fmt.Sprintf(
		"[Session compacted] anchored %d messages, kept recent %d/%d entries (estimated tokens: %d -> %d)",
		history,
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
	msgs := a.viewMessages()

	total, source := a.sessionTokenTotal(msgs)
	trigger := a.compactTrigger()
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
		Messages:       len(msgs),
		Total:          total,
		Input:          input,
		Output:         output,
		System:         system,
		Tool:           toolTok,
		CompactTrigger: trigger,
		ContextWindow:  a.cfg.Active.ContextWindow,
		MaxTokens:      a.maxOutputTokens(),
		Reserve:        a.compactReserveTokens(),
		Source:         source,
	}
}

func (a *Agent) compactTrigger() int {
	trigger := a.cfg.Compact.TriggerTokens
	limit := a.modelCompactLimit()
	if limit > 0 {
		if trigger <= 0 || trigger > limit {
			return limit
		}
		return trigger
	}
	if trigger > 0 {
		return trigger
	}
	return 12000
}

func (a *Agent) modelCompactLimit() int {
	ctxWin := a.cfg.Active.ContextWindow
	if ctxWin <= 0 {
		return 0
	}

	limit := ctxWin - a.maxOutputTokens() - a.compactReserveTokens()
	if limit < 1 {
		return 1
	}
	return limit
}

func (a *Agent) maxOutputTokens() int {
	if a.cfg.Active.MaxTokens > 0 {
		return a.cfg.Active.MaxTokens
	}
	return 4096
}

func (a *Agent) compactReserveTokens() int {
	if a.cfg.Compact.ReserveTokens > 0 {
		return a.cfg.Compact.ReserveTokens
	}
	return 2048
}

func (a *Agent) viewMessages() []msg.Message {
	return a.session.View().Messages
}

func (a *Agent) ensureTokenEstimator() {
	if a.tok == nil {
		a.tok = newTokenEstimator(a.cfg.Active.Model)
		return
	}
	a.tok.setModel(a.cfg.Active.Model)
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

func (a *Agent) watchInterrupt(ctx context.Context) {
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
		case <-ctx.Done():
			return
		}
	}
}

func findCompactCut(msgs []msg.Message, keep int) int {
	cut := len(msgs) - keep
	if cut <= 0 {
		return 0
	}

	for cut > 0 && msgs[cut].Role == "tool" {
		id := toolCallID(msgs[cut])
		if id == "" {
			cut++
			break
		}
		if i := findToolCaller(msgs, cut, id); i >= 0 {
			cut = i
		} else {
			cut++
			break
		}
	}
	return cut
}

func toolCallID(m msg.Message) string {
	if len(m.ToolResults) == 0 {
		return ""
	}
	return m.ToolResults[0].ToolCallID
}

func findToolCaller(msgs []msg.Message, end int, id string) int {
	for i := end - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		for _, tc := range msgs[i].ToolCalls {
			if tc.ID == id {
				return i
			}
		}
	}
	return -1
}

const (
	compactMaxHistoryRunes = 24000
	compactMaxMessageRunes = 800
)

func buildCompactHistoryPrompt(oldMsgs []msg.Message, note string) string {
	var b strings.Builder
	b.WriteString("Summarize the conversation history below for future context retention.\n")
	b.WriteString("Keep goals, decisions, constraints, file paths, pending tasks, and unresolved issues.\n")
	b.WriteString("Be concise and structured with bullet points.\n\n")
	if note != "" {
		b.WriteString("Additional instruction:\n")
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString("History:\n")

	blocks := collectCompactBlocks(oldMsgs, compactMaxHistoryRunes)
	if len(blocks) == 0 {
		return ""
	}
	for _, block := range blocks {
		b.WriteString(block)
	}
	return b.String()
}

func collectCompactBlocks(oldMsgs []msg.Message, maxRunes int) []string {
	if maxRunes <= 0 {
		maxRunes = compactMaxHistoryRunes
	}
	used := 0
	var rev []string

	for i := len(oldMsgs) - 1; i >= 0; i-- {
		block := compactMessageBlock(oldMsgs[i])
		if block == "" {
			continue
		}
		n := len([]rune(block))
		if used+n > maxRunes {
			break
		}
		rev = append(rev, block)
		used += n
	}

	if len(rev) == 0 {
		return nil
	}

	blocks := make([]string, 0, len(rev)+1)
	for i := len(rev) - 1; i >= 0; i-- {
		blocks = append(blocks, rev[i])
	}
	if len(blocks) < len(oldMsgs) {
		blocks = append([]string{"[system]\n(earlier history omitted due to size)\n\n"}, blocks...)
	}
	return blocks
}

func compactMessageBlock(m msg.Message) string {
	content := strings.TrimSpace(m.Content)
	if content == "" {
		if len(m.ToolCalls) > 0 {
			content = fmt.Sprintf("(tool calls: %d)", len(m.ToolCalls))
		} else if len(m.ToolResults) > 0 {
			content = fmt.Sprintf("(tool results: %d)", len(m.ToolResults))
		}
	}
	if content == "" {
		return ""
	}
	if len([]rune(content)) > compactMaxMessageRunes {
		content = string([]rune(content)[:compactMaxMessageRunes]) + "..."
	}
	return fmt.Sprintf("[%s]\n%s\n\n", m.Role, content)
}
