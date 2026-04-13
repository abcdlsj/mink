package platform

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/tool"
	tele "gopkg.in/telebot.v4"
)

const (
	telegramConfirmTimeout = 60 * time.Second
	telegramActiveTTL      = 30 * time.Minute
	telegramStreamMinInt   = 4 * time.Second
	telegramStreamMaxWait  = 5 * time.Second
	telegramStreamMinLen   = 1500
	telegramTypingRefresh  = 10 * time.Second
	telegramMsgLimit       = 3800
	confirmCallbackPrefix  = "mcfm"
	telegramTypingCooldown = 5 * time.Second
)

type confirmState struct {
	ch      chan tool.Approval
	created time.Time
	msgID   int
}

type streamState struct {
	chatID        int64
	buf           strings.Builder
	msgID         int
	progressMsgID int
	progressText  string
	dirty         bool
	ended         bool
	flush         bool
	at            time.Time
}

type inboundState struct {
	msgID    int
	threadID int
}

type assistantOutState struct {
	text      string
	replyToID int
	at        time.Time
}

type Telegram struct {
	token        string
	bus          *bus.Bus
	router       *command.Router
	bot          *tele.Bot
	stop         chan struct{}
	events       chan bus.Msg
	mentionMode  string
	sessionScope string
	agentNames   map[string]string
	agentNamesMu sync.RWMutex

	confirmMu sync.Mutex
	confirms  map[int64]map[string]confirmState

	streamMu sync.Mutex
	streams  map[string]*streamState

	inboundMu sync.RWMutex
	inbound   map[string][]inboundState
	lastIn    map[string]inboundState

	assistMu sync.Mutex
	assist   map[string]assistantOutState

	activeMu    sync.RWMutex
	activeChats map[int64]time.Time

	typingMu   sync.Mutex
	typing     map[int64]chan struct{}
	typingN    map[int64]int
	typingLast map[int64]time.Time
}

type TelegramOptions struct {
	MentionMode  string
	SessionScope string
}

func NewTelegram(token string, b *bus.Bus, r *command.Router, opts TelegramOptions) *Telegram {
	mentionMode := strings.ToLower(strings.TrimSpace(opts.MentionMode))
	if mentionMode == "" {
		mentionMode = "always"
	}
	if mentionMode != "always" && mentionMode != "smart" && mentionMode != "mention_only" {
		mentionMode = "always"
	}
	sessionScope := strings.ToLower(strings.TrimSpace(opts.SessionScope))
	if sessionScope == "" {
		sessionScope = "chat"
	}
	if sessionScope != "chat" && sessionScope != "thread" {
		sessionScope = "chat"
	}
	return &Telegram{
		token:        token,
		bus:          b,
		router:       r,
		stop:         make(chan struct{}),
		mentionMode:  mentionMode,
		sessionScope: sessionScope,
		confirms:     make(map[int64]map[string]confirmState),
		streams:      make(map[string]*streamState),
		inbound:      make(map[string][]inboundState),
		lastIn:       make(map[string]inboundState),
		assist:       make(map[string]assistantOutState),
		activeChats:  make(map[int64]time.Time),
		typing:       make(map[int64]chan struct{}),
		typingN:      make(map[int64]int),
		typingLast:   make(map[int64]time.Time),
	}
}

func (t *Telegram) ID() string { return "telegram" }

func (t *Telegram) Token() string { return t.token }

func (t *Telegram) SetAgentNames(names map[string]string) {
	t.agentNamesMu.Lock()
	defer t.agentNamesMu.Unlock()
	t.agentNames = names
}

func (t *Telegram) routeTarget(text string) string {
	t.agentNamesMu.RLock()
	defer t.agentNamesMu.RUnlock()
	lower := strings.ToLower(text)
	for name, id := range t.agentNames {
		if strings.Contains(lower, "@"+strings.ToLower(name)) {
			return id
		}
	}
	return bus.AddrAgentMain
}

