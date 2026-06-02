package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/tool"
	tele "gopkg.in/telebot.v4"
)

const (
	approvalTimeout   = 60 * time.Second
	approvalPrefix    = "approve"
	approvalReplyHelp = "reply: <id> y|a|n"
)

type approver struct {
	bot *tele.Bot

	mu      sync.Mutex
	pending map[int64]map[string]pendingApproval
}

type pendingApproval struct {
	ch       chan tool.Approval
	msgID    int
	threadID int
}

func newApprover(bot *tele.Bot) *approver {
	return &approver{
		bot:     bot,
		pending: map[int64]map[string]pendingApproval{},
	}
}

func (a *approver) Approve(ctx context.Context, req tool.Request) (tool.Approval, error) {
	chatID, threadID, ok := parseTelegramSource(command.SourceFrom(ctx))
	if !ok {
		return tool.Denied, nil
	}
	reqID := approvalID()
	ch := make(chan tool.Approval, 1)

	a.mu.Lock()
	if a.pending[chatID] == nil {
		a.pending[chatID] = map[string]pendingApproval{}
	}
	a.pending[chatID][reqID] = pendingApproval{ch: ch, threadID: threadID}
	a.mu.Unlock()

	msgID := a.send(chatID, threadID, reqID, req)
	if msgID > 0 {
		a.mu.Lock()
		state := a.pending[chatID][reqID]
		state.msgID = msgID
		a.pending[chatID][reqID] = state
		a.mu.Unlock()
	}

	defer a.drop(chatID, reqID)

	select {
	case v := <-ch:
		a.finish(chatID, reqID, v)
		return v, nil
	case <-ctx.Done():
		return tool.Denied, ctx.Err()
	case <-time.After(approvalTimeout):
		a.notice(chatID, threadID, fmt.Sprintf("approval %s timed out", reqID))
		return tool.Denied, nil
	}
}

func (a *approver) handleText(c tele.Context) bool {
	msg := c.Message()
	if msg == nil || msg.Chat == nil {
		return false
	}
	reqID, approval, ok := parseApprovalReply(strings.TrimSpace(c.Text()))
	if !ok {
		return false
	}
	if a.resolve(msg.Chat.ID, reqID, approval) {
		_ = c.Send("approval recorded")
		return true
	}
	_ = c.Send("approval request not found")
	return true
}

func (a *approver) handleCallback(c tele.Context) bool {
	cb := c.Callback()
	if cb == nil || cb.Message == nil || cb.Message.Chat == nil {
		return false
	}
	reqID, approval, ok := parseApprovalCallback(cb.Data)
	if !ok {
		return false
	}
	if !a.resolve(cb.Message.Chat.ID, reqID, approval) {
		_ = c.Respond(&tele.CallbackResponse{Text: "approval not found", ShowAlert: true})
		return true
	}
	_ = c.Respond()
	return true
}

func (a *approver) resolve(chatID int64, reqID string, approval tool.Approval) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	chat := a.pending[chatID]
	if len(chat) == 0 {
		return false
	}
	state, ok := chat[reqID]
	if !ok {
		return false
	}
	select {
	case state.ch <- approval:
	default:
	}
	return true
}

func (a *approver) drop(chatID int64, reqID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if chat := a.pending[chatID]; chat != nil {
		delete(chat, reqID)
		if len(chat) == 0 {
			delete(a.pending, chatID)
		}
	}
}

func (a *approver) finish(chatID int64, reqID string, approval tool.Approval) {
	a.mu.Lock()
	state, ok := a.pending[chatID][reqID]
	a.mu.Unlock()
	if !ok || state.msgID == 0 || a.bot == nil {
		return
	}
	label := "denied"
	if approval == tool.AllowOnce {
		label = "approved once"
	}
	if approval == tool.AllowAlways {
		label = "approved always"
	}
	chat := &tele.Chat{ID: chatID}
	_, _ = a.bot.Edit(&tele.Message{ID: state.msgID, Chat: chat}, fmt.Sprintf("approval %s: %s", reqID, label), &tele.SendOptions{
		ReplyMarkup: &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{}},
	})
}

func (a *approver) send(chatID int64, threadID int, reqID string, req tool.Request) int {
	if a.bot == nil {
		return 0
	}
	text := fmt.Sprintf("approval %s\n%s\n%s", reqID, req.Action, approvalReplyHelp)
	opts := &tele.SendOptions{ReplyMarkup: approvalMarkup(reqID)}
	if threadID > 0 {
		opts.ThreadID = threadID
	}
	msg, err := a.bot.Send(&tele.Chat{ID: chatID}, text, opts)
	if err != nil || msg == nil {
		return 0
	}
	return msg.ID
}

func (a *approver) notice(chatID int64, threadID int, text string) {
	if a.bot == nil {
		return
	}
	opts := &tele.SendOptions{}
	if threadID > 0 {
		opts.ThreadID = threadID
	}
	_, _ = a.bot.Send(&tele.Chat{ID: chatID}, text, opts)
}

func approvalMarkup(reqID string) *tele.ReplyMarkup {
	return &tele.ReplyMarkup{InlineKeyboard: [][]tele.InlineButton{{
		{Text: "Approve", Data: approvalPrefix + ":y:" + reqID},
		{Text: "Always", Data: approvalPrefix + ":a:" + reqID},
		{Text: "Deny", Data: approvalPrefix + ":n:" + reqID},
	}}}
}

func parseTelegramSource(src string) (int64, int, bool) {
	src = strings.TrimSpace(src)
	var rest string
	switch {
	case strings.HasPrefix(src, "tg:dm:"):
		rest = strings.TrimPrefix(src, "tg:dm:")
	case strings.HasPrefix(src, "tg:channel:"):
		rest = strings.TrimPrefix(src, "tg:channel:")
	default:
		return 0, 0, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) == 0 {
		return 0, 0, false
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || chatID == 0 {
		return 0, 0, false
	}
	if len(parts) < 2 {
		return chatID, 0, true
	}
	threadID, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || threadID <= 0 {
		return chatID, 0, true
	}
	return chatID, threadID, true
}

func parseApprovalReply(text string) (string, tool.Approval, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) != 2 {
		return "", tool.Denied, false
	}
	approval, ok := parseApprovalToken(parts[1])
	if !ok {
		return "", tool.Denied, false
	}
	return parts[0], approval, true
}

func parseApprovalCallback(data string) (string, tool.Approval, bool) {
	parts := strings.Split(strings.TrimSpace(data), ":")
	if len(parts) != 3 || parts[0] != approvalPrefix {
		return "", tool.Denied, false
	}
	reqID := strings.TrimSpace(parts[2])
	approval, ok := parseApprovalToken(parts[1])
	if !ok || reqID == "" {
		return "", tool.Denied, false
	}
	return reqID, approval, true
}

func parseApprovalToken(raw string) (tool.Approval, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "y":
		return tool.AllowOnce, true
	case "a":
		return tool.AllowAlways, true
	case "n":
		return tool.Denied, true
	default:
		return tool.Denied, false
	}
}

func approvalID() string {
	return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
}
