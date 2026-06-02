package telegram

import (
	"testing"

	"github.com/abcdlsj/sumi/config"
	tele "gopkg.in/telebot.v4"
)

func TestAllowTelegramMessageAllowsEmptyAllowlist(t *testing.T) {
	msg := telegramMessage(1, 2)
	if !allowTelegramMessage(config.TelegramConfig{}, msg) {
		t.Fatal("empty allowlist should allow")
	}
}

func TestAllowTelegramMessageRejectsUserMiss(t *testing.T) {
	msg := telegramMessage(1, 2)
	cfg := config.TelegramConfig{AllowedUserIDs: []int64{9}}
	if allowTelegramMessage(cfg, msg) {
		t.Fatal("user miss should reject")
	}
}

func TestAllowTelegramMessageRejectsChatMiss(t *testing.T) {
	msg := telegramMessage(1, 2)
	cfg := config.TelegramConfig{AllowedChatIDs: []int64{9}}
	if allowTelegramMessage(cfg, msg) {
		t.Fatal("chat miss should reject")
	}
}

func TestAllowTelegramMessageAllowsConfiguredUserAndChat(t *testing.T) {
	msg := telegramMessage(1, 2)
	cfg := config.TelegramConfig{
		AllowedUserIDs: []int64{1},
		AllowedChatIDs: []int64{2},
	}
	if !allowTelegramMessage(cfg, msg) {
		t.Fatal("configured user and chat should allow")
	}
}

func TestAllowTelegramMessageRejectsMissingSenderWhenUserAllowlistConfigured(t *testing.T) {
	msg := telegramMessage(1, 2)
	msg.Sender = nil
	cfg := config.TelegramConfig{AllowedUserIDs: []int64{1}}
	if allowTelegramMessage(cfg, msg) {
		t.Fatal("missing sender should reject when user allowlist is configured")
	}
}

func telegramMessage(userID, chatID int64) *tele.Message {
	return &tele.Message{
		Sender: &tele.User{ID: userID},
		Chat:   &tele.Chat{ID: chatID, Type: tele.ChatPrivate},
	}
}
