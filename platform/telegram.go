package platform

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/tool"
	tele "gopkg.in/telebot.v4"
)

const (
	telegramConfirmTimeout = 60 * time.Second
	telegramActiveTTL      = 30 * time.Minute
	telegramStreamFlush    = 400 * time.Millisecond
	telegramTypingRefresh  = 2 * time.Second
	telegramMsgLimit       = 3800
	confirmCallbackPrefix  = "mcfm"
)

type confirmState struct {
	ch      chan tool.Approval
	created time.Time
}

type streamState struct {
	buf   strings.Builder
	msgID int
	dirty bool
	ended bool
	flush bool
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
	token string
	bus   *bus.Bus
	bot   *tele.Bot
	stop  chan struct{}

	confirmMu sync.Mutex
	confirms  map[int64]map[string]confirmState

	streamMu sync.Mutex
	streams  map[int64]*streamState

	inboundMu sync.RWMutex
	inbound   map[int64]inboundState

	assistMu sync.Mutex
	assist   map[int64]assistantOutState

	activeMu    sync.RWMutex
	activeChats map[int64]time.Time

	typingMu sync.Mutex
	typing   map[int64]chan struct{}
	typingN  map[int64]int
}

func NewTelegram(token string, b *bus.Bus) *Telegram {
	return &Telegram{
		token:       token,
		bus:         b,
		stop:        make(chan struct{}),
		confirms:    make(map[int64]map[string]confirmState),
		streams:     make(map[int64]*streamState),
		inbound:     make(map[int64]inboundState),
		assist:      make(map[int64]assistantOutState),
		activeChats: make(map[int64]time.Time),
		typing:      make(map[int64]chan struct{}),
		typingN:     make(map[int64]int),
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
	close(t.stop)
	if t.bot != nil {
		t.bot.Stop()
	}
	return nil
}

func (t *Telegram) handleMessage(c tele.Context) error {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil
	}

	chatID := msg.Chat.ID
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
		return c.Send("session reset. started a new session")
	}

	t.touchActive(chatID)
	mentioned := t.isMentioned(msg)
	t.setInboundState(chatID, inboundState{
		msgID:    msg.ID,
		threadID: msg.ThreadID,
	})

	payload := t.formatInboundPayload(msg, text, mentioned)
	log.Printf("[TGDBG] inbound chat=%d msg=%d thread=%d mentioned=%v text=%q", chatID, msg.ID, msg.ThreadID, mentioned, truncateTG(text, 160))

	_ = t.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      bus.AddrAgentMain,
		Payload: payload,
	})
	t.startTyping(chatID)

	return nil
}

func (t *Telegram) forward(ctx context.Context) {
	ch := make(chan bus.Msg, 64)
	t.bus.Subscribe(bus.TypeAssistant, ch)
	t.bus.Subscribe(bus.TypeTurnDone, ch)
	t.bus.Subscribe(bus.TypeToolCall, ch)
	t.bus.Subscribe(bus.TypeToolResult, ch)
	t.bus.Subscribe(bus.TypeToolError, ch)
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
			log.Printf("[TGDBG] bus type=%s from=%s to=%s id=%s", m.Type, m.From, m.To, m.ID)
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
		prefix = fmt.Sprintf("%s: ", m.From)
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
		raw := fmt.Sprintf("%v", m.Payload)
		out := parseTelegramAssistantOutput(raw)
		replyToID := t.resolveReplyToID(chatID, out)
		log.Printf("[TGDBG] assistant chat=%d reply_to=%d silent=%v react=%q text=%q", chatID, replyToID, out.Silent, out.Reaction, truncateTG(out.Text, 160))
		if out.Reaction != "" {
			t.applyReaction(chatID, out.Reaction, replyToID)
		}
		if out.Silent || strings.TrimSpace(out.Text) == "" {
			return
		}
		text := out.Text
		if prefix != "" {
			text = prefix + text
		}
		t.sendAssistantText(chatID, text, replyToID)
	case bus.TypeTurnDone:
		t.stopTyping(chatID)
		return
	case bus.TypeToolCall:
		return
	case bus.TypeToolResult:
		return
	case bus.TypeToolError:
		t.sendText(chatID, fmt.Sprintf("tool error: %s%v", prefix, m.Payload))
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
		t.handleStreamChunk(chatID, fmt.Sprintf("%v", m.Payload))
	case bus.TypeStreamEnd:
		t.handleStreamEnd(chatID)
	case bus.TypeSessionNew:
		if id, ok := m.Payload.(string); ok {
			t.sendText(chatID, fmt.Sprintf("session: %s", id))
		}
	case bus.TypeSessionCompact:
		t.sendText(chatID, fmt.Sprintf("session compact: %v", m.Payload))
	}
}

