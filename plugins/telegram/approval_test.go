package telegram

import (
	"testing"

	"github.com/abcdlsj/mink/tool"
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

func TestParseApprovalSource(t *testing.T) {
	chatID, threadID, ok := parseApprovalSource("telegram:42:7")
	if !ok {
		t.Fatal("expected parse ok")
	}
	if chatID != 42 || threadID != 7 {
		t.Fatalf("got chat=%d thread=%d", chatID, threadID)
	}
}