func (t *Telegram) Start(ctx context.Context) error {
	pref := tele.Settings{
		Token:  t.token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	bot, err := tele.NewBot(pref)
	if err != nil {
		return err
	}
	t.bot = bot
	t.infof("bot started: @%s", bot.Me.Username)

	bot.Handle(tele.OnText, func(c tele.Context) error {
		return t.handleMessage(c)
	})

	bot.Handle(tele.OnCallback, func(c tele.Context) error {
		_, err := t.handleConfirmCallback(c)
		return err
	})

	go bot.Start()
	go t.forward(ctx)
	go t.flushStreamLoop(ctx)

	return nil
}

func (t *Telegram) Stop() error {
	select {
	case <-t.stop:
	default:
		close(t.stop)
	}
	if t.events != nil {
		t.bus.Unobserve(t.events)
		t.events = nil
	}
	if t.bot != nil {
		t.bot.Stop()
	}
	t.stopAllTyping()
	return nil
}

func (t *Telegram) handleMessage(c tele.Context) error {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil
	}

	chatID := msg.Chat.ID
	text := strings.TrimSpace(c.Text())
	src := t.source(chatID, msg.ThreadID)

	if text == "/new" {
		_ = t.bus.Pub(bus.Msg{
			Type:    bus.TypeSessionReset,
			From:    src,
			To:      bus.AddrAgentMain,
			Payload: src,
		})
		return c.Send("session reset. started a new session")
	}

	if text == "/cancel" {
		_ = t.bus.Pub(bus.Msg{
			Type:    bus.TypeInterrupt,
			From:    src,
			To:      bus.AddrAgentMain,
			Payload: "user cancelled",
		})
		t.stopTyping(chatID)
		return c.Send("cancelled current task")
	}

	t.touchActive(chatID)
	if command.IsCommand(text) && t.router != nil {
		ctx := bus.WithSource(context.Background(), src)
		out, ok, err := t.router.Route(ctx, text)
		if ok {
			if err != nil {
				out = "error: " + err.Error()
			}
			if strings.TrimSpace(out) == "" {
				out = "ok"
			}
			opts := &tele.SendOptions{}
			if msg.ThreadID != 0 {
				opts.ThreadID = msg.ThreadID
			}
			if msg.ID != 0 {
				opts.ReplyTo = &tele.Message{ID: msg.ID, Chat: msg.Chat}
			}
			t.sendTextWithOptions(chatID, out, opts)
			return nil
		}
	}
	mentioned := t.isMentioned(msg)
	if !t.shouldHandleMessage(msg, text, mentioned) {
		return nil
	}
	t.pushInboundState(src, inboundState{
		msgID:    msg.ID,
		threadID: msg.ThreadID,
	})
	t.applyReaction(src, chatID, "👀", msg.ID)

	payload := t.formatInboundPayload(msg, text, mentioned)
	t.debugf("inbound chat=%d msg=%d thread=%d mentioned=%v text=%q", chatID, msg.ID, msg.ThreadID, mentioned, truncateTG(text, 160))

	_ = t.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      t.routeTarget(text),
		Payload: payload,
	})
	t.startTyping(chatID)

	return nil
}

