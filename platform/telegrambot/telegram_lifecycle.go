package telegrambot

import (
	"context"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	tele "gopkg.in/telebot.v4"
)

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
