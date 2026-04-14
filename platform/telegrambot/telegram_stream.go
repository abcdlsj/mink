package telegrambot

import (
	"context"
	"strings"
	"time"

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