func (t *Telegram) handleStreamChunk(chatID int64, delta string) {
	if delta == "" {
		return
	}
	log.Printf("[TGDBG] stream chunk chat=%d size=%d", chatID, len([]rune(delta)))
	t.notifyTyping(chatID)

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
	log.Printf("[TGDBG] stream end chat=%d", chatID)
	t.streamMu.Lock()
	if s, ok := t.streams[chatID]; ok {
		s.ended = true
		s.dirty = true
	}
	t.streamMu.Unlock()

	t.flushStream(chatID, true)
}

func (t *Telegram) Approve(ctx context.Context, raw string) (tool.Approval, error) {
	src := bus.SourceFrom(ctx)
	if !strings.HasPrefix(src, "telegram:") {
		return tool.AllowOnce, nil
	}

	chatID := parseTelegramChatID(src)
	if chatID == 0 {
		return tool.Denied, fmt.Errorf("invalid chat id")
	}

	reqID := shortReqID()
	ch := make(chan tool.Approval, 1)
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

	t.sendConfirmRequest(chatID, reqID, raw)

	select {
	case approval := <-ch:
		return approval, nil
	case <-ctx.Done():
		return tool.Denied, ctx.Err()
	case <-time.After(telegramConfirmTimeout):
		t.sendText(chatID, fmt.Sprintf("confirm %s timed out, cancelled", reqID))
		return tool.Denied, nil
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

	reqID, approval, ok := parseConfirmReply(text, pending)
	if !ok {
		var ids []string
		for id := range pending {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return true, c.Send(fmt.Sprintf("pending confirmations: %s\nreply: <id> y|a|n", strings.Join(ids, ", ")))
	}

	state, exists := t.popConfirmState(chatID, reqID)

	if !exists {
		return true, c.Send("confirmation request not found or expired")
	}

	select {
	case state.ch <- approval:
	default:
	}

	switch approval {
	case tool.AllowAlways:
		return true, c.Send(fmt.Sprintf("confirm %s always allowed", reqID))
	case tool.AllowOnce:
		return true, c.Send(fmt.Sprintf("confirm %s approved", reqID))
	default:
		return true, c.Send(fmt.Sprintf("confirm %s cancelled", reqID))
	}
}

func (t *Telegram) handleConfirmCallback(c tele.Context) (bool, error) {
	cb := c.Callback()
	if cb == nil {
		return false, nil
	}

	reqID, approval, ok := parseConfirmCallback(cb.Data)
	if !ok {
		return false, nil
	}

	chatID := callbackChatID(c)
	if chatID == 0 {
		_ = c.Respond(&tele.CallbackResponse{Text: "confirmation chat not found", ShowAlert: true})
		return true, nil
	}

	state, exists := t.popConfirmState(chatID, reqID)
	if !exists {
		_ = c.Respond(&tele.CallbackResponse{Text: "confirmation request not found or expired", ShowAlert: true})
		return true, nil
	}

	select {
	case state.ch <- approval:
	default:
	}

	var msg string
	switch approval {
	case tool.AllowAlways:
		msg = fmt.Sprintf("confirm %s always allowed", reqID)
	case tool.AllowOnce:
		msg = fmt.Sprintf("confirm %s approved", reqID)
	default:
		msg = fmt.Sprintf("confirm %s cancelled", reqID)
	}
	_ = c.Respond(&tele.CallbackResponse{Text: msg})
	t.sendText(chatID, msg)
	return true, nil
}

func (t *Telegram) popConfirmState(chatID int64, reqID string) (confirmState, bool) {
	t.confirmMu.Lock()
	defer t.confirmMu.Unlock()

	chatReqs := t.confirms[chatID]
	if len(chatReqs) == 0 {
		return confirmState{}, false
	}

	state, ok := chatReqs[reqID]
	if ok {
		delete(chatReqs, reqID)
		if len(chatReqs) == 0 {
			delete(t.confirms, chatID)
		}
	}

	return state, ok
}

func parseConfirmCallback(data string) (string, tool.Approval, bool) {
	parts := strings.Split(strings.TrimSpace(data), ":")
	if len(parts) != 3 || parts[0] != confirmCallbackPrefix {
		return "", tool.Denied, false
	}

	reqID := strings.TrimSpace(parts[2])
	if reqID == "" {
		return "", tool.Denied, false
	}

	switch strings.ToLower(strings.TrimSpace(parts[1])) {
	case "y":
		return reqID, tool.AllowOnce, true
	case "a":
		return reqID, tool.AllowAlways, true
	case "n":
		return reqID, tool.Denied, true
	default:
		return "", tool.Denied, false
	}
}

func callbackChatID(c tele.Context) int64 {
	if chat := c.Chat(); chat != nil {
		return chat.ID
	}
	cb := c.Callback()
	if cb == nil || cb.Message == nil || cb.Message.Chat == nil {
		return 0
	}
	return cb.Message.Chat.ID
}

func (t *Telegram) sendConfirmRequest(chatID int64, reqID, raw string) {
	text := fmt.Sprintf("confirm %s\nexecute:\n%s\nchoose: tap button or reply: %s y|a|n", reqID, raw, reqID)
	opts := t.assistantSendOptions(chatID, 0, false)
	if opts == nil {
		opts = &tele.SendOptions{}
	} else {
		opts = cloneSendOptions(opts)
	}
	opts.ReplyMarkup = confirmMarkup(reqID)
	t.sendTextWithOptions(chatID, text, opts)
}

func confirmMarkup(reqID string) *tele.ReplyMarkup {
	allow := tele.InlineButton{Text: "Approve", Data: confirmCallbackPrefix + ":y:" + reqID}
	always := tele.InlineButton{Text: "Always Allow", Data: confirmCallbackPrefix + ":a:" + reqID}
	deny := tele.InlineButton{Text: "Deny", Data: confirmCallbackPrefix + ":n:" + reqID}
	return &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{allow, always, deny}}}
}

