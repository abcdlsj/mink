package telegrambot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/tool"
	tele "gopkg.in/telebot.v4"
)

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

func confirmMarkup(reqID string) *tele.ReplyMarkup {
	allow := tele.InlineButton{Text: "Approve", Data: confirmCallbackPrefix + ":y:" + reqID}
	always := tele.InlineButton{Text: "Always Allow", Data: confirmCallbackPrefix + ":a:" + reqID}
	deny := tele.InlineButton{Text: "Deny", Data: confirmCallbackPrefix + ":n:" + reqID}
	return &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{allow, always, deny}}}
}
