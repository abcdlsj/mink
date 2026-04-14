package platform

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/command"
	"github.com/abcdlsj/mink/internal/xstr"
	tele "gopkg.in/telebot.v4"
)

func (t *Telegram) routeTarget(text string) string {
	t.agentNamesMu.RLock()
	defer t.agentNamesMu.RUnlock()
	lower := strings.ToLower(text)
	for name, id := range t.agentNames {
		if strings.Contains(lower, "@"+strings.ToLower(name)) {
			return id
		}
	}
	return bus.AddrAgentMain
}

func (t *Telegram) handleMessage(c tele.Context) error {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return nil
	}

	chatID := msg.Chat.ID
	text := strings.TrimSpace(c.Text())
	src := t.source(chatID, msg.ThreadID)

	if text == "/new" {
		_ = t.bus.Pub(bus.Msg{
			Type:    bus.TypeSessionReset,
			From:    src,
			To:      bus.AddrAgentMain,
			Payload: src,
		})
		return c.Send("session reset. started a new session")
	}

	if text == "/cancel" {
		_ = t.bus.Pub(bus.Msg{
			Type:    bus.TypeInterrupt,
			From:    src,
			To:      bus.AddrAgentMain,
			Payload: "user cancelled",
		})
		t.stopTyping(chatID)
		return c.Send("cancelled current task")
	}

	t.touchActive(chatID)
	if command.IsCommand(text) && t.router != nil {
		ctx := bus.WithSource(context.Background(), src)
		out, ok, err := t.router.Route(ctx, text)
		if ok {
			if err != nil {
				out = "error: " + err.Error()
			}
			if strings.TrimSpace(out) == "" {
				out = "ok"
			}
			opts := &tele.SendOptions{}
			if msg.ThreadID != 0 {
				opts.ThreadID = msg.ThreadID
			}
			if msg.ID != 0 {
				opts.ReplyTo = &tele.Message{ID: msg.ID, Chat: msg.Chat}
			}
			t.sendTextWithOptions(chatID, out, opts)
			return nil
		}
	}
	mentioned := t.isMentioned(msg)
	if !t.shouldHandleMessage(msg, text, mentioned) {
		return nil
	}
	t.pushInboundState(src, inboundState{
		msgID:    msg.ID,
		threadID: msg.ThreadID,
	})
	t.applyReaction(src, chatID, "👀", msg.ID)

	payload := t.formatInboundPayload(msg, text, mentioned)
	t.debugf("inbound chat=%d msg=%d thread=%d mentioned=%v text=%q", chatID, msg.ID, msg.ThreadID, mentioned, truncateTG(text, 160))

	_ = t.bus.Pub(bus.Msg{
		Type:    bus.TypeUserInput,
		From:    src,
		To:      t.routeTarget(text),
		Payload: payload,
	})
	t.startTyping(chatID)

	return nil
}

func (t *Telegram) pushInboundState(route string, s inboundState) {
	t.inboundMu.Lock()
	t.inbound[route] = append(t.inbound[route], s)
	t.lastIn[route] = s
	t.inboundMu.Unlock()
}

func (t *Telegram) peekInboundState(route string) (inboundState, bool) {
	t.inboundMu.RLock()
	defer t.inboundMu.RUnlock()
	q := t.inbound[route]
	if len(q) == 0 {
		s, ok := t.lastIn[route]
		return s, ok
	}
	return q[0], true
}

func (t *Telegram) latestInboundForChat(chatID int64) (inboundState, bool) {
	t.inboundMu.RLock()
	defer t.inboundMu.RUnlock()
	prefix := bus.Telegram(chatID)
	for route, q := range t.inbound {
		if !strings.HasPrefix(route, prefix) || len(q) == 0 {
			continue
		}
		return q[len(q)-1], true
	}
	for route, s := range t.lastIn {
		if strings.HasPrefix(route, prefix) {
			return s, true
		}
	}
	return inboundState{}, false
}

func (t *Telegram) popInboundState(route string) {
	t.inboundMu.Lock()
	defer t.inboundMu.Unlock()
	q := t.inbound[route]
	if len(q) == 0 {
		return
	}
	q = q[1:]
	if len(q) == 0 {
		delete(t.inbound, route)
		return
	}
	t.inbound[route] = q
}

func (t *Telegram) applyReaction(route string, chatID int64, emoji string, replyToID int) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || t.bot == nil {
		return
	}
	msgID := replyToID
	st, ok := t.peekInboundState(route)
	if !ok {
		return
	}
	if msgID == 0 {
		msgID = st.msgID
	}
	if msgID == 0 {
		return
	}

	chat := &tele.Chat{ID: chatID}
	err := t.bot.React(chat, &tele.Message{ID: msgID, Chat: chat}, tele.Reactions{
		Reactions: []tele.Reaction{{Type: tele.ReactionTypeEmoji, Emoji: emoji}},
	})
	if err != nil {
		t.errorf("react error: %v", err)
		return
	}
	t.debugf("reacted chat=%d msg=%d emoji=%s", chatID, msgID, emoji)
}

func (t *Telegram) assistantSendOptions(route string, chatID int64, replyToID int, withReply bool) *tele.SendOptions {
	st, ok := t.peekInboundState(route)
	if !ok {
		return nil
	}

	opts := &tele.SendOptions{}
	has := false
	if st.threadID != 0 {
		opts.ThreadID = st.threadID
		has = true
	}
	if withReply && (replyToID != 0 || st.msgID != 0) {
		target := replyToID
		if target == 0 {
			target = st.msgID
		}
		opts.ReplyTo = &tele.Message{ID: target, Chat: &tele.Chat{ID: chatID}}
		has = true
	}
	if !has {
		return nil
	}
	return opts
}

