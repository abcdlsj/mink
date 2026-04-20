package telegram

import "testing"

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
