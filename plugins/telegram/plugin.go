package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/config"
	tele "gopkg.in/telebot.v4"
)

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterEntrypoint("tg", run)
		return nil
	}
}

func run(ctx context.Context, a *app.App, args []string) error {
	cfg := a.Config().Telegram
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return fmt.Errorf("telegram token is not configured")
	}
	bot, err := tele.NewBot(tele.Settings{
		Token:  token,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	})
	if err != nil {
		return err
	}
	ap := newApprover(bot)
	a.SetToolApprover(ap)
	wireHandlers(ctx, a, bot, ap, cfg)

	go func() {
		<-ctx.Done()
		bot.Stop()
	}()

	fmt.Println("telegram bot started")
	bot.Start()
	return nil
}

func wireHandlers(ctx context.Context, a *app.App, bot *tele.Bot, ap *approver, cfg config.TelegramConfig) {
	bot.Handle(tele.OnCallback, func(c tele.Context) error {
		ap.handleCallback(c)
		return nil
	})
	bot.Handle(tele.OnText, func(c tele.Context) error {
		return handleText(ctx, a, bot, ap, cfg, c)
	})
}

func handleText(ctx context.Context, a *app.App, bot *tele.Bot, ap *approver, cfg config.TelegramConfig, c tele.Context) error {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil
	}
	text := strings.TrimSpace(c.Text())
	if text == "" || ap.handleText(c) {
		return nil
	}
	if !shouldHandle(cfg.MentionMode, bot.Me.Username, msg, text) {
		return nil
	}
	src := source(cfg.SessionScope, msg.Chat.ID, msg.ThreadID)
	if out, ok, err := handleTelegramCommand(a, src, text); ok {
		if err != nil {
			return c.Send("error: " + err.Error())
		}
		return c.Send(out)
	}

	done := make(chan struct{})
	go typingLoop(c, done)
	out, err := a.HandleInput(ctx, src, stripMention(bot.Me.Username, text))
	close(done)
	if err != nil {
		return c.Send("error: " + err.Error())
	}
	if strings.TrimSpace(out) == "" {
		out = "ok"
	}
	return sendOutput(bot, c, out)
}

func handleTelegramCommand(a *app.App, src, text string) (string, bool, error) {
	switch text {
	case "/new":
		_, err := a.NewSession(src)
		return "started a new session", true, err
	case "/model":
		return a.CurrentModel(), true, nil
	default:
		return "", false, nil
	}
}

func typingLoop(c tele.Context, done <-chan struct{}) {
	tick := time.NewTicker(4 * time.Second)
	defer tick.Stop()
	for {
		_ = c.Notify(tele.Typing)
		select {
		case <-done:
			return
		case <-tick.C:
		}
	}
}

func sendLong(c tele.Context, text string, opts ...interface{}) error {
	parts := split(text, 3500)
	for _, part := range parts {
		if err := c.Send(part, opts...); err != nil {
			return err
		}
	}
	return nil
}

func split(text string, n int) []string {
	if len([]rune(text)) <= n {
		return []string{text}
	}
	var out []string
	rs := []rune(text)
	for len(rs) > 0 {
		if len(rs) <= n {
			out = append(out, string(rs))
			break
		}
		out = append(out, string(rs[:n]))
		rs = rs[n:]
	}
	return out
}

func source(scope string, chatID int64, threadID int) string {
	if strings.TrimSpace(scope) == "thread" && threadID != 0 {
		return fmt.Sprintf("telegram:%d:%d", chatID, threadID)
	}
	return fmt.Sprintf("telegram:%d", chatID)
}

func shouldHandle(mode, username string, msg *tele.Message, text string) bool {
	if msg == nil || msg.Chat == nil {
		return false
	}
	if msg.Chat.Type == tele.ChatPrivate {
		return true
	}
	mode = strings.TrimSpace(mode)
	if mode == "always" {
		return true
	}
	tag := "@" + strings.ToLower(strings.TrimSpace(username))
	has := tag != "@" && strings.Contains(strings.ToLower(text), tag)
	switch mode {
	case "mention_only":
		return has
	case "smart":
		return has || msg.ReplyTo != nil
	default:
		return true
	}
}

func stripMention(username, text string) string {
	tag := strings.TrimSpace(username)
	if tag == "" {
		return text
	}
	return strings.TrimSpace(strings.ReplaceAll(text, "@"+tag, ""))
}
