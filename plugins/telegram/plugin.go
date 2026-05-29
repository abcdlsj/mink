package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
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
	go relayNotices(ctx, a, bot)

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
	bot.Handle(tele.OnPhoto, func(c tele.Context) error {
		return handleImage(ctx, a, bot, ap, cfg, c)
	})
	bot.Handle(tele.OnDocument, func(c tele.Context) error {
		return handleImage(ctx, a, bot, ap, cfg, c)
	})
}

func relayNotices(ctx context.Context, a *app.App, bot *tele.Bot) {
	events, cancel := a.Bus().Subscribe(256)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == bus.ServiceNotice {
				_ = sendNotice(bot, ev.Source, ev.Text)
			}
		}
	}
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
	if !shouldHandle(cfg.MentionMode, bot.Me.Username, msg, text) && !mentionsPersona(a, text) {
		return nil
	}
	src := source(cfg.SessionScope, msg.Chat, msg.ThreadID)
	if src == "" {
		return nil
	}
	if out, ok, err := handleTelegramCommand(a, src, text); ok {
		if err != nil {
			return c.Send(userError(err))
		}
		return c.Send(out)
	}

	stripped := stripMention(bot.Me.Username, text)
	before := spaceSnapshotIDs(a, src)
	done := make(chan struct{})
	go typingLoop(c, done)
	out, err := a.HandleInput(ctx, src, stripped)
	close(done)
	if err != nil {
		return c.Send(userError(err))
	}
	if sourceTracksSpace(src) {
		return sendSpaceReplies(bot, c, a, src, stripped, before)
	}
	if strings.TrimSpace(out) == "" {
		out = "ok"
	}
	return sendOutput(bot, c, out)
}

func handleImage(ctx context.Context, a *app.App, bot *tele.Bot, ap *approver, cfg config.TelegramConfig, c tele.Context) error {
	tgmsg := c.Message()
	if tgmsg == nil || tgmsg.Chat == nil {
		return nil
	}
	text := strings.TrimSpace(tgmsg.Caption)
	if ap.handleText(c) {
		return nil
	}
	if !shouldHandle(cfg.MentionMode, bot.Me.Username, tgmsg, text) && !mentionsPersona(a, text) {
		return nil
	}
	att, err := telegramImageAttachment(bot, tgmsg)
	if err != nil {
		return c.Send(userError(err))
	}
	src := source(cfg.SessionScope, tgmsg.Chat, tgmsg.ThreadID)
	if src == "" {
		return nil
	}

	stripped := stripMention(bot.Me.Username, text)
	before := spaceSnapshotIDs(a, src)
	done := make(chan struct{})
	go typingLoop(c, done)
	out, err := a.HandleInputWithAttachments(ctx, src, stripped, []msg.Attachment{att})
	close(done)
	if err != nil {
		return c.Send(userError(err))
	}
	if sourceTracksSpace(src) {
		return sendSpaceReplies(bot, c, a, src, stripped, before)
	}
	if strings.TrimSpace(out) == "" {
		out = "ok"
	}
	return sendOutput(bot, c, out)
}

func telegramImageAttachment(bot *tele.Bot, m *tele.Message) (msg.Attachment, error) {
	if bot == nil || m == nil {
		return msg.Attachment{}, fmt.Errorf("missing telegram image")
	}
	file, name, mt, err := telegramImageFile(m)
	if err != nil {
		return msg.Attachment{}, err
	}
	r, err := bot.File(&file)
	if err != nil {
		return msg.Attachment{}, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, imageDownloadLimit+1))
	if err != nil {
		return msg.Attachment{}, err
	}
	if len(data) > imageDownloadLimit {
		return msg.Attachment{}, fmt.Errorf("telegram image is larger than %d bytes", imageDownloadLimit)
	}
	if mt == "" {
		mt = detectTelegramImageMIME(data, name)
	}
	if mt == "" {
		return msg.Attachment{}, fmt.Errorf("unsupported telegram image type")
	}
	return msg.Attachment{
		Kind:     "image",
		Name:     name,
		MIME:     mt,
		Data:     base64.StdEncoding.EncodeToString(data),
		Telegram: file.FileID,
	}, nil
}

func telegramImageFile(m *tele.Message) (tele.File, string, string, error) {
	if m.Photo != nil && m.Photo.FileID != "" {
		return m.Photo.File, "telegram-photo.jpg", "", nil
	}
	if m.Document != nil && m.Document.FileID != "" {
		mt := strings.TrimSpace(m.Document.MIME)
		if !strings.HasPrefix(mt, "image/") {
			return tele.File{}, "", "", fmt.Errorf("telegram document is not an image")
		}
		return m.Document.File, m.Document.FileName, mt, nil
	}
	return tele.File{}, "", "", fmt.Errorf("telegram message has no image")
}

func detectTelegramImageMIME(data []byte, name string) string {
	switch strings.ToLower(mime.TypeByExtension(imageExt(name))) {
	case "image/jpeg":
		return "image/jpeg"
	case "image/png":
		return "image/png"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	}
	switch {
	case len(data) >= 3 && string(data[:3]) == "\xff\xd8\xff":
		return "image/jpeg"
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a"):
		return "image/gif"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	default:
		return ""
	}
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
	return sendText(c.Send, text, opts...)
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

// source builds the routed source string for a Telegram message
// per P4 v3:
//
//   private chat                 -> "tg:dm:<chat>"
//   group / supergroup / channel -> "tg:channel:<chat>"
//
// scope == "thread" appends ":<threadID>" for forum-style topics.
//
// Per Iris: unknown chat types are NOT silently treated as DM. We
// emit a tg:dm:<chat> with a leading underscore namespace so the
// Space mapping rejects it (MapSource keeps the strict prefixes).
// Callers should treat an empty return as "do not route".
func source(scope string, chat *tele.Chat, threadID int) string {
	if chat == nil {
		return ""
	}
	id := chat.ID
	suffix := ""
	if strings.TrimSpace(scope) == "thread" && threadID != 0 {
		suffix = fmt.Sprintf(":%d", threadID)
	}
	switch chat.Type {
	case tele.ChatPrivate:
		return fmt.Sprintf("tg:dm:%d%s", id, suffix)
	case tele.ChatGroup, tele.ChatSuperGroup, tele.ChatChannel:
		return fmt.Sprintf("tg:channel:%d%s", id, suffix)
	}
	// Unknown chat type — refuse to map. The caller will skip this
	// message rather than fall into a default DM bucket.
	return ""
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

func mentionsPersona(a *app.App, text string) bool {
	if a == nil || a.Personas() == nil {
		return false
	}
	id := leadingMentionID(text)
	return id != "" && a.Personas().Get(id) != nil
}

func leadingMentionID(text string) string {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "@") {
		return ""
	}
	s = s[1:]
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}
