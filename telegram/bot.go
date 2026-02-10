package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/abcdlsj/mink/event"
)

type Bot struct {
	agent   Agent
	bus     *event.Bus
	token   string
	chats   map[int64]bool
	client  *http.Client
	offset  int
}

type Agent interface {
	Run(ctx context.Context, input string) error
	Cmd(name string, args []string) (string, error)
}

type Update struct {
	ID      int      `json:"update_id"`
	Message *Message `json:"message"`
}

type Message struct {
	Chat *Chat  `json:"chat"`
	Text string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

func New(a Agent, b *event.Bus, token string) *Bot {
	return &Bot{
		agent:  a,
		bus:    b,
		token:  token,
		chats:  make(map[int64]bool),
		client: &http.Client{},
	}
}

func (b *Bot) Start() error {
	b.bus.Subscribe(event.AssistantMsg, func(e event.Event) {
		for id := range b.chats {
			b.send(id, e.Data.(string))
		}
	})
	b.bus.Subscribe(event.ToolStart, func(e event.Event) {
		d := e.Data.(map[string]string)
		for id := range b.chats {
			b.send(id, "🔧 "+d["name"])
		}
	})
	go b.poll()
	return nil
}

func (b *Bot) poll() {
	for {
		updates, err := b.getUpdates()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tg poll: %v\n", err)
			continue
		}
		for _, u := range updates {
			if u.ID >= b.offset {
				b.offset = u.ID + 1
			}
			if u.Message == nil {
				continue
			}
			b.handle(u.Message)
		}
	}
}

func (b *Bot) handle(m *Message) {
	b.chats[m.Chat.ID] = true

	if strings.HasPrefix(m.Text, "/") {
		parts := strings.Fields(m.Text[1:])
		if len(parts) == 0 {
			return
		}
		out, err := b.agent.Cmd(parts[0], parts[1:])
		if err != nil {
			b.send(m.Chat.ID, "error: "+err.Error())
		} else {
			b.send(m.Chat.ID, out)
		}
		return
	}

	go func() {
		ctx := context.Background()
		if err := b.agent.Run(ctx, m.Text); err != nil {
			b.send(m.Chat.ID, "error: "+err.Error())
		}
	}()
}

func (b *Bot) send(chatID int64, text string) {
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	body, _ := json.Marshal(map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	b.client.Post(u, "application/json", bytes.NewBuffer(body))
}

func (b *Bot) getUpdates() ([]Update, error) {
	u := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&limit=100", b.token, b.offset)
	resp, err := b.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var r struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return r.Result, nil
}