func parseConfirmReply(text string, pending map[string]confirmState) (string, tool.Approval, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(fields) == 0 {
		return "", tool.Denied, false
	}

	var approval tool.Approval
	hasDecision := false
	reqID := ""

	for _, f := range fields {
		switch f {
		case "y", "yes", "ok", "allow":
			approval = tool.AllowOnce
			hasDecision = true
		case "a", "always":
			approval = tool.AllowAlways
			hasDecision = true
		case "n", "no", "deny", "cancel":
			approval = tool.Denied
			hasDecision = true
		default:
			candidate := strings.TrimPrefix(f, "#")
			if _, ok := pending[candidate]; ok {
				reqID = candidate
			}
		}
	}

	if !hasDecision {
		return "", tool.Denied, false
	}

	if reqID == "" && len(pending) == 1 {
		for id := range pending {
			reqID = id
			break
		}
	}

	if reqID == "" {
		return "", tool.Denied, false
	}

	return reqID, approval, true
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
	for {
		t.streamMu.Lock()
		s, ok := t.streams[chatID]
		if !ok {
			t.streamMu.Unlock()
			return
		}
		if s.flush {
			log.Printf("[TGDBG] skip flush chat=%d force=%v reason=already_flushing", chatID, force)
			t.streamMu.Unlock()
			return
		}
		if !s.dirty {
			t.streamMu.Unlock()
			return
		}
		s.flush = true
		text := s.buf.String()
		msgID := s.msgID
		ended := s.ended
		s.dirty = false
		t.streamMu.Unlock()
		log.Printf("[TGDBG] flush stream chat=%d force=%v ended=%v msg_id=%d text_size=%d", chatID, force, ended, msgID, len([]rune(text)))

		out := tgAssistantOut{Text: text}
		replyToID := 0
		if ended {
			out = parseTelegramAssistantOutput(text)
			replyToID = t.resolveReplyToID(chatID, out)
			if out.Reaction != "" {
				t.applyReaction(chatID, out.Reaction, replyToID)
			}
		}

		if ended && (out.Silent || strings.TrimSpace(out.Text) == "") {
			if msgID != 0 {
				if err := t.bot.Delete(&tele.Message{ID: msgID, Chat: &tele.Chat{ID: chatID}}); err != nil {
					log.Printf("[TG] stream delete error: %v", err)
				}
			}
			t.streamMu.Lock()
			if cur, ok := t.streams[chatID]; ok {
				cur.flush = false
				delete(t.streams, chatID)
			}
			t.streamMu.Unlock()
			return
		}

		parts := splitText(out.Text, telegramMsgLimit)
		if len(parts) == 0 {
			if ended {
				t.streamMu.Lock()
				if cur, ok := t.streams[chatID]; ok {
					cur.flush = false
					delete(t.streams, chatID)
				}
				t.streamMu.Unlock()
			}
			t.streamMu.Lock()
			if cur, ok := t.streams[chatID]; ok {
				cur.flush = false
			}
			t.streamMu.Unlock()
			return
		}

		chat := &tele.Chat{ID: chatID}
		first := parts[0]
		if msgID == 0 {
			sendOpts := t.assistantSendOptions(chatID, replyToID, true)
			sent, err := t.sendWithOpts(chat, first, sendOpts)
			if err != nil {
				log.Printf("[TG] stream send error: %v", err)
			} else {
				msgID = sent.ID
			}
		} else {
			if _, err := t.bot.Edit(&tele.Message{ID: msgID, Chat: chat}, tgRenderText(first), &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
				log.Printf("[TG] stream edit error: %v", err)
			}
		}

		t.streamMu.Lock()
		shouldContinue := false
		if s, ok := t.streams[chatID]; ok {
			s.msgID = msgID
			if !ended && s.dirty {
				shouldContinue = true
			}
			s.flush = false
		}
		t.streamMu.Unlock()

		if ended {
			threadOpts := t.assistantSendOptions(chatID, 0, false)
			for _, part := range parts[1:] {
				t.sendTextWithOptions(chatID, part, threadOpts)
			}
			t.streamMu.Lock()
			delete(t.streams, chatID)
			t.streamMu.Unlock()
			return
		}

		if !shouldContinue {
			return
		}
	}
}