func (t *Telegram) resolveReplyToID(route string, out tgAssistantOut) int {
	if out.ReplyToID > 0 {
		return out.ReplyToID
	}
	st, ok := t.peekInboundState(route)
	if !ok {
		return 0
	}
	if out.ReplyNow {
		return st.msgID
	}
	return st.msgID
}

func (t *Telegram) isDuplicateAssistant(route string, text string, replyToID int) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	now := time.Now()
	t.assistMu.Lock()
	defer t.assistMu.Unlock()

	st, ok := t.assist[route]
	if ok && st.text == text && st.replyToID == replyToID && now.Sub(st.at) < 3*time.Second {
		t.debugf("dedupe hit route=%s reply_to=%d age_ms=%d", route, replyToID, now.Sub(st.at).Milliseconds())
		return true
	}

	t.assist[route] = assistantOutState{text: text, replyToID: replyToID, at: now}
	return false
}

func (t *Telegram) source(chatID int64, threadID int) string {
	if t.sessionScope == "thread" && threadID != 0 {
		return fmt.Sprintf("telegram:%d:%d", chatID, threadID)
	}
	return bus.Telegram(chatID)
}

func (t *Telegram) shouldHandleMessage(msg *tele.Message, text string, mentioned bool) bool {
	if msg == nil || msg.Chat == nil {
		return false
	}
	if msg.Chat.Type == tele.ChatPrivate {
		return true
	}
	switch t.mentionMode {
	case "mention_only":
		return mentioned
	case "smart":
		if mentioned {
			return true
		}
		v := strings.ToLower(strings.TrimSpace(text))
		return strings.HasPrefix(v, "/new") || strings.HasPrefix(v, "/cancel")
	default:
		return true
	}
}

func (t *Telegram) isMentioned(msg *tele.Message) bool {
	if msg == nil {
		return false
	}
	if t.bot != nil && t.bot.Me != nil {
		if msg.ReplyTo != nil && msg.ReplyTo.Sender != nil && msg.ReplyTo.Sender.ID == t.bot.Me.ID {
			return true
		}
		for _, e := range msg.Entities {
			if e.Type == tele.EntityTMention && e.User != nil && e.User.ID == t.bot.Me.ID {
				return true
			}
		}
		name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t.bot.Me.Username), "@"))
		if name != "" {
			needle := "@" + name
			if strings.Contains(strings.ToLower(msg.Text), needle) || strings.Contains(strings.ToLower(msg.Caption), needle) {
				return true
			}
		}
	}
	return false
}

func (t *Telegram) formatInboundPayload(msg *tele.Message, text string, mentioned bool) string {
	if msg == nil || msg.Chat == nil {
		return text
	}

	var b strings.Builder
	b.WriteString("[telegram_context]\n")
	b.WriteString("chat_id: ")
	b.WriteString(strconv.FormatInt(msg.Chat.ID, 10))
	b.WriteByte('\n')
	b.WriteString("chat_type: ")
	b.WriteString(string(msg.Chat.Type))
	b.WriteByte('\n')
	if title := oneLine(msg.Chat.Title); title != "" {
		b.WriteString("chat_title: ")
		b.WriteString(title)
		b.WriteByte('\n')
	}
	b.WriteString("message_id: ")
	b.WriteString(strconv.Itoa(msg.ID))
	b.WriteByte('\n')
	if msg.ThreadID != 0 {
		b.WriteString("thread_id: ")
		b.WriteString(strconv.Itoa(msg.ThreadID))
		b.WriteByte('\n')
	}
	if sender := oneLine(senderDisplay(msg.Sender)); sender != "" {
		b.WriteString("sender: ")
		b.WriteString(sender)
		b.WriteByte('\n')
	}
	if msg.Sender != nil {
		if u := oneLine(msg.Sender.Username); u != "" {
			b.WriteString("sender_username: @")
			b.WriteString(u)
			b.WriteByte('\n')
		}
	}
	b.WriteString("was_mentioned: ")
	if mentioned {
		b.WriteString("true\n")
	} else {
		b.WriteString("false\n")
	}
	if msg.ReplyTo != nil {
		b.WriteString("reply_to_message_id: ")
		b.WriteString(strconv.Itoa(msg.ReplyTo.ID))
		b.WriteByte('\n')
		if rs := oneLine(senderDisplay(msg.ReplyTo.Sender)); rs != "" {
			b.WriteString("reply_to_sender: ")
			b.WriteString(rs)
			b.WriteByte('\n')
		}
	}
	b.WriteString("[/telegram_context]\n\n")
	b.WriteString(text)
	return b.String()
}

func senderDisplay(u *tele.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(strings.TrimSpace(u.FirstName + " " + u.LastName))
	if name != "" {
		if un := strings.TrimSpace(u.Username); un != "" {
			return name + " (@" + un + ")"
		}
		return name
	}
	if un := strings.TrimSpace(u.Username); un != "" {
		return "@" + un
	}
	return strconv.FormatInt(u.ID, 10)
}

func oneLine(v string) string {
	v = strings.TrimSpace(v)
	v = strings.ReplaceAll(v, "\n", " ")
	v = strings.ReplaceAll(v, "\r", " ")
	return strings.TrimSpace(v)
}

func parseTelegramChatID(src string) int64 {
	var chatID int64
	_, _ = fmt.Sscanf(strings.TrimPrefix(src, "telegram:"), "%d", &chatID)
	return chatID
}

func shortReqID() string {
	val := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(val) > 6 {
		return val[len(val)-6:]
	}
	return val
}

var truncateTG = xstr.TruncateAppend
