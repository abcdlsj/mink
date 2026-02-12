package platform

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	tele "gopkg.in/telebot.v4"
)

const (
	telegramConfirmTimeout = 60 * time.Second
	telegramActiveTTL      = 30 * time.Minute
	telegramStreamFlush    = 400 * time.Millisecond
	telegramMsgLimit       = 3800
)

type confirmState struct {
	ch      chan bool
	created time.Time
}

type streamState struct {
	buf   strings.Builder
	msgID int
	dirty bool
	ended bool
}

type Telegram struct {
	token string
	bus   *bus.Bus
	bot   *tele.Bot
	stop  chan struct{}

	confirmMu sync.Mutex
	confirms  map[int64]map[string]confirmState

	streamMu sync.Mutex
	streams  map[int64]*streamState

	activeMu    sync.RWMutex
	activeChats map[int64]time.Time
}

func NewTelegram(token string, b *bus.Bus) *Telegram {
	return &Telegram{
		token:       token,
		bus:         b,
		stop:        make(chan struct{}),
		confirms:    make(map[int64]map[string]confirmState),
		streams:     make(map[int64]*streamState),
		activeChats: make(map[int64]time.Time),
	}
}

func (t *Telegram) ID() string { return "telegram" }

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
	log.Printf("[TG] Bot started: @%s", bot.Me.Username)

	bot.Handle(tele.OnText, func(c tele.Context) error {
		return t.handleMessage(c)
	})

	go bot.Start()
	go t.forward(ctx)
	go t.flushStreamLoop(ctx)

	return nil
}

func (t *Telegram) Stop() error {
	close(t.stop)
	if t.bot != nil {
		t.bot.Stop()
	}
	return nil
}

func (t *Telegram) handleMessage(c tele.Context) error {
	chatID := c.Chat().ID
	text := strings.TrimSpace(c.Text())

	handled, err := t.handleConfirmReply(c, chatID, text)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}

	src := bus.Telegram(chatID)

	if text == "/new" {
		_ = t.bus.Pub(bus.Msg{
			Type:    bus.TypeSessionReset,
			From:    src,
			To:      bus.AddrAgentMain,
			Payload: src,
		})
		return c.Send("[Session] Created new session")
	}

	t.touchActive(chatID)

	_ = t.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: text,
	})

	return nil
}

func (t *Telegram) forward(ctx context.Context) {
	ch := make(chan bus.Msg, 64)
	t.bus.Subscribe(bus.TypeAssistant, ch)
	t.bus.Subscribe(bus.TypeToolCall, ch)
	t.bus.Subscribe(bus.TypeToolResult, ch)
	t.bus.Subscribe(bus.TypeToolError, ch)
	t.bus.Subscribe(bus.TypeCommand, ch)
	t.bus.Subscribe(bus.TypeCommandOK, ch)
	t.bus.Subscribe(bus.TypeCommandError, ch)
	t.bus.Subscribe(bus.TypeAgentSpawn, ch)
	t.bus.Subscribe(bus.TypeAgentDone, ch)
	t.bus.Subscribe(bus.TypeTaskStart, ch)
	t.bus.Subscribe(bus.TypeTaskDone, ch)
	t.bus.Subscribe(bus.TypeStreamChunk, ch)
	t.bus.Subscribe(bus.TypeStreamEnd, ch)
	t.bus.Subscribe(bus.TypeSessionNew, ch)
	t.bus.Subscribe(bus.TypeSessionCompact, ch)

	for {
		select {
		case m := <-ch:
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
	chatIDs := t.getTargetChats(m)
	if len(chatIDs) == 0 {
		return
	}

	prefix := ""
	if m.From != "" && m.From != bus.AddrAgentMain {
		prefix = fmt.Sprintf("[%s] ", m.From)
	}

	for _, chatID := range chatIDs {
		t.touchActive(chatID)
		t.sendToChat(chatID, m, prefix)
	}
}

func (t *Telegram) getTargetChats(m bus.Msg) []int64 {
	if strings.HasPrefix(m.To, "telegram:") {
		chatID := parseTelegramChatID(m.To)
		if chatID != 0 {
			return []int64{chatID}
		}
	}

	if m.To == bus.AddrBroadcast {
		now := time.Now()
		t.activeMu.Lock()
		defer t.activeMu.Unlock()

		chats := make([]int64, 0, len(t.activeChats))
		for id, seen := range t.activeChats {
			if now.Sub(seen) > telegramActiveTTL {
				delete(t.activeChats, id)
				continue
			}
			chats = append(chats, id)
		}
		return chats
	}

	return nil
}

func (t *Telegram) sendToChat(chatID int64, m bus.Msg, prefix string) {
	switch m.Type {
	case bus.TypeAssistant:
		t.sendText(chatID, fmt.Sprintf("%s%v", prefix, m.Payload))
	case bus.TypeToolCall:
		t.sendText(chatID, fmt.Sprintf("[Tool] %s%v", prefix, m.Payload))
	case bus.TypeToolResult:
		t.sendText(chatID, fmt.Sprintf("[OK] %s%s", prefix, truncateTG(fmt.Sprintf("%v", m.Payload), 200)))
	case bus.TypeToolError:
		t.sendText(chatID, fmt.Sprintf("[ERR] %s%v", prefix, m.Payload))
	case bus.TypeCommand:
		t.sendText(chatID, fmt.Sprintf("$ %s%v", prefix, m.Payload))
	case bus.TypeCommandOK:
		t.sendText(chatID, fmt.Sprintf("[OK] %s%s", prefix, truncateTG(fmt.Sprintf("%v", m.Payload), 200)))
	case bus.TypeCommandError:
		t.sendText(chatID, fmt.Sprintf("[ERR] %s%v", prefix, m.Payload))
	case bus.TypeAgentSpawn:
		if payload, ok := m.Payload.(map[string]string); ok {
			task := truncateTG(payload["task"], 100)
			t.sendText(chatID, fmt.Sprintf("[Spawn] %s: %s", payload["agent_id"], task))
		}
	case bus.TypeAgentDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			t.sendText(chatID, fmt.Sprintf("[Done] %s %s", payload["agent_id"], payload["result"]))
		}
	case bus.TypeTaskStart:
		if payload, ok := m.Payload.(map[string]string); ok {
			cmd := truncateTG(payload["cmd"], 50)
			t.sendText(chatID, fmt.Sprintf("[Run] %s: %s", payload["task_id"], cmd))
		}
	case bus.TypeTaskDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			if payload["status"] == "ok" {
				t.sendText(chatID, fmt.Sprintf("[OK] %s completed", payload["task_id"]))
			} else {
				t.sendText(chatID, fmt.Sprintf("[ERR] %s: %s", payload["task_id"], truncateTG(payload["error"], 50)))
			}
		}
	case bus.TypeStreamChunk:
		t.handleStreamChunk(chatID, fmt.Sprintf("%v", m.Payload))
	case bus.TypeStreamEnd:
		t.handleStreamEnd(chatID)
	case bus.TypeSessionNew:
		if id, ok := m.Payload.(string); ok {
			t.sendText(chatID, fmt.Sprintf("[Session] %s", id))
		}
	case bus.TypeSessionCompact:
		t.sendText(chatID, fmt.Sprintf("[Compact] %v", m.Payload))
	}
}

