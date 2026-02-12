package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/cmd"
)

type Telegram struct {
	token   string
	bus     *bus.Bus
	client  *http.Client
	chatIDs map[int64]bool
	offset  int
	stop    chan struct{}

	confirmMu sync.Mutex
	confirms  map[int64]chan bool
}

type tgUpdate struct {
	ID      int        `json:"update_id"`
	Message *tgMessage `json:"message"`
}

type tgMessage struct {
	ID   int     `json:"message_id"`
	Chat *tgChat `json:"chat"`
	Text string  `json:"text"`
}

type tgChat struct {
	ID int64 `json:"id"`
}

func NewTelegram(token string, b *bus.Bus) *Telegram {
	return &Telegram{
		token:    token,
		bus:      b,
		client:   &http.Client{Timeout: 30 * time.Second},
		chatIDs:  make(map[int64]bool),
		stop:     make(chan struct{}),
		confirms: make(map[int64]chan bool),
	}
}

func (t *Telegram) ID() string { return "telegram" }

func (t *Telegram) Start(ctx context.Context) error {
	go t.forward(ctx)
	go t.poll(ctx)
	return nil
}

func (t *Telegram) Stop() error {
	close(t.stop)
	return nil
}

func (t *Telegram) poll(ctx context.Context) {
	for {
		select {
		case <-t.stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		updates, err := t.getUpdates()
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		for _, u := range updates {
			if u.ID >= t.offset {
				t.offset = u.ID + 1
			}
			if u.Message == nil || u.Message.Text == "" {
				continue
			}
			t.handleMessage(u.Message)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func (t *Telegram) handleMessage(m *tgMessage) {
	chatID := m.Chat.ID
	t.chatIDs[chatID] = true

	t.confirmMu.Lock()
	ch := t.confirms[chatID]
	t.confirmMu.Unlock()

	if ch != nil {
		ans := strings.ToLower(strings.TrimSpace(m.Text))
		ok := ans == "y" || ans == "yes"
		select {
		case ch <- ok:
		default:
		}
		return
	}

	_ = t.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    fmt.Sprintf("telegram:%d", chatID),
		To:      "*",
		Payload: m.Text,
	})
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

	unsub := func() {
		t.bus.Unsubscribe(bus.TypeAssistant, ch)
		t.bus.Unsubscribe(bus.TypeToolCall, ch)
		t.bus.Unsubscribe(bus.TypeToolResult, ch)
		t.bus.Unsubscribe(bus.TypeToolError, ch)
		t.bus.Unsubscribe(bus.TypeCommand, ch)
		t.bus.Unsubscribe(bus.TypeCommandOK, ch)
		t.bus.Unsubscribe(bus.TypeCommandError, ch)
		t.bus.Unsubscribe(bus.TypeAgentSpawn, ch)
		t.bus.Unsubscribe(bus.TypeAgentDone, ch)
		t.bus.Unsubscribe(bus.TypeTaskStart, ch)
		t.bus.Unsubscribe(bus.TypeTaskDone, ch)
		close(ch)
	}

	for {
		select {
		case m := <-ch:
			if !strings.HasPrefix(m.To, "telegram:") {
				continue
			}
			t.sendMsg(m)
		case <-t.stop:
			unsub()
			return
		case <-ctx.Done():
			unsub()
			return
		}
	}
}

func (t *Telegram) sendMsg(m bus.Msg) {
	var chatID int64
	fmt.Sscanf(strings.TrimPrefix(m.To, "telegram:"), "%d", &chatID)
	if chatID == 0 {
		// 广播消息发给所有已知的 chat
		if m.To == "*" {
			for id := range t.chatIDs {
				t.sendMsgToChat(id, m)
			}
		}
		return
	}
	t.sendMsgToChat(chatID, m)
}

func (t *Telegram) sendMsgToChat(chatID int64, m bus.Msg) {
	prefix := ""
	if m.From != "" && m.From != "main" {
		prefix = fmt.Sprintf("[%s] ", m.From)
	}

	var text string
	switch m.Type {
	case bus.TypeAssistant:
		text = fmt.Sprintf("[Assistant] %s%s", prefix, m.Payload)
	case bus.TypeToolCall:
		text = fmt.Sprintf("[Tool] %s%s", prefix, m.Payload)
	case bus.TypeToolResult:
		text = fmt.Sprintf("[OK] %s%s", prefix, truncate(fmt.Sprintf("%v", m.Payload), 200))
	case bus.TypeToolError:
		text = fmt.Sprintf("[ERR] %s%s", prefix, m.Payload)
	case bus.TypeCommand:
		text = fmt.Sprintf("$ %s%s", prefix, m.Payload)
	case bus.TypeCommandOK:
		text = fmt.Sprintf("[OK] %s%s", prefix, truncate(fmt.Sprintf("%v", m.Payload), 200))
	case bus.TypeCommandError:
		text = fmt.Sprintf("[ERR] %s%s", prefix, m.Payload)
	case bus.TypeAgentSpawn:
		if payload, ok := m.Payload.(map[string]string); ok {
			task := truncate(payload["task"], 100)
			text = fmt.Sprintf("[Spawn] %s: %s", payload["agent_id"], task)
		}
	case bus.TypeAgentDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			text = fmt.Sprintf("[Done] %s %s", payload["agent_id"], payload["result"])
		}
	case bus.TypeTaskStart:
		if payload, ok := m.Payload.(map[string]string); ok {
			cmd := truncate(payload["cmd"], 50)
			text = fmt.Sprintf("[Run] %s: %s", payload["task_id"], cmd)
		}
	case bus.TypeTaskDone:
		if payload, ok := m.Payload.(map[string]string); ok {
			if payload["status"] == "ok" {
				text = fmt.Sprintf("[OK] %s completed", payload["task_id"])
			} else {
				text = fmt.Sprintf("[ERR] %s: %s", payload["task_id"], truncate(payload["error"], 50))
			}
		}
	default:
		return
	}

	if text != "" {
		t.send(chatID, text)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (t *Telegram) getUpdates() ([]tgUpdate, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&limit=100", t.token, t.offset)

	resp, err := t.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r struct {
		OK     bool       `json:"ok"`
		Result []tgUpdate `json:"result"`
	}

	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}

	if !r.OK {
		return nil, fmt.Errorf("api error")
	}

	return r.Result, nil
}

func (t *Telegram) send(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.token)

	body, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})

	resp, err := t.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status=%d: %s", resp.StatusCode, body)
	}

	return nil
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

	t.send(chatID, fmt.Sprintf("[Confirm] Execute: %s?\nReply Y/N", raw))

	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(60 * time.Second):
		t.send(chatID, "[Timeout] Cancelled")
		return false, nil
	}
}
