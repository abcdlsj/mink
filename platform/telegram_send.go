package platform

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/internal/xstr"
	"github.com/abcdlsj/mink/tool"
	tele "gopkg.in/telebot.v4"
)

func (t *Telegram) handleStreamChunk(route string, chatID int64, delta string) {
	if delta == "" {
		return
	}
	t.clearProgress(route)
	t.debugf("stream chunk chat=%d size=%d", chatID, len([]rune(delta)))
	t.notifyTyping(chatID)

	t.streamMu.Lock()
	s, ok := t.streams[route]
	if !ok {
		s = &streamState{chatID: chatID}
		t.streams[route] = s
	}
	s.buf.WriteString(delta)
	s.dirty = true
	should := t.shouldFlush(s, false)
	t.streamMu.Unlock()

	if should {
		t.flushStream(route, false)
	}
}

func (t *Telegram) shouldFlush(s *streamState, ended bool) bool {
	if ended {
		return true
	}
	if !s.dirty {
		return false
	}
	if s.at.IsZero() {
		return true
	}
	elapsed := time.Since(s.at)
	bufLen := s.buf.Len()

	if elapsed >= telegramStreamMinInt {
		return true
	}
	if bufLen >= telegramStreamMinLen {
		return true
	}
	return false
}

func endsWithBreak(s string) bool {
	if len(s) == 0 {
		return false
	}
	r := []rune(s)
	c := r[len(r)-1]
	return c == '\n' || c == '.' || c == '!' || c == '?' || c == '。' || c == '！' || c == '？'
}

func (t *Telegram) handleStreamEnd(route string) {
	chatID := parseTelegramChatID(route)
	t.debugf("stream end chat=%d", chatID)
	t.streamMu.Lock()
	if s, ok := t.streams[route]; ok {
		s.ended = true
		s.dirty = true
	}
	t.streamMu.Unlock()

	t.flushStream(route, true)
}

func (t *Telegram) Approve(ctx context.Context, raw string) (tool.Approval, error) {
	src := bus.SourceFrom(ctx)
	if !strings.HasPrefix(src, "telegram:") {
		return tool.AllowOnce, nil
	}

	chatID := parseTelegramChatID(src)
	if chatID == 0 {
		return tool.Denied, fmt.Errorf("invalid chat id")
	}

	reqID := shortReqID()
	ch := make(chan tool.Approval, 1)
	t.confirmMu.Lock()
	if _, ok := t.confirms[chatID]; !ok {
		t.confirms[chatID] = make(map[string]confirmState)
	}
	t.confirms[chatID][reqID] = confirmState{ch: ch, created: time.Now()}
	t.confirmMu.Unlock()

	defer func() {
		t.confirmMu.Lock()
		if chatReqs, ok := t.confirms[chatID]; ok {
			delete(chatReqs, reqID)
			if len(chatReqs) == 0 {
				delete(t.confirms, chatID)
			}
		}
		t.confirmMu.Unlock()
	}()

	msgID := t.sendConfirmRequest(src, chatID, reqID, raw)
	if msgID > 0 {
		t.confirmMu.Lock()
		if state, ok := t.confirms[chatID][reqID]; ok {
			state.msgID = msgID
			t.confirms[chatID][reqID] = state
		}
		t.confirmMu.Unlock()
	}

	select {
	case approval := <-ch:
		return approval, nil
	case <-ctx.Done():
		return tool.Denied, ctx.Err()
	case <-time.After(telegramConfirmTimeout):
		t.sendText(chatID, fmt.Sprintf("confirm %s timed out, cancelled", reqID))
		return tool.Denied, nil
	}
}

func (t *Telegram) respondConfirm(chatID int64, msgID int, approval tool.Approval, reqID string) {
	if msgID <= 0 {
		return
	}

	chat := &tele.Chat{ID: chatID}
	emoji := "👎"
	if approval == tool.AllowAlways || approval == tool.AllowOnce {
		emoji = "👍"
	}

	_, _ = t.bot.Edit(&tele.Message{ID: msgID, Chat: chat}, fmt.Sprintf("confirm %s\n", reqID), &tele.SendOptions{
		ParseMode: tele.ModeHTML,
		ReplyMarkup: &tele.ReplyMarkup{
			InlineKeyboard: [][]tele.InlineButton{},
		},
	})

	_ = t.bot.React(chat, &tele.Message{ID: msgID, Chat: chat}, tele.Reactions{
		Reactions: []tele.Reaction{{Type: tele.ReactionTypeEmoji, Emoji: emoji}},
	})
}