func (t *Telegram) handleStreamChunk(chatID int64, delta string) {
	if delta == "" {
		return
	}

	t.streamMu.Lock()
	defer t.streamMu.Unlock()

	s, ok := t.streams[chatID]
	if !ok {
		s = &streamState{}
		t.streams[chatID] = s
	}
	s.buf.WriteString(delta)
	s.dirty = true
}

func (t *Telegram) handleStreamEnd(chatID int64) {
	t.streamMu.Lock()
	if s, ok := t.streams[chatID]; ok {
		s.ended = true
		s.dirty = true
	}
	t.streamMu.Unlock()

	t.flushStream(chatID, true)
}

func (t *Telegram) Allow(ctx context.Context, raw string) (bool, error) {
	src := cmd.SourceFrom(ctx)
	if !strings.HasPrefix(src, "telegram:") {
		return true, nil
	}

	chatID := parseTelegramChatID(src)
	if chatID == 0 {
		return false, fmt.Errorf("invalid chat id")
	}

	reqID := shortReqID()
	ch := make(chan bool, 1)
	t.confirmMu.Lock()
	if _, ok := t.confirms[chatID]; !ok {
		t.confirms[chatID] = make(map[string]confirmState)
	}
	t.confirms[chatID][reqID] = confirmState{ch: ch, created: time.Now()}
	t.confirmMu.Unlock()

	defer func() {
		t.confirmMu.Lock()
		if chatReqs, ok := t.confirms[chatID]; ok {
			delete(chatReqs, reqID)
			if len(chatReqs) == 0 {
				delete(t.confirms, chatID)
			}
		}
		t.confirmMu.Unlock()
	}()

	t.sendText(chatID, fmt.Sprintf("[Confirm %s] Execute:\n%s\nReply with: %s y | %s n", reqID, raw, reqID, reqID))

	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(telegramConfirmTimeout):
		t.sendText(chatID, fmt.Sprintf("[Confirm %s] timeout, cancelled", reqID))
		return false, nil
	}
}

