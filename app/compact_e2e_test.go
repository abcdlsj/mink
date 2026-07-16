package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/agent"
	"github.com/abcdlsj/sumi/config"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/space"
)

// e2eDirectApp builds an App wired for the real CLI direct entry (HandleInput
// with a "cli:direct:<key>" source), with a finite native model window so the
// hard-overflow guard is armed. Budget = ContextWindow - MaxTokens -
// ReserveTokens = 200 - 10 - 10 = 180 tok.
func e2eDirectApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	a, err := New(config.Config{
		Runtime:     "native",
		DataDir:     filepath.Join(dir, "sumi-data"),
		Workspace:   dir,
		MaxTokens:   10,
		ActiveModel: "main",
		Default:     "main",
		Models: map[string]config.ModelConfig{
			"main": {Provider: "openai", Model: "test", APIKey: "test-key", MaxTokens: 10, ContextWindow: 200},
		},
		Compact: config.CompactConfig{Auto: true, KeepRecentMessages: 2, ReserveTokens: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.provider = &stubSummarizer{}
	return a
}

// TestDirectDraftFailsClosedThroughRealEntry is the e2e contract for the CLI
// Draft lifecycle, using a REAL in-memory Draft (not a persisted-key look-alike).
// The CLI shell creates a Draft via Spaces().DraftSpace — it lives only in the
// drafts map and is NOT in the store until the first user message persists it —
// then drives turns through source "cli:direct:<draftSpace.ID>". This test
// reproduces that exact shell shape and proves: (1) before sending, no persisted
// Space exists; (2) the first over-window input persists the user turn (Draft ->
// first persisted turn) and only THEN hits the hard-overflow preflight, which
// fails closed with ErrContextOverflow; (3) the runtime never ran and no
// assistant reply was faked. This is the full entry (route -> directConversation
// -> Draft resolve/persist -> autoCompact), not autoCompact in isolation.
func TestDirectDraftFailsClosedThroughRealEntry(t *testing.T) {
	a := e2eDirectApp(t)

	var ran bool
	a.RegisterRuntime("native", func(env *agent.RuntimeEnv) (agent.Runtime, error) {
		return runtimeFunc(func(ctx context.Context, turn *agent.Turn) error {
			ran = true
			turn.Session.Add(msg.Message{Role: "assistant", Content: "should not happen"})
			return nil
		}), nil
	})

	// Real Draft, exactly as cli/shell.go resolveLaunchSpace does for a fresh
	// direct chat: DraftSpace(KindDirectChat, "cli:direct:<uuid>") held in-memory,
	// then source = "cli:direct:<draftSpace.ID>".
	draft, err := a.Spaces().DraftSpace(space.KindDirectChat, "cli:direct:e2e-"+t.Name(), "", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	source := "cli:direct:" + draft.ID

	// Pre-send: the Draft is in-memory only. The store must not know it yet —
	// neither by kind+key nor in the persisted listing.
	if got, err := a.Spaces().Store().FindSpaceByKindAndKey(space.KindDirectChat, draft.Key); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Fatalf("draft must not be persisted before first message, found %+v", got)
	}
	if persisted, err := a.Spaces().ListSpaces(); err != nil {
		t.Fatal(err)
	} else if len(persisted) != 0 {
		t.Fatalf("no persisted spaces should exist before first message, got %d", len(persisted))
	}

	// The pending turn alone (~251 tok) exceeds the ~180 usable window. No history
	// exists yet, so this is unambiguously the pending-alone fail-closed case.
	_, err = a.HandleInput(context.Background(), source, strings.Repeat("x", 1000))
	if !errors.Is(err, ErrContextOverflow) {
		t.Fatalf("real Draft entry must fail closed on pending-alone overflow, got %v", err)
	}
	if !strings.Contains(err.Error(), "pending input") {
		t.Fatalf("error should identify the pending-alone case, got %v", err)
	}
	if ran {
		t.Fatalf("runtime ran despite overflow preflight failure through the real entry")
	}

	// Post-send: the Draft was resolved and PERSISTED by the first user message
	// before the preflight — so the user's turn is durable (the conversation is
	// not lost), the Space now exists in the store, but no assistant reply was
	// faked on the fail-closed path.
	sp, err := a.Spaces().LoadSpace(draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sp == nil {
		t.Fatalf("draft space should have been persisted by the first user message")
	}
	if got, err := a.Spaces().Store().FindSpaceByKindAndKey(space.KindDirectChat, draft.Key); err != nil {
		t.Fatal(err)
	} else if got == nil {
		t.Fatalf("space must be in the store after the first message persisted it")
	}
	var sawUser, sawAssistant bool
	for _, m := range sp.Messages {
		switch m.AuthorKind {
		case space.ParticipantUser:
			sawUser = true
		case space.ParticipantAgent:
			sawAssistant = true
		}
	}
	if !sawUser {
		t.Fatalf("user turn must be persisted in the space, messages=%+v", sp.Messages)
	}
	if sawAssistant {
		t.Fatalf("no assistant reply should be persisted on fail-closed, messages=%+v", sp.Messages)
	}
}
