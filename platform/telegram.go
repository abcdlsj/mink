package platform

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
	tele "gopkg.in/telebot.v4"
)

type Telegram struct {
	token string
	bus   *bus.Bus
	bot   *tele.Bot
	stop  chan struct{}

	confirmMu sync.Mutex
	confirms  map[int64]chan bool

	streamMu  sync.Mutex
	streamBuf map[int64]*strings.Builder
	streamMsg map[int64]int

	activeMu    sync.RWMutex
	activeChats map[int64]bool
}

func NewTelegram(token string, b *bus.Bus) *Telegram {
	return &Telegram{
		token:       token,
		bus:         b,
		stop:        make(chan struct{}),
		confirms:    make(map[int64]chan bool),
		streamBuf:   make(map[int64]*strings.Builder),
		streamMsg:   make(map[int64]int),
		activeChats: make(map[int64]bool),
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
	text := c.Text()

	t.confirmMu.Lock()
	ch := t.confirms[chatID]
	t.confirmMu.Unlock()

	if ch != nil {
		ans := strings.ToLower(strings.TrimSpace(text))
		ok := ans == "y" || ans == "yes"
		select {
		case ch <- ok:
		default:
		}
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

	t.activeMu.Lock()
	t.activeChats[chatID] = true
	t.activeMu.Unlock()

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
		t.sendToChat(chatID, m, prefix)
	}
}

func (t *Telegram) getTargetChats(m bus.Msg) []int64 {
	if strings.HasPrefix(m.To, "telegram:") {
		var chatID int64
		fmt.Sscanf(strings.TrimPrefix(m.To, "telegram:"), "%d", &chatID)
		if chatID != 0 {
			return []int64{chatID}
		}
	}

	// 广播给所有活跃用户
	if m.To == bus.AddrBroadcast {
		t.activeMu.RLock()
		defer t.activeMu.RUnlock()

		chats := make([]int64, 0, len(t.activeChats))
		for id := range t.activeChats {
			chats = append(chats, id)
		}
		return chats
	}

	return nil
}

func (t *Telegram) sendToChat(chatID int64, m bus.Msg, prefix string) {
	chat := &tele.Chat{ID: chatID}

	switch m.Type {
	case bus.TypeAssistant:
		_, err := t.bot.Send(chat, fmt.Sprintf("%s%s", prefix, m.Payload))
		if err != nil {
			log.Printf("[TG] Send error: %v", err)
		}
	case bus.TypeToolCall:
		t.bot.Send(chat, fmt.Sprintf("[Tool] %s%s", prefix, m.Payload))
	case bus.TypeToolResult:
		t.bot.Send(chat, fmt.Sprintf("[OK] %s%s", prefix, truncate(fmt.Sprintf("%v", m.Payload), 200)))
	case bus.TypeToolError:
		t.bot.Send(chat, fmt.Sprintf("[ERR] %s%s", prefix, m.Payload))
	case bus.TypeCommand:
		t.bot.Send(chat, fmt.Sprintf("$ %s%s", prefix, m.Payload))
	case bus.TypeCommandOK:
		t.bot.Send(chat, fmt.Sprintf("[OK] %s%s", prefix, truncate(fmt.Sprintf("%v", m.Payload), 200)))
	case bus.TypeCommandError:
		t.bot.Send(chat, fmt.Sprintf("[ERR] %s%s", prefix, m.Payload))
	case bus.TypeAgentSpawn:
		if payload, ok := m.Payload.(map[string]string); ok {
			task := truncate(payload["task"], 100)
			t.bot.Send(chat, fmt.Sprintf("[Spawn] %s: %s", payload["agent_id"], task))
		}
	case bus.TypeAgentDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			t.bot.Send(chat, fmt.Sprintf("[Done] %s %s", payload["agent_id"], payload["result"]))
		}
	case bus.TypeTaskStart:
		if payload, ok := m.Payload.(map[string]string); ok {
			cmd := truncate(payload["cmd"], 50)
			t.bot.Send(chat, fmt.Sprintf("[Run] %s: %s", payload["task_id"], cmd))
		}
	case bus.TypeTaskDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			if payload["status"] == "ok" {
				t.bot.Send(chat, fmt.Sprintf("[OK] %s completed", payload["task_id"]))
			} else {
				t.bot.Send(chat, fmt.Sprintf("[ERR] %s: %s", payload["task_id"], truncate(payload["error"], 50)))
			}
		}
	case bus.TypeStreamChunk:
		t.handleStreamChunk(chatID, m)
	case bus.TypeStreamEnd:
		t.handleStreamEnd(chatID)
	case bus.TypeSessionNew:
		if id, ok := m.Payload.(string); ok {
			t.bot.Send(chat, fmt.Sprintf("[Session] %s", id))
		}
	}
}

func (t *Telegram) handleStreamChunk(chatID int64, m bus.Msg) {
	delta, _ := m.Payload.(string)

	t.streamMu.Lock()
	defer t.streamMu.Unlock()

	buf, ok := t.streamBuf[chatID]
	if !ok {
		buf = &strings.Builder{}
		t.streamBuf[chatID] = buf
	}
	buf.WriteString(delta)
}

func (t *Telegram) handleStreamEnd(chatID int64) {
	t.streamMu.Lock()
	defer t.streamMu.Unlock()

	if buf, ok := t.streamBuf[chatID]; ok {
		text := buf.String()
		chat := &tele.Chat{ID: chatID}

		if msgID, exists := t.streamMsg[chatID]; exists {
			t.bot.Edit(&tele.Message{ID: msgID, Chat: chat}, text)
		} else if text != "" {
			sent, err := t.bot.Send(chat, text)
			if err == nil {
				t.streamMsg[chatID] = sent.ID
			}
		}

		delete(t.streamBuf, chatID)
		delete(t.streamMsg, chatID)
	}
}

func (t *Telegram) Allow(ctx context.Context, raw string) (bool, error) {
	src := cmd.SourceFrom(ctx)
	if !strings.HasPrefix(src, "telegram:") {
		return true, nil
	}

	var chatID int64
	fmt.Sscanf(strings.TrimPrefix(src, "telegram:"), "%d", &chatID)
	if chatID == 0 {
		return false, fmt.Errorf("invalid chat id")
	}

	ch := make(chan bool, 1)
	t.confirmMu.Lock()
	t.confirms[chatID] = ch
	t.confirmMu.Unlock()

	defer func() {
		t.confirmMu.Lock()
		delete(t.confirms, chatID)
		t.confirmMu.Unlock()
	}()

	chat := &tele.Chat{ID: chatID}
	t.bot.Send(chat, fmt.Sprintf("[Confirm] Execute: %s?\nReply Y/N", raw))

	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(60 * time.Second):
		t.bot.Send(chat, "[Timeout] Cancelled")
		return false, nil
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
