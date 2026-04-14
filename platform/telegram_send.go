package platform

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/internal/xstr"
	tele "gopkg.in/telebot.v4"
)

func (t *Telegram) sendTextWithOptions(chatID int64, text string, opts *tele.SendOptions) int {
	chat := &tele.Chat{ID: chatID}
	parts := splitText(text, telegramMsgLimit)
	msgID := 0
	for _, part := range parts {
		msg, err := t.sendWithOpts(chat, part, opts)
		if err != nil {
			t.errorf("send error: %v", err)
			return 0
		}
		if msg != nil {
			msgID = msg.ID
		}
		opts = nil
	}
	return msgID
}

func (t *Telegram) sendAssistantText(route string, chatID int64, text string, replyToID int) {
	t.debugf("send assistant chat=%d reply_to=%d text=%q", chatID, replyToID, truncateTG(text, 160))
	if t.isDuplicateAssistant(route, text, replyToID) {
		t.debugf("duplicate suppressed chat=%d reply_to=%d text=%q", chatID, replyToID, truncateTG(text, 160))
		return
	}
	t.notifyTyping(chatID)

	parts := splitText(text, telegramMsgLimit)
	if len(parts) == 0 {
		return
	}

	replyOpts := t.assistantSendOptions(route, chatID, replyToID, true)
	threadOpts := t.assistantSendOptions(route, chatID, 0, false)
	for i, part := range parts {
		opts := threadOpts
		if i == 0 {
			opts = replyOpts
		}
		t.sendTextWithOptions(chatID, part, opts)
	}
}

func (t *Telegram) sendText(chatID int64, text string) {
	t.sendTextWithOptions(chatID, text, nil)
}

func (t *Telegram) sendWithOpts(chat *tele.Chat, text string, opts *tele.SendOptions) (*tele.Message, error) {
	replyTo := 0
	threadID := 0
	if opts != nil {
		threadID = opts.ThreadID
		if opts.ReplyTo != nil {
			replyTo = opts.ReplyTo.ID
		}
	}

	text = tgRenderText(text)
	if opts == nil {
		opts = &tele.SendOptions{}
	} else {
		opts = cloneSendOptions(opts)
	}
	if opts.ParseMode == tele.ModeDefault {
		opts.ParseMode = tele.ModeHTML
	}
	msg, err := t.bot.Send(chat, text, opts)
	if err != nil {
		t.errorf("send error chat=%d thread=%d reply_to=%d err=%v text=%q", chat.ID, threadID, replyTo, err, truncateTG(text, 120))
		return nil, err
	}
	t.debugf("sent chat=%d msg=%d thread=%d reply_to=%d text=%q", chat.ID, msg.ID, threadID, replyTo, truncateTG(text, 120))
	return msg, nil
}

func splitText(s string, limit int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	if limit <= 0 || len(r) <= limit {
		return []string{s}
	}

	parts := make([]string, 0, (len(r)/limit)+1)
	for len(r) > 0 {
		n := min(limit, len(r))
		parts = append(parts, string(r[:n]))
		r = r[n:]
	}
	return parts
}

func cloneSendOptions(opts *tele.SendOptions) *tele.SendOptions {
	if opts == nil {
		return nil
	}
	cp := *opts
	return &cp
}

func (t *Telegram) touchActive(chatID int64) {
	t.activeMu.Lock()
	t.activeChats[chatID] = time.Now()
	t.activeMu.Unlock()
}

func (t *Telegram) notifyTyping(chatID int64) {
	if t.bot == nil {
		return
	}

	t.typingMu.Lock()
	last, ok := t.typingLast[chatID]
	if ok && time.Since(last) < telegramTypingCooldown {
		t.typingMu.Unlock()
		return
	}
	t.typingLast[chatID] = time.Now()
	t.typingMu.Unlock()

	chat := &tele.Chat{ID: chatID}
	st, ok := t.latestInboundForChat(chatID)
	if ok && st.threadID != 0 {
		if err := t.bot.Notify(chat, tele.Typing, st.threadID); err != nil {
			t.debugf("notify typing error chat=%d thread=%d err=%v", chatID, st.threadID, err)
		}
		return
	}
	if err := t.bot.Notify(chat, tele.Typing); err != nil {
		t.debugf("notify typing error chat=%d err=%v", chatID, err)
	}
}

func (t *Telegram) startTyping(chatID int64) {
	t.typingMu.Lock()
	t.typingN[chatID]++
	if _, ok := t.typing[chatID]; ok {
		t.typingMu.Unlock()
		t.notifyTyping(chatID)
		return
	}

	ch := make(chan struct{})
	t.typing[chatID] = ch
	t.typingMu.Unlock()
	t.notifyTyping(chatID)

	go func() {
		ticker := time.NewTicker(telegramTypingRefresh)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				t.notifyTyping(chatID)
			case <-ch:
				return
			case <-t.stop:
				return
			}
		}
	}()
}

func (t *Telegram) stopTyping(chatID int64) {
	t.typingMu.Lock()
	if n := t.typingN[chatID]; n > 1 {
		t.typingN[chatID] = n - 1
		t.typingMu.Unlock()
		return
	}
	delete(t.typingN, chatID)

	ch, ok := t.typing[chatID]
	if ok {
		delete(t.typing, chatID)
	}
	t.typingMu.Unlock()

	if ok {
		close(ch)
	}
}

func (t *Telegram) stopAllTyping() {
	t.typingMu.Lock()
	ids := make([]int64, 0, len(t.typing))
	for chatID := range t.typing {
		ids = append(ids, chatID)
	}
	t.typingMu.Unlock()

	for _, chatID := range ids {
		t.stopTyping(chatID)
	}
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