func (t *Telegram) handleConfirmCallback(c tele.Context) (bool, error) {
	cb := c.Callback()
	if cb == nil {
		return false, nil
	}

	reqID, approval, ok := parseConfirmCallback(cb.Data)
	if !ok {
		return false, nil
	}

	chatID := callbackChatID(c)
	if chatID == 0 {
		_ = c.Respond(&tele.CallbackResponse{Text: "confirmation chat not found", ShowAlert: true})
		return true, nil
	}

	state, exists := t.popConfirmState(chatID, reqID)
	if !exists {
		_ = c.Respond(&tele.CallbackResponse{Text: "confirmation request not found or expired", ShowAlert: true})
		return true, nil
	}

	select {
	case state.ch <- approval:
	default:
	}

	_ = c.Respond(&tele.CallbackResponse{Text: ""})
	t.respondConfirm(chatID, state.msgID, approval, reqID)
	return true, nil
}

func (t *Telegram) popConfirmState(chatID int64, reqID string) (confirmState, bool) {
	t.confirmMu.Lock()
	defer t.confirmMu.Unlock()

	chatReqs := t.confirms[chatID]
	if len(chatReqs) == 0 {
		return confirmState{}, false
	}

	state, ok := chatReqs[reqID]
	if ok {
		delete(chatReqs, reqID)
		if len(chatReqs) == 0 {
			delete(t.confirms, chatID)
		}
	}

	return state, ok
}

func parseConfirmCallback(data string) (string, tool.Approval, bool) {
	parts := strings.Split(strings.TrimSpace(data), ":")
	if len(parts) != 3 || parts[0] != confirmCallbackPrefix {
		return "", tool.Denied, false
	}

	reqID := strings.TrimSpace(parts[2])
	if reqID == "" {
		return "", tool.Denied, false
	}

	switch strings.ToLower(strings.TrimSpace(parts[1])) {
	case "y":
		return reqID, tool.AllowOnce, true
	case "a":
		return reqID, tool.AllowAlways, true
	case "n":
		return reqID, tool.Denied, true
	default:
		return "", tool.Denied, false
	}
}

func callbackChatID(c tele.Context) int64 {
	if chat := c.Chat(); chat != nil {
		return chat.ID
	}
	cb := c.Callback()
	if cb == nil || cb.Message == nil || cb.Message.Chat == nil {
		return 0
	}
	return cb.Message.Chat.ID
}

func (t *Telegram) sendConfirmRequest(route string, chatID int64, reqID, raw string) int {
	text := fmt.Sprintf("confirm %s\nexecute:\n%s\nchoose: tap button or reply: %s y|a|n", reqID, raw, reqID)
	opts := t.assistantSendOptions(route, chatID, 0, false)
	if opts == nil {
		opts = &tele.SendOptions{}
	} else {
		opts = cloneSendOptions(opts)
	}
	opts.ReplyMarkup = confirmMarkup(reqID)
	return t.sendTextWithOptions(chatID, text, opts)
}

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

func confirmMarkup(reqID string) *tele.ReplyMarkup {
	allow := tele.InlineButton{Text: "Approve", Data: confirmCallbackPrefix + ":y:" + reqID}
	always := tele.InlineButton{Text: "Always Allow", Data: confirmCallbackPrefix + ":a:" + reqID}
	deny := tele.InlineButton{Text: "Deny", Data: confirmCallbackPrefix + ":n:" + reqID}
	return &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{allow, always, deny}}}
}

func (t *Telegram) flushStreamLoop(ctx context.Context) {
	ticker := time.NewTicker(telegramStreamMaxWait)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.flushStreams(false)
		case <-t.stop:
			t.flushStreams(true)
			return
		case <-ctx.Done():
			t.flushStreams(true)
			return
		}
	}
}

func (t *Telegram) flushStreams(force bool) {
	t.streamMu.Lock()
	routes := make([]string, 0, len(t.streams))
	for route := range t.streams {
		routes = append(routes, route)
	}
	t.streamMu.Unlock()

	for _, route := range routes {
		t.flushStream(route, force)
	}
}

