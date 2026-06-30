package space

import (
	"reflect"
	"testing"
)

func snapshotFromList(personas []PersonaInfo) PersonaSnapshot {
	byID := map[string]PersonaInfo{}
	byDisplay := map[string]PersonaInfo{}
	for _, p := range personas {
		byID[p.ID] = p
		byDisplay[p.Display] = p
	}
	return func(id string) (PersonaInfo, bool) {
		if p, ok := byID[id]; ok {
			return p, true
		}
		if p, ok := byDisplay[id]; ok {
			return p, true
		}
		return PersonaInfo{}, false
	}
}

func newRouterTestEnv(t *testing.T) (*Router, *Manager, *Space) {
	t.Helper()
	store := newMemoryStore()
	mgr := NewManager(store, "user", "You")
	personas := []PersonaInfo{
		{ID: "coder", Display: "Coder"},
		{ID: "reviewer", Display: "Reviewer"},
	}
	router := NewRouter(mgr, snapshotFromList(personas), 4)
	ch, err := mgr.EnsureSpace(KindChannel, "default", PersonaInfo{})
	if err != nil {
		t.Fatalf("EnsureSpace: %v", err)
	}
	return router, mgr, ch
}

func TestRouterUserMessageWithMentionWakesOnce(t *testing.T) {
	router, mgr, ch := newRouterTestEnv(t)
	wakes, notices, err := router.RouteUserChannelMessage(ch.ID, "@coder look", "", nil)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(wakes) != 1 || wakes[0].AgentID != "coder" {
		t.Errorf("expected single coder wake, got %+v", wakes)
	}
	if len(notices) != 0 {
		t.Errorf("expected no notices, got %+v", notices)
	}
	loaded, _ := mgr.store.LoadSpace(ch.ID)
	if !loaded.HasParticipant("coder") {
		t.Error("coder should have been atomically added to participants")
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("user message not persisted, got %d", len(loaded.Messages))
	}
	if !reflect.DeepEqual(loaded.Messages[0].Mentions, []string{"coder"}) {
		t.Errorf("message mentions = %v, want [coder]", loaded.Messages[0].Mentions)
	}
}

func TestRouterUserMessageNoMentionDoesNotWakeButPersists(t *testing.T) {
	router, mgr, ch := newRouterTestEnv(t)
	wakes, notices, err := router.RouteUserChannelMessage(ch.ID, "just thinking out loud", "", nil)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(wakes) != 0 {
		t.Errorf("no-mention message must not wake; got %+v", wakes)
	}
	if len(notices) != 1 || notices[0].Kind != NoticeChannelNoTarget {
		t.Errorf("expected single no_target notice, got %+v", notices)
	}
	loaded, _ := mgr.store.LoadSpace(ch.ID)
	if len(loaded.Messages) != 1 {
		t.Errorf("user message should still persist, got %d", len(loaded.Messages))
	}
	for _, p := range loaded.Participants {
		if p.Kind == ParticipantAgent {
			t.Errorf("no-mention message should not add agents, found %v", p)
		}
	}
}

func TestRouterListeningAgentDoesNotWakeOnPlainMessage(t *testing.T) {
	router, mgr, ch := newRouterTestEnv(t)
	if err := mgr.SetAgentMode(ch.ID, "coder", "listen"); err != nil {
		t.Fatalf("SetAgentMode: %v", err)
	}
	wakes, notices, err := router.RouteUserChannelMessage(ch.ID, "just thinking out loud", "", nil)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(wakes) != 0 {
		t.Fatalf("plain message must not wake listening agent, got %+v", wakes)
	}
	if len(notices) != 1 || notices[0].Kind != NoticeListeningNoMatch {
		t.Fatalf("expected listening_no_match notice, got %+v", notices)
	}
}

func TestRouterThreadListeningAgentDoesNotWakeOnPlainMessage(t *testing.T) {
	router, mgr, ch := newRouterTestEnv(t)
	root, err := mgr.AppendUserMessage(ch.ID, "root", nil)
	if err != nil {
		t.Fatalf("AppendUserMessage: %v", err)
	}
	if err := mgr.SetAgentMode(ch.ID, "reviewer", "listen"); err != nil {
		t.Fatalf("SetAgentMode: %v", err)
	}
	if err := mgr.SetThreadAgentMode(ch.ID, root.ID, "coder", "listen"); err != nil {
		t.Fatalf("SetThreadAgentMode: %v", err)
	}
	wakes, notices, err := router.RouteUserChannelMessage(ch.ID, "thread follow-up", root.ID, nil)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(wakes) != 0 {
		t.Fatalf("thread plain message must not wake listening agent, got %+v", wakes)
	}
	if len(notices) != 1 || notices[0].Kind != NoticeListeningNoMatch {
		t.Fatalf("expected listening_no_match notice, got %+v", notices)
	}
}

func TestRouterMultipleListeningAgentsAreAmbiguous(t *testing.T) {
	router, mgr, ch := newRouterTestEnv(t)
	if err := mgr.SetAgentMode(ch.ID, "coder", "listen"); err != nil {
		t.Fatalf("SetAgentMode coder: %v", err)
	}
	if err := mgr.SetAgentMode(ch.ID, "reviewer", "listen"); err != nil {
		t.Fatalf("SetAgentMode reviewer: %v", err)
	}
	wakes, notices, err := router.RouteUserChannelMessage(ch.ID, "just thinking out loud", "", nil)
	if err != nil {
		t.Fatalf("route: %v", err)
	}
	if len(wakes) != 0 {
		t.Fatalf("ambiguous listening must not wake, got %+v", wakes)
	}
	if len(notices) != 1 || notices[0].Kind != NoticeListeningAmbiguous {
		t.Fatalf("expected listening ambiguous notice, got %+v", notices)
	}
}

