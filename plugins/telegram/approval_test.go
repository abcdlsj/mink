package telegram

import (
	"testing"

	"github.com/abcdlsj/sumi/tool"
)

func TestParseApprovalReply(t *testing.T) {
	reqID, approval, ok := parseApprovalReply("abcd1234 a")
	if !ok {
		t.Fatal("expected parse ok")
	}
	if reqID != "abcd1234" {
		t.Fatalf("reqID = %q", reqID)
	}
	if approval != tool.AllowAlways {
		t.Fatalf("approval = %v", approval)
	}
}

func TestParseApprovalCallback(t *testing.T) {
	reqID, approval, ok := parseApprovalCallback("approve:y:beefcafe")
	if !ok {
		t.Fatal("expected parse ok")
	}
	if reqID != "beefcafe" {
		t.Fatalf("reqID = %q", reqID)
	}
	if approval != tool.AllowOnce {
		t.Fatalf("approval = %v", approval)
	}
}

func TestParseTelegramSource(t *testing.T) {
	cases := []struct {
		src      string
		wantChat int64
		wantThr  int
		wantOK   bool
	}{
		{"tg:dm:42", 42, 0, true},
		{"tg:dm:42:7", 42, 7, true},
		{"tg:channel:42", 42, 0, true},
		{"tg:channel:42:7", 42, 7, true},
		{"telegram:42:7", 0, 0, false},
		{"desktop", 0, 0, false},
	}
	for _, c := range cases {
		chatID, threadID, ok := parseTelegramSource(c.src)
		if ok != c.wantOK || chatID != c.wantChat || threadID != c.wantThr {
			t.Errorf("parseTelegramSource(%q) = %d / %d / %v, want %d / %d / %v",
				c.src, chatID, threadID, ok, c.wantChat, c.wantThr, c.wantOK)
		}
	}
}