func (t *Telegram) flushStream(route string, force bool) {
	for {
		t.streamMu.Lock()
		s, ok := t.streams[route]
		if !ok {
			t.streamMu.Unlock()
			return
		}
		chatID := s.chatID
		if s.flush {
			t.debugf("skip flush chat=%d force=%v reason=already_flushing", chatID, force)
			t.streamMu.Unlock()
			return
		}
		if !s.dirty {
			t.streamMu.Unlock()
			return
		}
		s.flush = true
		text := s.buf.String()
		msgID := s.msgID
		ended := s.ended
		s.dirty = false
		t.streamMu.Unlock()
		t.debugf("flush stream chat=%d force=%v ended=%v msg_id=%d text_size=%d", chatID, force, ended, msgID, len([]rune(text)))

		out := tgAssistantOut{Text: text}
		replyToID := 0
		if ended {
			out = parseTelegramAssistantOutput(text)
			replyToID = t.resolveReplyToID(route, out)
			if out.Reaction != "" {
				t.applyReaction(route, chatID, out.Reaction, replyToID)
			}
		}

		if ended && (out.Silent || strings.TrimSpace(out.Text) == "") {
			if msgID != 0 {
				if err := t.bot.Delete(&tele.Message{ID: msgID, Chat: &tele.Chat{ID: chatID}}); err != nil {
					t.errorf("stream delete error: %v", err)
				}
			}
			t.streamMu.Lock()
			if cur, ok := t.streams[route]; ok {
				cur.flush = false
				delete(t.streams, route)
			}
			t.streamMu.Unlock()
			return
		}

		parts := splitText(out.Text, telegramMsgLimit)
		if len(parts) == 0 {
			if ended {
				t.streamMu.Lock()
				if cur, ok := t.streams[route]; ok {
					cur.flush = false
					delete(t.streams, route)
				}
				t.streamMu.Unlock()
			}
			t.streamMu.Lock()
			if cur, ok := t.streams[route]; ok {
				cur.flush = false
			}
			t.streamMu.Unlock()
			return
		}

		chat := &tele.Chat{ID: chatID}
		first := parts[0]
		if msgID == 0 {
			sendOpts := t.assistantSendOptions(route, chatID, replyToID, true)
			sent, err := t.sendWithOpts(chat, first, sendOpts)
			if err != nil {
				t.errorf("stream send error: %v", err)
			} else {
				msgID = sent.ID
			}
		} else {
			if _, err := t.bot.Edit(&tele.Message{ID: msgID, Chat: chat}, tgRenderText(first), &tele.SendOptions{ParseMode: tele.ModeHTML}); err != nil {
				t.errorf("stream edit error: %v", err)
			}
		}

		t.streamMu.Lock()
		shouldContinue := false
		if s, ok := t.streams[route]; ok {
			s.msgID = msgID
			s.at = time.Now()
			if !ended && s.dirty {
				shouldContinue = true
			}
			s.flush = false
		}
		t.streamMu.Unlock()

		if ended {
			threadOpts := t.assistantSendOptions(route, chatID, 0, false)
			for _, part := range parts[1:] {
				t.sendTextWithOptions(chatID, part, threadOpts)
			}

			t.streamMu.Lock()
			delete(t.streams, route)
			t.streamMu.Unlock()
			return
		}

		if !shouldContinue {
			return
		}
	}
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

func (t *Telegram) handleToolCall(route string, chatID int64) {
	t.setProgress(route, chatID, "[tool] calling tools...")
}

func (t *Telegram) handleToolResult(_ string, _ int64) {}

func (t *Telegram) handleToolError(route string, chatID int64) {
	t.setProgress(route, chatID, "[tool] error, retrying strategy...")
}

func (t *Telegram) setProgress(route string, chatID int64, text string) {
	text = strings.TrimSpace(text)
	if text == "" || t.bot == nil {
		return
	}

	t.streamMu.Lock()
	s, ok := t.streams[route]
	if !ok {
		s = &streamState{chatID: chatID}
		t.streams[route] = s
	}
	if s.chatID == 0 {
		s.chatID = chatID
	}
	if s.progressText == text && s.progressMsgID != 0 {
		t.streamMu.Unlock()
		return
	}
	msgID := s.progressMsgID
	s.progressText = text
	t.streamMu.Unlock()

	chat := &tele.Chat{ID: chatID}
	if msgID != 0 {
		if _, err := t.bot.Edit(&tele.Message{ID: msgID, Chat: chat}, tgRenderText(text), &tele.SendOptions{ParseMode: tele.ModeHTML}); err == nil {
			return
		}
	}

	opts := t.assistantSendOptions(route, chatID, 0, true)
	sent, err := t.sendWithOpts(chat, text, opts)
	if err != nil || sent == nil {
		return
	}

	t.streamMu.Lock()
	if cur, ok := t.streams[route]; ok {
		cur.progressMsgID = sent.ID
		cur.progressText = text
	}
	t.streamMu.Unlock()
}

func (t *Telegram) clearProgress(route string) {
	if t.bot == nil {
		return
	}

	t.streamMu.Lock()
	s, ok := t.streams[route]
	if !ok || s.progressMsgID == 0 {
		t.streamMu.Unlock()
		return
	}
	msgID := s.progressMsgID
	chatID := s.chatID
	s.progressMsgID = 0
	s.progressText = ""
	t.streamMu.Unlock()

	if chatID == 0 || msgID == 0 {
		return
	}
	_ = t.bot.Delete(&tele.Message{ID: msgID, Chat: &tele.Chat{ID: chatID}})
}
