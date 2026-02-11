package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
)

type Telegram struct {
	token   string
	bus     *bus.Bus
	client  *http.Client
	chatIDs map[int64]bool
	offset  int
	stop    chan struct{}
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
		token:   token,
		bus:     b,
		client:  &http.Client{Timeout: 30 * time.Second},
		chatIDs: make(map[int64]bool),
		stop:    make(chan struct{}),
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

	t.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    fmt.Sprintf("telegram:%d", chatID),
		To:      "*",
		Payload: m.Text,
	})
}

func (t *Telegram) forward(ctx context.Context) {
	ch := make(chan bus.Msg, 64)
	t.bus.Subscribe(bus.TypeAssistant, ch)

	for {
		select {
		case m := <-ch:
			if !strings.HasPrefix(m.To, "telegram:") {
				continue
			}
			t.sendResponse(m)
		case <-t.stop:
			t.bus.Unsubscribe(bus.TypeAssistant, ch)
			return
		case <-ctx.Done():
			t.bus.Unsubscribe(bus.TypeAssistant, ch)
			return
		}
	}
}

func (t *Telegram) sendResponse(m bus.Msg) {
	text := fmt.Sprintf("🤖 %s", m.Payload)

	s := strings.TrimPrefix(m.To, "telegram:")
	var chatID int64
	fmt.Sscanf(s, "%d", &chatID)

	if chatID != 0 {
		t.send(chatID, text)
	}
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