func (t *Telegram) sendAssistantText(chatID int64, text string, replyToID int) {
	log.Printf("[TGDBG] send assistant chat=%d reply_to=%d text=%q", chatID, replyToID, truncateTG(text, 160))
	if t.isDuplicateAssistant(chatID, text, replyToID) {
		log.Printf("[TGDBG] duplicate suppressed chat=%d reply_to=%d text=%q", chatID, replyToID, truncateTG(text, 160))
		return
	}
	t.notifyTyping(chatID)

	parts := splitText(text, telegramMsgLimit)
	if len(parts) == 0 {
		return
	}

	replyOpts := t.assistantSendOptions(chatID, replyToID, true)
	threadOpts := t.assistantSendOptions(chatID, 0, false)
	for i, part := range parts {
		opts := threadOpts
		if i == 0 {
			opts = replyOpts
		}
		t.sendTextWithOptions(chatID, part, opts)
	}
}

func (t *Telegram) sendText(chatID int64, text string) {
	t.sendTextWithOptions(chatID, text, nil)
}

func (t *Telegram) sendTextWithOptions(chatID int64, text string, opts *tele.SendOptions) {
	chat := &tele.Chat{ID: chatID}
	parts := splitText(text, telegramMsgLimit)
	for _, part := range parts {
		if _, err := t.sendWithOpts(chat, part, opts); err != nil {
			log.Printf("[TG] Send error: %v", err)
			return
		}
	}
}

func (t *Telegram) sendWithOpts(chat *tele.Chat, text string, opts *tele.SendOptions) (*tele.Message, error) {
	replyTo := 0
	threadID := 0
	if opts != nil {
		threadID = opts.ThreadID
		if opts.ReplyTo != nil {
			replyTo = opts.ReplyTo.ID
		}
	}

	text = tgRenderText(text)
	if opts == nil {
		opts = &tele.SendOptions{}
	} else {
		opts = cloneSendOptions(opts)
	}
	if opts.ParseMode == tele.ModeDefault {
		opts.ParseMode = tele.ModeHTML
	}
	msg, err := t.bot.Send(chat, text, opts)
	if err != nil {
		log.Printf("[TGDBG] send error chat=%d thread=%d reply_to=%d err=%v text=%q", chat.ID, threadID, replyTo, err, truncateTG(text, 120))
		return nil, err
	}
	log.Printf("[TGDBG] sent chat=%d msg=%d thread=%d reply_to=%d text=%q", chat.ID, msg.ID, threadID, replyTo, truncateTG(text, 120))
	return msg, nil
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
		n := min(limit, len(r))
		parts = append(parts, string(r[:n]))
		r = r[n:]
	}
	return parts
}

