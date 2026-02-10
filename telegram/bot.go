package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
)

type Bot struct {
	token   string
	bus     *bus.Bus
	client  *http.Client
	chatIDs map[int64]bool
	offset  int
	stop    chan bool
}

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

func New(token string, b *bus.Bus) *Bot {
	return &Bot{
		token:   token,
		bus:     b,
		client:  &http.Client{Timeout: 30 * time.Second},
		chatIDs: make(map[int64]bool),
		stop:    make(chan bool),
	}
}

func (b *Bot) Start() error {
	go b.forward()
	go b.poll()
	return nil
}

func (b *Bot) Stop() {
	close(b.stop)
}

func (b *Bot) poll() {
	for {
		select {
		case <-b.stop:
			return
		default:
		}

		updates, err := b.updates()
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

	b.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    fmt.Sprintf("telegram:%d", chatID),
		To:      "*",
		Payload: m.Text,
	})
}

func (b *Bot) forward() {
	ch := make(chan bus.Msg, 64)
	b.bus.Subscribe(bus.TypeAssistant, ch)

	for {
		select {
		case m := <-ch:
			if !strings.HasPrefix(m.To, "telegram:") {
				continue
			}
			b.sendTo(m)
		case <-b.stop:
			b.bus.Unsubscribe(bus.TypeAssistant, ch)
			return
		}
	}
}

func (b *Bot) sendTo(m bus.Msg) {
	text := fmt.Sprintf("🤖 %s", m.Payload)

	s := strings.TrimPrefix(m.To, "telegram:")
	var chatID int64
	fmt.Sscanf(s, "%d", &chatID)

	if chatID != 0 {
		b.send(chatID, text)
	}
}

func (b *Bot) updates() ([]Update, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&limit=100", b.token, b.offset)

	resp, err := b.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var r struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}

	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}

	if !r.OK {
		return nil, fmt.Errorf("api error")
	}

	return r.Result, nil
}

func (b *Bot) send(chatID int64, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)

	body, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})

	resp, err := b.client.Post(url, "application/json", bytes.NewBuffer(body))
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