func (t *Telegram) forward(ctx context.Context) {
	if t.events != nil {
		t.bus.Unobserve(t.events)
	}
	ch := make(chan bus.Msg, 512)
	t.events = ch
	t.bus.Observe(ch)

	for {
		select {
		case m := <-ch:
			t.debugf("bus type=%s from=%s to=%s id=%s", m.Type, m.From, m.To, m.ID)
			if !strings.HasPrefix(m.To, "telegram:") && m.To != bus.AddrBroadcast {
				continue
			}
			t.sendMsg(m)
		case <-t.stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (t *Telegram) sendMsg(m bus.Msg) {
	targets := t.targetRoutes(m)
	if len(targets) == 0 {
		return
	}

	prefix := ""
	if m.From != "" && m.From != bus.AddrAgentMain {
		prefix = fmt.Sprintf("%s: ", m.From)
	}

	for _, target := range targets {
		chatID := parseTelegramChatID(target)
		if chatID == 0 {
			continue
		}
		t.touchActive(chatID)
		t.sendToChat(target, chatID, m, prefix)
	}
}

func (t *Telegram) targetRoutes(m bus.Msg) []string {
	if strings.HasPrefix(m.To, "telegram:") {
		return []string{m.To}
	}
	if m.To != bus.AddrBroadcast {
		return nil
	}

	now := time.Now()
	t.activeMu.Lock()
	defer t.activeMu.Unlock()

	routes := make([]string, 0, len(t.activeChats))
	for id, seen := range t.activeChats {
		if now.Sub(seen) > telegramActiveTTL {
			delete(t.activeChats, id)
			continue
		}
		routes = append(routes, bus.Telegram(id))
	}
	return routes
}

func (t *Telegram) sendToChat(route string, chatID int64, m bus.Msg, prefix string) {
	switch m.Type {
	case bus.TypeAssistant:
		raw := fmt.Sprintf("%v", m.Payload)
		out := parseTelegramAssistantOutput(raw)
		if strings.HasPrefix(strings.TrimSpace(out.Text), "[status] ") && out.Reaction == "" {
			status := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out.Text), "[status] "))
			if status != "" {
				t.setProgress(route, chatID, "[status] "+status)
			}
			return
		}
		t.clearProgress(route)
		replyToID := t.resolveReplyToID(route, out)
		t.debugf("assistant chat=%d reply_to=%d silent=%v react=%q text=%q", chatID, replyToID, out.Silent, out.Reaction, truncateTG(out.Text, 160))
		if out.Reaction != "" {
			t.applyReaction(route, chatID, out.Reaction, replyToID)
		}
		if out.Silent || strings.TrimSpace(out.Text) == "" {
			return
		}
		text := out.Text
		if prefix != "" {
			text = prefix + text
		}
		t.sendAssistantText(route, chatID, text, replyToID)
	case bus.TypeTurnDone:
		t.clearProgress(route)
		t.popInboundState(route)
		t.stopTyping(chatID)
		return
	case bus.TypeToolCall:
		t.handleToolCall(route, chatID)
		return
	case bus.TypeToolResult:
		t.handleToolResult(route, chatID)
		return
	case bus.TypeToolError:
		t.handleToolError(route, chatID)

	case bus.TypeAgentSpawn:
		if payload, ok := m.Payload.(map[string]string); ok {
			task := truncateTG(payload["task"], 100)
			t.sendText(chatID, fmt.Sprintf("spawned %s: %s", payload["agent_id"], task))
		}
	case bus.TypeAgentDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			t.sendText(chatID, fmt.Sprintf("completed %s: %s", payload["agent_id"], payload["result"]))
		}
	case bus.TypeTaskStart:
		if payload, ok := m.Payload.(map[string]string); ok {
			cmd := truncateTG(payload["cmd"], 50)
			t.sendText(chatID, fmt.Sprintf("task start %s: %s", payload["task_id"], cmd))
		}
	case bus.TypeTaskDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			if payload["status"] == "ok" {
				t.sendText(chatID, fmt.Sprintf("task done %s", payload["task_id"]))
			} else {
				t.sendText(chatID, fmt.Sprintf("task error %s: %s", payload["task_id"], truncateTG(payload["error"], 50)))
			}
		}
	case bus.TypeStreamChunk:
		t.handleStreamChunk(route, chatID, fmt.Sprintf("%v", m.Payload))
	case bus.TypeStreamEnd:
		t.handleStreamEnd(route)
		t.stopTyping(chatID)
	case bus.TypeSessionReset:
		t.clearProgress(route)
		t.stopTyping(chatID)
	case bus.TypeInterrupt:
		t.clearProgress(route)
		t.stopTyping(chatID)
	case bus.TypeSessionNew:
		if id, ok := m.Payload.(string); ok {
			t.sendText(chatID, fmt.Sprintf("session: %s", id))
		}
	case bus.TypeSessionCompact:
		t.sendText(chatID, fmt.Sprintf("session compact: %v", m.Payload))
	}
}
