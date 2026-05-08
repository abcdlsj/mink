package session

import (
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/msg"
)

func TestNewSessionIDIncludesDateAndSourceTag(t *testing.T) {
	s := New("telegram:42:7")

	parts := strings.Split(s.ID, "-")
	if len(parts) != 3 {
		t.Fatalf("got id %q", s.ID)
	}
	if len(parts[0]) != 8 {
		t.Fatalf("got date %q", parts[0])
	}
	if parts[1] != "telegram" {
		t.Fatalf("got source tag %q", parts[1])
	}
	if len(parts[2]) != 8 {
		t.Fatalf("got hash %q", parts[2])
	}
	for _, r := range parts[2] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("hash is not hex: %q", parts[2])
		}
	}
}

func TestCompactKeepsSummaryAndRecentMessages(t *testing.T) {
	s := New("cli")
	for i := 0; i < 5; i++ {
		s.Add(msg.Message{Role: "user", Content: "m"})
	}

	s.Compact("summary", 2)

	if s.Summary != "summary" {
		t.Fatalf("got summary %q", s.Summary)
	}
	if len(s.Messages) != 3 {
		t.Fatalf("got %d messages", len(s.Messages))
	}
	if s.Messages[0].Role != "system" {
		t.Fatalf("expected system summary message")
	}
}

func TestCompactAllowsKeepingZeroRecentMessages(t *testing.T) {
	s := New("cli")
	for i := 0; i < 3; i++ {
		s.Add(msg.Message{Role: "user", Content: "m"})
	}

	s.Compact("summary", 0)

	if len(s.Messages) != 1 {
		t.Fatalf("got %d messages", len(s.Messages))
	}
	if s.Messages[0].Role != "system" {
		t.Fatalf("expected system summary message")
	}
}
