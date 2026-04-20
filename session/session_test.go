package session

import (
	"testing"

	"github.com/abcdlsj/mink/msg"
)

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