func cloneSendOptions(opts *tele.SendOptions) *tele.SendOptions {
	if opts == nil {
		return nil
	}
	cp := *opts
	return &cp
}

func (t *Telegram) touchActive(chatID int64) {
	t.activeMu.Lock()
	t.activeChats[chatID] = time.Now()
	t.activeMu.Unlock()
}

func (t *Telegram) notifyTyping(chatID int64) {
	if t.bot == nil {
		return
	}

	chat := &tele.Chat{ID: chatID}
	st, ok := t.getInboundState(chatID)
	if ok && st.threadID != 0 {
		if err := t.bot.Notify(chat, tele.Typing, st.threadID); err != nil {
			log.Printf("[TGDBG] notify typing error chat=%d thread=%d err=%v", chatID, st.threadID, err)
		}
		return
	}
	if err := t.bot.Notify(chat, tele.Typing); err != nil {
		log.Printf("[TGDBG] notify typing error chat=%d err=%v", chatID, err)
	}
}

func (t *Telegram) startTyping(chatID int64) {
	t.typingMu.Lock()
	t.typingN[chatID]++
	if _, ok := t.typing[chatID]; ok {
		t.typingMu.Unlock()
		t.notifyTyping(chatID)
		return
	}

	ch := make(chan struct{})
	t.typing[chatID] = ch
	t.typingMu.Unlock()
	t.notifyTyping(chatID)

	go func() {
		ticker := time.NewTicker(telegramTypingRefresh)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.notifyTyping(chatID)
			case <-ch:
				return
			case <-t.stop:
				return
			}
		}
	}()
}

func (t *Telegram) stopTyping(chatID int64) {
	t.typingMu.Lock()
	if n := t.typingN[chatID]; n > 1 {
		t.typingN[chatID] = n - 1
		t.typingMu.Unlock()
		return
	}
	delete(t.typingN, chatID)

	ch, ok := t.typing[chatID]
	if ok {
		delete(t.typing, chatID)
	}
	t.typingMu.Unlock()

	if ok {
		close(ch)
	}
}

func (t *Telegram) setInboundState(chatID int64, s inboundState) {
	t.inboundMu.Lock()
	t.inbound[chatID] = s
	t.inboundMu.Unlock()
}

func (t *Telegram) getInboundState(chatID int64) (inboundState, bool) {
	t.inboundMu.RLock()
	defer t.inboundMu.RUnlock()
	s, ok := t.inbound[chatID]
	return s, ok
}

func (t *Telegram) applyReaction(chatID int64, emoji string, replyToID int) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || t.bot == nil {
		return
	}
	msgID := replyToID
	st, ok := t.getInboundState(chatID)
	if !ok {
		return
	}
	if msgID == 0 {
		msgID = st.msgID
	}
	if msgID == 0 {
		return
	}

	chat := &tele.Chat{ID: chatID}
	err := t.bot.React(chat, &tele.Message{ID: msgID, Chat: chat}, tele.Reactions{
		Reactions: []tele.Reaction{{Type: tele.ReactionTypeEmoji, Emoji: emoji}},
	})
	if err != nil {
		log.Printf("[TG] react error: %v", err)
		return
	}
	log.Printf("[TGDBG] reacted chat=%d msg=%d emoji=%s", chatID, msgID, emoji)
}

func (t *Telegram) assistantSendOptions(chatID int64, replyToID int, withReply bool) *tele.SendOptions {
	st, ok := t.getInboundState(chatID)
	if !ok {
		return nil
	}

	opts := &tele.SendOptions{}
	has := false
	if st.threadID != 0 {
		opts.ThreadID = st.threadID
		has = true
	}
	if withReply && (replyToID != 0 || st.msgID != 0) {
		target := replyToID
		if target == 0 {
			target = st.msgID
		}
		opts.ReplyTo = &tele.Message{ID: target, Chat: &tele.Chat{ID: chatID}}
		has = true
	}
	if !has {
		return nil
	}
	return opts
}

