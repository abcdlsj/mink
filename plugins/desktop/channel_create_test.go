package desktop

import (
	"strings"
	"testing"
)

func TestCreateChannelCreatesNewSpace(t *testing.T) {
	b, a := newThreadBackend(t)
	item, err := b.CreateChannel("research")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Fatal("returned ChannelItem must carry the new Space id")
	}
	if !strings.Contains(item.Name, "research") {
		t.Fatalf("name = %q, expected to contain 'research'", item.Name)
	}
	channels := b.ListChannels()
	found := false
	for _, c := range channels {
		if c.ID == item.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("new channel must appear in ListChannels")
	}
	_ = a
}

func TestCreateChannelEmptyNameRejected(t *testing.T) {
	b, _ := newThreadBackend(t)
	cases := []string{"", "   ", "#", "###"}
	for _, c := range cases {
		if _, err := b.CreateChannel(c); err == nil {
			t.Fatalf("expected error for name = %q", c)
		}
	}
}

func TestCreateChannelDuplicateRejected(t *testing.T) {
	b, _ := newThreadBackend(t)
	if _, err := b.CreateChannel("research"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.CreateChannel("Research"); err == nil {
		t.Fatal("expected error for duplicate (case-insensitive seed)")
	}
	if _, err := b.CreateChannel("#research"); err == nil {
		t.Fatal("expected error for duplicate after stripping leading #")
	}
}

func TestCreateChannelNormalizesSeed(t *testing.T) {
	cases := map[string]string{
		"Research":             "research",
		"Bug Fixes":            "bug-fixes",
		"#design":              "design",
		"  spaces   in   name": "spaces-in-name",
		"hello!@#$%world":      "helloworld",
		"--lead-trim--":        "lead-trim",
	}
	for input, want := range cases {
		got := normalizeChannelSeed(input)
		if got != want {
			t.Errorf("normalizeChannelSeed(%q) = %q, want %q", input, got, want)
		}
	}
}
