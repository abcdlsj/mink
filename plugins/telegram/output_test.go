package telegram

import (
	"testing"

	tele "gopkg.in/telebot.v4"
)

func TestParseTelegramOutput(t *testing.T) {
	out := parseOutput("[[reply_to:42]] [[react:👍]] done")
	if out.Text != "done" {
		t.Fatalf("text = %q", out.Text)
	}
	if out.ReplyToID != 42 {
		t.Fatalf("reply_to = %d", out.ReplyToID)
	}
	if out.Reaction != "👍" {
		t.Fatalf("reaction = %q", out.Reaction)
	}
	if !out.HasAction {
		t.Fatal("expected action")
	}
}

func TestParseTelegramOutputSilent(t *testing.T) {
	out := parseOutput("NO_REPLY")
	if !out.Silent {
		t.Fatal("expected silent")
	}
	if out.Text != "" {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestParseTelegramOutputReplyCurrent(t *testing.T) {
	out := parseOutput("[[reply_to_current]] hi")
	if !out.ReplyNow {
		t.Fatal("expected reply_now")
	}
	if out.Text != "hi" {
		t.Fatalf("text = %q", out.Text)
	}
}

func TestSendOptionsOmitsNilReply(t *testing.T) {
	opts := sendOptions(nil)
	if len(opts) != 0 {
		t.Fatalf("len = %d", len(opts))
	}
}

func TestSendOptionsRepliesToMessage(t *testing.T) {
	reply := &tele.Message{ID: 42}
	opts := sendOptions(reply)
	if len(opts) != 1 {
		t.Fatalf("len = %d", len(opts))
	}
	opt, ok := opts[0].(*tele.SendOptions)
	if !ok {
		t.Fatalf("option type = %T", opts[0])
	}
	if opt.ReplyTo != reply {
		t.Fatalf("reply = %v", opt.ReplyTo)
	}
}

func TestNoticeReplyTargetRequiresExplicitReplyID(t *testing.T) {
	chat := &tele.Chat{ID: 7}
	if got := noticeReplyTarget(chat, output{ReplyNow: true}); got != nil {
		t.Fatalf("target = %#v", got)
	}
	got := noticeReplyTarget(chat, output{ReplyToID: 42})
	if got == nil || got.ID != 42 || got.Chat != chat {
		t.Fatalf("target = %#v", got)
	}
}
