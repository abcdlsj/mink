package platform

import (
	"time"

	tele "gopkg.in/telebot.v4"
)

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
