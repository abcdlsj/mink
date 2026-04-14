package platform

import (
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
