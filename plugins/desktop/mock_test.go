package desktop

import (
	"strings"
	"testing"
)

func TestMockChannelsHaveExpectedShape(t *testing.T) {
	chs := mockChannels()
	if len(chs) == 0 {
		t.Fatal("mockChannels empty")
	}
	for _, c := range chs {
		if c.ID == "" || c.Name == "" {
			t.Errorf("channel missing id/name: %#v", c)
		}
		if strings.HasPrefix(c.Name, "#") {
			t.Errorf("channel name should not include #: %q", c.Name)
		}
	}
}

func TestMockThreadsLinkToChannels(t *testing.T) {
	chs := mockChannels()
	chIDs := map[string]bool{}
	for _, c := range chs {
		chIDs[c.ID] = true
	}
	for _, th := range mockThreads() {
		if !chIDs[th.ChannelID] {
			t.Errorf("thread %q references unknown channel %q", th.ID, th.ChannelID)
		}
	}
}

func TestMockChannelDetailFollowsRequestedChannel(t *testing.T) {
	for _, c := range mockChannels() {
		got := mockChannelDetail(c.ID)
		if got.Item.ID != c.ID {
			t.Errorf("mockChannelDetail(%q): item.ID=%q", c.ID, got.Item.ID)
		}
		if !strings.HasPrefix(got.Item.Title, "#") {
			t.Errorf("channel title should start with #: %q", got.Item.Title)
		}
	}
}

func TestMockThreadDetailMessagesHaveAuthors(t *testing.T) {
	d := mockThreadDetail("th-fallback")
	if len(d.Messages) == 0 {
		t.Fatal("thread detail has no messages")
	}
	for _, m := range d.Messages {
		if m.Role == "agent" && m.AuthorID == "" {
			t.Errorf("agent message missing author_id: %+v", m)
		}
	}
}

func TestMockParticipantsScopedByView(t *testing.T) {
	channelOnly := mockParticipants("ch-coding", "")
	threadView := mockParticipants("ch-coding", "th-fallback")
	if len(threadView.ActiveRuns) == 0 {
		t.Fatal("thread view should expose active runs")
	}
	if len(channelOnly.ActiveRuns) != 0 {
		t.Errorf("channel view active runs should be empty in mock, got %d", len(channelOnly.ActiveRuns))
	}
}