func (t *Telegram) handleConfirmReply(c tele.Context, chatID int64, text string) (bool, error) {
	t.confirmMu.Lock()
	chatReqs := t.confirms[chatID]
	if len(chatReqs) == 0 {
		t.confirmMu.Unlock()
		return false, nil
	}

	now := time.Now()
	pending := make(map[string]confirmState, len(chatReqs))
	for id, state := range chatReqs {
		if now.Sub(state.created) > telegramConfirmTimeout {
			delete(chatReqs, id)
			continue
		}
		pending[id] = state
	}
	if len(chatReqs) == 0 {
		delete(t.confirms, chatID)
	}
	t.confirmMu.Unlock()

	if len(pending) == 0 {
		return false, nil
	}

	reqID, decision, ok := parseConfirmReply(text, pending)
	if !ok {
		var ids []string
		for id := range pending {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return true, c.Send(fmt.Sprintf("[Confirm] pending requests: %s\nReply: <id> y|n", strings.Join(ids, ", ")))
	}

	t.confirmMu.Lock()
	chatReqs = t.confirms[chatID]
	if len(chatReqs) == 0 {
		t.confirmMu.Unlock()
		return true, c.Send("[Confirm] request not found or expired")
	}
	state, exists := chatReqs[reqID]
	if exists {
		delete(chatReqs, reqID)
		if len(chatReqs) == 0 {
			delete(t.confirms, chatID)
		}
	}
	t.confirmMu.Unlock()

	if !exists {
		return true, c.Send("[Confirm] request not found or expired")
	}

	select {
	case state.ch <- decision:
	default:
	}

	if decision {
		return true, c.Send(fmt.Sprintf("[Confirm %s] approved", reqID))
	}
	return true, c.Send(fmt.Sprintf("[Confirm %s] cancelled", reqID))
}

func parseConfirmReply(text string, pending map[string]confirmState) (string, bool, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(fields) == 0 {
		return "", false, false
	}

	decision, hasDecision := false, false
	reqID := ""

	for _, f := range fields {
		switch f {
		case "y", "yes", "ok", "allow":
			decision = true
			hasDecision = true
		case "n", "no", "deny", "cancel":
			decision = false
			hasDecision = true
		default:
			candidate := strings.TrimPrefix(f, "#")
			if _, ok := pending[candidate]; ok {
				reqID = candidate
			}
		}
	}

	if !hasDecision {
		return "", false, false
	}

	if reqID == "" && len(pending) == 1 {
		for id := range pending {
			reqID = id
			break
		}
	}

	if reqID == "" {
		return "", false, false
	}

	return reqID, decision, true
}

func (t *Telegram) flushStreamLoop(ctx context.Context) {
	ticker := time.NewTicker(telegramStreamFlush)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.flushStreams(false)
		case <-t.stop:
			t.flushStreams(true)
			return
		case <-ctx.Done():
			t.flushStreams(true)
			return
		}
	}
}

func (t *Telegram) flushStreams(force bool) {
	t.streamMu.Lock()
	chatIDs := make([]int64, 0, len(t.streams))
	for chatID := range t.streams {
		chatIDs = append(chatIDs, chatID)
	}
	t.streamMu.Unlock()

	for _, chatID := range chatIDs {
		t.flushStream(chatID, force)
	}
}

func (t *Telegram) flushStream(chatID int64, force bool) {
	t.streamMu.Lock()
	s, ok := t.streams[chatID]
	if !ok {
		t.streamMu.Unlock()
		return
	}
	if !force && !s.dirty && !s.ended {
		t.streamMu.Unlock()
		return
	}
	text := s.buf.String()
	msgID := s.msgID
	ended := s.ended
	s.dirty = false
	t.streamMu.Unlock()

	parts := splitText(text, telegramMsgLimit)
	if len(parts) == 0 {
		if ended {
			t.streamMu.Lock()
			delete(t.streams, chatID)
			t.streamMu.Unlock()
		}
		return
	}

	chat := &tele.Chat{ID: chatID}
	first := parts[0]
	if msgID == 0 {
		sent, err := t.bot.Send(chat, first)
		if err != nil {
			log.Printf("[TG] stream send error: %v", err)
		} else {
			msgID = sent.ID
		}
	} else {
		if _, err := t.bot.Edit(&tele.Message{ID: msgID, Chat: chat}, first); err != nil {
			log.Printf("[TG] stream edit error: %v", err)
		}
	}

	t.streamMu.Lock()
	if s, ok := t.streams[chatID]; ok {
		s.msgID = msgID
	}
	t.streamMu.Unlock()

	if ended {
		for _, part := range parts[1:] {
			t.sendText(chatID, part)
		}
		t.streamMu.Lock()
		delete(t.streams, chatID)
		t.streamMu.Unlock()
	}
}

func (t *Telegram) sendText(chatID int64, text string) {
	chat := &tele.Chat{ID: chatID}
	parts := splitText(text, telegramMsgLimit)
	for _, part := range parts {
		if _, err := t.bot.Send(chat, part); err != nil {
			log.Printf("[TG] Send error: %v", err)
			return
		}
	}
}

func splitText(s string, limit int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	if limit <= 0 || len(r) <= limit {
		return []string{s}
	}

	parts := make([]string, 0, (len(r)/limit)+1)
	for len(r) > 0 {
		n := limit
		if len(r) < n {
			n = len(r)
		}
		parts = append(parts, string(r[:n]))
		r = r[n:]
	}
	return parts
}

func (t *Telegram) touchActive(chatID int64) {
	t.activeMu.Lock()
	t.activeChats[chatID] = time.Now()
	t.activeMu.Unlock()
}

func parseTelegramChatID(src string) int64 {
	var chatID int64
	_, _ = fmt.Sscanf(strings.TrimPrefix(src, "telegram:"), "%d", &chatID)
	return chatID
}

func shortReqID() string {
	val := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(val) > 6 {
		return val[len(val)-6:]
	}
	return val
}

func truncateTG(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
