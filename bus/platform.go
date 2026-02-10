package bus

import (
	"context"
	"fmt"
)

// Platform 平台适配接口
type Platform interface {
	Start() error
	Stop() error
	Send(m Msg) error
	Recv() <-chan Msg
}

// PlatformAdapter 平台适配器
type PlatformAdapter struct {
	bus      *Bus
	platform string
	in       chan Msg
	out      chan Msg
}

func NewAdapter(b *Bus, name string) *PlatformAdapter {
	return &PlatformAdapter{
		bus:      b,
		platform: name,
		in:       make(chan Msg, 64),
		out:      make(chan Msg, 64),
	}
}

// Adapt 适配消息格式
func (a *PlatformAdapter) Adapt(from string, payload any) Msg {
	return Msg{
		From:    fmt.Sprintf("%s:%s", a.platform, from),
		Type:    TypeUserInput,
		Payload: payload,
		Context: MsgContext{
			Data: map[string]any{
				"platform": a.platform,
			},
		},
	}
}

// Bridge 桥接平台到总线
func (a *PlatformAdapter) Bridge(ctx context.Context) {
	// 平台消息 → 总线
	go func() {
		for {
			select {
			case m := <-a.in:
				a.bus.Pub(m)
			case <-ctx.Done():
				return
			}
		}
	}()
	
	// 总线消息 → 平台
	go func() {
		ch := make(chan Msg, 64)
		a.bus.Subscribe(TypeAssistant, ch)
		defer a.bus.Unsubscribe(TypeAssistant, ch)
		
		for {
			select {
			case m := <-ch:
				// 过滤只给本平台的消息
				if a.match(m) {
					a.out <- m
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (a *PlatformAdapter) match(m Msg) bool {
	// 检查是否是发给本平台的消息
	if m.To == "" || m.To == "*" {
		return true
	}
	return m.To == a.platform
}

// Send 发送给平台
func (a *PlatformAdapter) Send(m Msg) {
	a.out <- m
}

// Recv 从平台接收
func (a *PlatformAdapter) Recv() <-chan Msg {
	return a.in
}

// CLIAdapter CLI适配器
type CLIAdapter struct {
	*PlatformAdapter
}

func NewCLI(b *Bus) *CLIAdapter {
	return &CLIAdapter{
		PlatformAdapter: NewAdapter(b, "cli"),
	}
}

// TelegramAdapter Telegram适配器
type TelegramAdapter struct {
	*PlatformAdapter
	token   string
	chatIDs map[int64]bool
}

func NewTelegram(b *Bus, token string) *TelegramAdapter {
	return &TelegramAdapter{
		PlatformAdapter: NewAdapter(b, "telegram"),
		token:           token,
		chatIDs:         make(map[int64]bool),
	}
}

func (t *TelegramAdapter) Start() error {
	// 启动长轮询
	go t.poll()
	return nil
}

func (t *TelegramAdapter) poll() {
	// 简化实现，实际需要调用 Telegram API
	for update := range t.fetchUpdates() {
		if update.Message != nil {
			t.chatIDs[update.Message.Chat.ID] = true
			
			m := t.Adapt(
				fmt.Sprintf("%d", update.Message.Chat.ID),
				update.Message.Text,
			)
			t.in <- m
		}
	}
}

func (t *TelegramAdapter) fetchUpdates() <-chan *Update {
	ch := make(chan *Update)
	// 实际实现需要调用 Telegram API
	return ch
}

func (t *TelegramAdapter) sendToTelegram(chatID int64, text string) {
	// 调用 Telegram API 发送消息
}

// Update Telegram更新
type Update struct {
	Message *struct {
		Chat *struct {
			ID int64
		}
		Text string
	}
}

// WebAdapter WebUI适配器
type WebAdapter struct {
	*PlatformAdapter
	clients map[string]chan Msg
}

func NewWeb(b *Bus) *WebAdapter {
	return &WebAdapter{
		PlatformAdapter: NewAdapter(b, "web"),
		clients:         make(map[string]chan Msg),
	}
}

func (w *WebAdapter) RegisterClient(id string) chan Msg {
	ch := make(chan Msg, 64)
	w.clients[id] = ch
	return ch
}

func (w *WebAdapter) UnregisterClient(id string) {
	if ch, ok := w.clients[id]; ok {
		close(ch)
		delete(w.clients, id)
	}
}

func (w *WebAdapter) Broadcast(m Msg) {
	for _, ch := range w.clients {
		select {
		case ch <- m:
		default:
		}
	}
}