func TestRouterUnknownMentionDropsSilently(t *testing.T) {
	router, mgr, ch := newRouterTestEnv(t)
	wakes, notices, _ := router.RouteUserChannelMessage(ch.ID, "@nobody hi", "", nil)
	if len(wakes) != 0 {
		t.Errorf("unknown mention must not wake, got %+v", wakes)
	}
	if len(notices) != 1 || notices[0].Kind != NoticeChannelNoTarget {
		t.Errorf("unknown mention should still trip the no_target notice (the channel produced no wake), got %+v", notices)
	}
	loaded, _ := mgr.store.LoadSpace(ch.ID)
	for _, p := range loaded.Participants {
		if p.ID == "nobody" {
			t.Errorf("unknown id must not be added to participants, got %v", p)
		}
	}
}

func TestRouterMultipleMentionsWakeAll(t *testing.T) {
	router, _, ch := newRouterTestEnv(t)
	wakes, _, _ := router.RouteUserChannelMessage(ch.ID, "@coder and @reviewer please", "", nil)
	got := make([]string, 0, len(wakes))
	for _, w := range wakes {
		got = append(got, w.AgentID)
	}
	if !reflect.DeepEqual(got, []string{"coder", "reviewer"}) {
		t.Errorf("wakes = %v, want [coder reviewer]", got)
	}
	if wakes[0].Chain == nil || wakes[1].Chain == nil {
		t.Error("each wake target must reference the chain")
	}
	if wakes[0].Chain != wakes[1].Chain {
		t.Error("simultaneous wakes from one user message must share the chain")
	}
}

func TestRouterAgentReplyHonorsBudget(t *testing.T) {
	router, _, ch := newRouterTestEnv(t)
	wakes1, _, _ := router.RouteUserChannelMessage(ch.ID, "@coder look", "", nil)
	if len(wakes1) != 1 {
		t.Fatalf("expected coder wake, got %+v", wakes1)
	}
	chain := wakes1[0].Chain

	wakes2, notices2, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "msg-coder-reply", "looking, ping @reviewer for a second pair", "coder")
	if len(wakes2) != 1 || wakes2[0].AgentID != "reviewer" {
		t.Errorf("coder->reviewer wake failed: %+v", wakes2)
	}
	if len(notices2) != 0 {
		t.Errorf("expected no notices, got %+v", notices2)
	}

	wakes3, _, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "msg-reviewer-reply", "thanks @tshoot", "reviewer")
	if len(wakes3) != 0 {
		t.Errorf("unknown @ in reply must not wake, got %+v", wakes3)
	}

	wakes4, notices4, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "msg-reviewer-reply-2", "hey @coder one more", "reviewer")
	if len(wakes4) != 1 || wakes4[0].AgentID != "coder" {
		t.Errorf("coder should be wakable again while budget remains, got %+v", wakes4)
	}
	if len(notices4) != 0 {
		t.Errorf("expected no duplicate notice, got %+v", notices4)
	}
}

func TestRouterAgentReplyWithoutChain(t *testing.T) {
	router, _, ch := newRouterTestEnv(t)
	wakes, notices, _ := router.RouteAgentReply(ch.ID, "missing-root", "reply-1", "hi @coder", "reviewer")
	if len(wakes) != 0 {
		t.Errorf("missing chain must not wake, got %+v", wakes)
	}
	if len(notices) != 1 || notices[0].Kind != NoticeBudgetExhausted {
		t.Errorf("missing chain should produce budget_exhausted notice, got %+v", notices)
	}
}

func TestRouterAgentCannotWakeItself(t *testing.T) {
	router, _, ch := newRouterTestEnv(t)
	wakes1, _, _ := router.RouteUserChannelMessage(ch.ID, "@coder do it", "", nil)
	chain := wakes1[0].Chain

	wakes2, _, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "reply-1", "hmm @coder", "coder")
	if len(wakes2) != 0 {
		t.Errorf("self-mention must not wake; got %+v", wakes2)
	}
}

func TestRouterFanOutBudgetCapsAcrossReplies(t *testing.T) {
	router, _, ch := newRouterTestEnv(t)
	wakes1, _, _ := router.RouteUserChannelMessage(ch.ID, "@coder", "", nil)
	chain := wakes1[0].Chain

	wakes2, _, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "r1", "@reviewer", "coder")
	if len(wakes2) != 1 {
		t.Fatalf("coder->reviewer should wake, got %+v", wakes2)
	}

	wakes3, notices, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "r2", "@coder", "reviewer")
	if len(wakes3) != 1 || wakes3[0].AgentID != "coder" {
		t.Errorf("budget should allow coder to re-enter once, got %+v", wakes3)
	}
	if len(notices) != 0 {
		t.Errorf("expected no notice before budget exhaustion, got %+v", notices)
	}

	wakes4, notices4, _ := router.RouteAgentReply(ch.ID, chain.RootMessageID, "r3", "@reviewer", "coder")
	if len(wakes4) != 0 {
		t.Errorf("budget exhausted should not wake reviewer, got %+v", wakes4)
	}
	if len(notices4) != 1 || notices4[0].AgentID != "reviewer" || notices4[0].Kind != NoticeBudgetExhausted {
		t.Errorf("expected budget_exhausted notice for reviewer, got %+v", notices4)
	}
}