func (t *Telegram) resolveReplyToID(chatID int64, out tgAssistantOut) int {
	if out.ReplyToID > 0 {
		return out.ReplyToID
	}
	st, ok := t.getInboundState(chatID)
	if !ok {
		return 0
	}
	if out.ReplyNow {
		return st.msgID
	}
	return st.msgID
}

func (t *Telegram) isDuplicateAssistant(chatID int64, text string, replyToID int) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	now := time.Now()
	t.assistMu.Lock()
	defer t.assistMu.Unlock()

	st, ok := t.assist[chatID]
	if ok && st.text == text && st.replyToID == replyToID && now.Sub(st.at) < 3*time.Second {
		log.Printf("[TGDBG] dedupe hit chat=%d reply_to=%d age_ms=%d", chatID, replyToID, now.Sub(st.at).Milliseconds())
		return true
	}

	t.assist[chatID] = assistantOutState{text: text, replyToID: replyToID, at: now}
	return false
}

func (t *Telegram) isMentioned(msg *tele.Message) bool {
	if msg == nil {
		return false
	}
	if t.bot != nil && t.bot.Me != nil {
		if msg.ReplyTo != nil && msg.ReplyTo.Sender != nil && msg.ReplyTo.Sender.ID == t.bot.Me.ID {
			return true
		}
		for _, e := range msg.Entities {
			if e.Type == tele.EntityTMention && e.User != nil && e.User.ID == t.bot.Me.ID {
				return true
			}
		}
		name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t.bot.Me.Username), "@"))
		if name != "" {
			needle := "@" + name
			if strings.Contains(strings.ToLower(msg.Text), needle) || strings.Contains(strings.ToLower(msg.Caption), needle) {
				return true
			}
		}
	}
	return false
}

func (t *Telegram) formatInboundPayload(msg *tele.Message, text string, mentioned bool) string {
	if msg == nil || msg.Chat == nil {
		return text
	}

	var b strings.Builder
	b.WriteString("[telegram_context]\n")
	b.WriteString("chat_id: ")
	b.WriteString(strconv.FormatInt(msg.Chat.ID, 10))
	b.WriteByte('\n')
	b.WriteString("chat_type: ")
	b.WriteString(string(msg.Chat.Type))
	b.WriteByte('\n')
	if title := oneLine(msg.Chat.Title); title != "" {
		b.WriteString("chat_title: ")
		b.WriteString(title)
		b.WriteByte('\n')
	}
	b.WriteString("message_id: ")
	b.WriteString(strconv.Itoa(msg.ID))
	b.WriteByte('\n')
	if msg.ThreadID != 0 {
		b.WriteString("thread_id: ")
		b.WriteString(strconv.Itoa(msg.ThreadID))
		b.WriteByte('\n')
	}
	if sender := oneLine(senderDisplay(msg.Sender)); sender != "" {
		b.WriteString("sender: ")
		b.WriteString(sender)
		b.WriteByte('\n')
	}
	if msg.Sender != nil {
		if u := oneLine(msg.Sender.Username); u != "" {
			b.WriteString("sender_username: @")
			b.WriteString(u)
			b.WriteByte('\n')
		}
	}
	b.WriteString("was_mentioned: ")
	if mentioned {
		b.WriteString("true\n")
	} else {
		b.WriteString("false\n")
	}
	if msg.ReplyTo != nil {
		b.WriteString("reply_to_message_id: ")
		b.WriteString(strconv.Itoa(msg.ReplyTo.ID))
		b.WriteByte('\n')
		if rs := oneLine(senderDisplay(msg.ReplyTo.Sender)); rs != "" {
			b.WriteString("reply_to_sender: ")
			b.WriteString(rs)
			b.WriteByte('\n')
		}
	}
	b.WriteString("[/telegram_context]\n\n")
	b.WriteString(text)
	return b.String()
}

func senderDisplay(u *tele.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName + " " + u.LastName))
	if name != "" {
		if un := strings.TrimSpace(u.Username); un != "" {
			return name + " (@" + un + ")"
		}
		return name
	}
	if un := strings.TrimSpace(u.Username); un != "" {
		return "@" + un
	}
	return strconv.FormatInt(u.ID, 10)
}

func oneLine(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\r", " ")
	return strings.TrimSpace(v)
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
