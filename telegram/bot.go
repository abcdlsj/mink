package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/abcdlsj/mink/bus"
)

// Bot Telegram Bot
type Bot struct {
	token   string
	bus     *bus.Bus
	client  *http.Client
	chatIDs map[int64]bool
	offset  int
	stop    chan bool
}

// Update Telegram update
type Update struct {
	ID      int      `json:"update_id"`
	Message *Message `json:"message"`
}

type Message struct {
	ID   int    `json:"message_id"`
	Chat *Chat  `json:"chat"`
	Text string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

// NewBot 创建 Bot
func New(token string, b *bus.Bus) *Bot {
	return &Bot{
		token:   token,
		bus:     b,
		client:  &http.Client{Timeout: 30 * time.Second},
		chatIDs: make(map[int64]bool),
		stop:    make(chan bool),
	}
}

// Start 启动 bot
func (b *Bot) Start() error {
	// 订阅 AI 回复
	go b.forwardReplies()

	// 启动长轮询
	go b.poll()

	return nil
}

// Stop 停止 bot
func (b *Bot) Stop() {
	close(b.stop)
}

// poll 长轮询获取消息
func (b *Bot) poll() {
	for {
		select {
		case <-b.stop:
			return
		default:
		}

		updates, err := b.getUpdates()
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		for _, u := range updates {
			if u.ID >= b.offset {
				b.offset = u.ID + 1
			}

			if u.Message == nil || u.Message.Text == "" {
				continue
			}

			b.handle(u.Message)
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func (b *Bot) handle(m *Message) {
	chatID := m.Chat.ID
	b.chatIDs[chatID] = true

	// 转换为 bus 消息
	msg := bus.Msg{
		Type:    bus.TypeUserInput,
		From:    fmt.Sprintf("telegram:%d", chatID),
		To:      "*", // 广播给所有 agent
		Payload: m.Text,
		Context: bus.MsgContext{
			Data: map[string]any{
				"platform":   "telegram",
				"chat_id":    chatID,
				"message_id": m.ID,
			},
		},
	}

	b.bus.Pub(msg)
}

// forwardReplies 转发 AI 回复到 Telegram
func (b *Bot) forwardReplies() {
	ch := make(chan bus.Msg, 64)
	b.bus.Subscribe(bus.TypeAssistant, ch)

	for {
		select {
		case m := <-ch:
			b.sendToChats(m)
		case <-b.stop:
			b.bus.Unsubscribe(bus.TypeAssistant, ch)
			return
		}
	}
}

func (b *Bot) sendToChats(m bus.Msg) {
	text := fmt.Sprintf("🤖 %s", m.Payload)

	// 如果有特定目标，只发给目标
	if to, ok := m.Context.Data["chat_id"].(int64); ok {
		b.send(to, text)
		return
	}

	// 否则广播给所有 chats
	for chatID := range b.chatIDs {
		b.send(chatID, text)
	}
}

func (b *Bot) getUpdates() ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&limit=100",
		b.token, b.offset)

	resp, err := b.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if !result.OK {
		return nil, fmt.Errorf("api error")
	}

	return result.Result, nil
}

func (b *Bot) send(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)

	body, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	})

	resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// SetWebhook 设置 webhook（可选）
func (b *Bot) SetWebhook(webhookURL string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", b.token)

	body, _ := json.Marshal(map[string]string{
		"url": webhookURL,
	})

	resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
