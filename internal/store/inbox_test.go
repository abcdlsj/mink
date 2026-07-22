package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInboxAttentionMentionFollowMuteAndDM(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()

	root, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "root",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	items := fixture.listItems(t, 10, fixture.at(2))
	if len(items) != 1 || items[0].Reason != InboxReasonMention || items[0].TriggerMessageID != root.ID {
		t.Fatalf("mention items = %+v", items)
	}
	claim := fixture.claim(t, items[0].ID, uuid.NewString(), fixture.at(3))
	if _, err := fixture.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		InboxItemID: claim.ID, Now: fixture.at(4),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "thread mention",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	items = fixture.listItems(t, 10, fixture.at(6))
	if len(items) != 1 || items[0].Reason != InboxReasonMention {
		t.Fatalf("thread mention items = %+v", items)
	}
	fixture.claim(t, items[0].ID, uuid.NewString(), fixture.at(7))
	if _, err := fixture.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		InboxItemID: items[0].ID, Now: fixture.at(8),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "followed", Now: fixture.at(9),
	}); err != nil {
		t.Fatal(err)
	}
	items = fixture.listItems(t, 10, fixture.at(10))
	if len(items) != 1 || items[0].Reason != InboxReasonThreadFollow {
		t.Fatalf("follow items = %+v", items)
	}
	fixture.claim(t, items[0].ID, uuid.NewString(), fixture.at(11))
	if _, err := fixture.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		InboxItemID: items[0].ID, Now: fixture.at(12),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		SpaceID: fixture.group.ID, Muted: true, Now: fixture.at(13),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "muted ordinary", Now: fixture.at(14),
	}); err != nil {
		t.Fatal(err)
	}
	if items := fixture.listItems(t, 10, fixture.at(15)); len(items) != 0 {
		t.Fatalf("muted ordinary items = %+v", items)
	}
	mentioned, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "muted mention",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(16),
	})
	if err != nil {
		t.Fatal(err)
	}
	items = fixture.listItems(t, 10, fixture.at(17))
	if len(items) != 1 || items[0].Reason != InboxReasonMention || !reflect.DeepEqual(mentioned.MentionedAgentIDs, []string{fixture.agentID}) {
		t.Fatalf("muted mention = %+v, items = %+v", mentioned, items)
	}

	dm, err := fixture.database.CreateDM(ctx, CreateDMParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Peer: Principal{Kind: "agent", ID: fixture.agentID, OrganizationID: fixture.owner.OrganizationID}, Now: fixture.at(18),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.issueAgentGrant(t, dm.ID, CapabilitySpaceRead, fixture.at(19))
	if _, err := fixture.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		SpaceID: dm.ID, Muted: true, Now: fixture.at(20),
	}); err != nil {
		t.Fatal(err)
	}
	dmMessage, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: dm.ID}, Body: "dm pierces mute", Now: fixture.at(21),
	})
	if err != nil {
		t.Fatal(err)
	}
	items = fixture.listItems(t, 10, fixture.at(22))
	if len(items) != 2 || items[1].Reason != InboxReasonDM || items[1].TriggerMessageID != dmMessage.ID {
		t.Fatalf("dm items = %+v", items)
	}
}

func TestInboxExactFreshnessHeldDraftAndCanonicalReplay(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	trigger, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "trigger",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	item := fixture.listItems(t, 1, fixture.at(2))[0]
	claimRequest := uuid.NewString()
	claimed := fixture.claim(t, item.ID, claimRequest, fixture.at(3))
	replayedClaim := fixture.claim(t, item.ID, claimRequest, fixture.at(4))
	if !reflect.DeepEqual(claimed, replayedClaim) {
		t.Fatalf("claim replay changed: %+v != %+v", claimed, replayedClaim)
	}
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: item.Target, Limit: 20, Now: fixture.at(5),
	})
	if err != nil || observed.Head != trigger.TargetSequence {
		t.Fatalf("observe = %+v, %v", observed, err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: item.Target,
		Body: "advanced exact target", Now: fixture.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	sendRequest := uuid.NewString()
	held, err := fixture.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: sendRequest, Authentication: fixture.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "agent result", MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(7),
	})
	if err != nil || held.Kind != InboxResultHeldDraft || held.HeldDraft == nil {
		t.Fatalf("held reply = %+v, %v", held, err)
	}
	replayedHeld, err := fixture.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: sendRequest, Authentication: fixture.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "agent result", MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(8),
	})
	if err != nil || !reflect.DeepEqual(held, replayedHeld) {
		t.Fatalf("held replay = %+v, %v", replayedHeld, err)
	}
	var sentWithHeldRequest int
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM messages WHERE request_id = ?`, sendRequest).Scan(&sentWithHeldRequest); err != nil || sentWithHeldRequest != 0 {
		t.Fatalf("held request messages = %d, %v", sentWithHeldRequest, err)
	}
	observed, err = fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: item.Target, Limit: 20, Now: fixture.at(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveRequest := uuid.NewString()
	resolved, err := fixture.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: resolveRequest, Authentication: fixture.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: fixture.at(10),
	})
	if err != nil || resolved.Kind != InboxResultMessage || resolved.Message == nil || resolved.InboxItem.Completion != InboxCompletionSent {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	replayedResolved, err := fixture.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: resolveRequest, Authentication: fixture.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: fixture.at(11),
	})
	if err != nil || !reflect.DeepEqual(resolved, replayedResolved) {
		t.Fatalf("resolve replay = %+v, %v", replayedResolved, err)
	}
	replayedHeldAfterDone, err := fixture.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: sendRequest, Authentication: fixture.authentication, InboxItemID: item.ID,
		BasisTargetSequence: trigger.TargetSequence, Body: "agent result", MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(12),
	})
	if err != nil || !reflect.DeepEqual(held, replayedHeldAfterDone) {
		t.Fatalf("held replay after item done = %+v, %v", replayedHeldAfterDone, err)
	}
	if _, err := fixture.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: sendRequest, Authentication: fixture.authentication, InboxItemID: item.ID, Now: fixture.at(13),
	}); !errors.Is(err, ErrInboxRequestConflict) {
		t.Fatalf("cross-operation request reuse error = %v", err)
	}
	items := fixture.listItems(t, 10, fixture.at(14))
	if len(items) != 0 {
		t.Fatalf("completed item remained pending: %+v", items)
	}
}

func TestInboxRequestReceiptsExcludeBusinessContentAndCredentials(t *testing.T) {
	fixture := openInboxFixture(t)
	mentionedAgentID := fixture.createMentionMember(t, "receipt-mentioned", fixture.at(1))
	freshBody := "fresh-receipt-body-6b71c7"
	heldBody := "held-receipt-body-e2a194"

	if _, _, err := fixture.sendInboxReply(t, freshBody, []string{mentionedAgentID}, false, 2); err != nil {
		t.Fatal(err)
	}
	_, heldResult, err := fixture.sendInboxReply(t, heldBody, []string{mentionedAgentID}, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ResolveHeldDraft(context.Background(), ResolveHeldDraftParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		HeldDraftID: heldResult.HeldDraft.ID, Action: DraftResolutionCancel, Now: fixture.at(20),
	}); err != nil {
		t.Fatal(err)
	}

	var messageBodies, draftBodies, messageMentions, draftMentions int
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM messages WHERE body = ?`, freshBody).Scan(&messageBodies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM agent_held_drafts WHERE body = ?`, heldBody).Scan(&draftBodies); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM message_mentions WHERE agent_id = ?`, mentionedAgentID).Scan(&messageMentions); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM agent_held_draft_mentions WHERE agent_id = ?`, mentionedAgentID).Scan(&draftMentions); err != nil {
		t.Fatal(err)
	}
	if messageBodies != 1 || draftBodies != 1 || messageMentions != 1 || draftMentions != 1 {
		t.Fatalf("business facts = message bodies %d, draft bodies %d, message mentions %d, draft mentions %d", messageBodies, draftBodies, messageMentions, draftMentions)
	}

	rows, err := fixture.database.db.Query(`SELECT response_snapshot FROM agent_requests`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	privateValues := []string{
		freshBody,
		heldBody,
		mentionedAgentID,
		runtimeTestToken(42),
		runtimeTestToken(250),
		"computer-registration-key",
	}
	count := 0
	for rows.Next() {
		var snapshot string
		if err := rows.Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		count++
		for _, privateValue := range privateValues {
			if strings.Contains(snapshot, privateValue) {
				t.Fatalf("request receipt contains private value %q: %s", privateValue, snapshot)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("agent request receipts = %d, want 5", count)
	}
	var agentAuditCount, messageSendAuditCount int
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE actor_kind = 'agent' AND actor_id = ?`, fixture.agentID).Scan(&agentAuditCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE actor_kind = 'agent' AND actor_id = ? AND action = ?`, fixture.agentID, AuditMessageSend).Scan(&messageSendAuditCount); err != nil {
		t.Fatal(err)
	}
	if agentAuditCount != 1 || messageSendAuditCount != 1 {
		t.Fatalf("agent audits = %d, message.send = %d; held/cancel must not fake publish", agentAuditCount, messageSendAuditCount)
	}
}

func TestInboxRequestReplayFailsClosedOnBrokenBusinessFacts(t *testing.T) {
	t.Run("message missing", func(t *testing.T) {
		fixture := openInboxFixture(t)
		params, result, err := fixture.sendInboxReply(t, "missing-message-body", nil, false, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.db.Exec(`DELETE FROM messages WHERE id = ?`, result.Message.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = fixture.at(20)
		if _, err := fixture.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("missing message replay error = %v", err)
		}
	})

	t.Run("message mentions missing", func(t *testing.T) {
		fixture := openInboxFixture(t)
		mentionedAgentID := fixture.createMentionMember(t, "missing-mention", fixture.at(1))
		params, result, err := fixture.sendInboxReply(t, "missing-mention-body", []string{mentionedAgentID}, false, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.db.Exec(`DELETE FROM message_mentions WHERE message_id = ?`, result.Message.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = fixture.at(20)
		if _, err := fixture.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("missing message mentions replay error = %v", err)
		}
	})

	t.Run("message owner mismatch", func(t *testing.T) {
		fixture := openInboxFixture(t)
		otherAgentID := fixture.createMentionMember(t, "wrong-owner", fixture.at(1))
		params, result, err := fixture.sendInboxReply(t, "wrong-owner-body", nil, false, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.db.Exec(`UPDATE messages SET author_id = ? WHERE id = ?`, otherAgentID, result.Message.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = fixture.at(20)
		if _, err := fixture.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("message owner mismatch replay error = %v", err)
		}
	})

	t.Run("held draft missing", func(t *testing.T) {
		fixture := openInboxFixture(t)
		params, result, err := fixture.sendInboxReply(t, "missing-draft-body", nil, true, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.database.db.Exec(`DELETE FROM agent_held_drafts WHERE id = ?`, result.HeldDraft.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = fixture.at(20)
		if _, err := fixture.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("missing held draft replay error = %v", err)
		}
	})
}

func TestHeldDraftRetryStaleCreatesCanonicalSuccessor(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	item, original := fixture.makeHeldDraft(t, fixture.group, 1)
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: item.Target, Limit: 20, Now: fixture.at(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: item.Target,
		Body: "advance again", Now: fixture.at(8),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	params := ResolveHeldDraftParams{
		RequestID: requestID, Authentication: fixture.authentication,
		HeldDraftID: original.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: fixture.at(9),
	}
	result, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || result.Kind != InboxResultHeldDraft || result.HeldDraft == nil || result.InboxItem.State != InboxStateClaimed {
		t.Fatalf("stale retry = %+v, %v", result, err)
	}
	if result.HeldDraft.PredecessorDraftID != original.ID || result.HeldDraft.Sequence <= original.Sequence {
		t.Fatalf("successor = %+v, original = %+v", result.HeldDraft, original)
	}
	resolvedOriginal := readHeldDraft(t, fixture.database, original.ID)
	if resolvedOriginal.State != HeldDraftStateSuperseded || resolvedOriginal.ResolutionAction != DraftResolutionRetry || resolvedOriginal.ResultKind != InboxResultHeldDraft || resolvedOriginal.ResultID != result.HeldDraft.ID {
		t.Fatalf("resolved original = %+v", resolvedOriginal)
	}
	params.Now = fixture.at(10)
	replayed, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("stale retry replay = %+v, %v", replayed, err)
	}
	if _, err := fixture.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		HeldDraftID: result.HeldDraft.ID, Action: DraftResolutionCancel, Now: fixture.at(11),
	}); err != nil {
		t.Fatal(err)
	}
	params.Now = fixture.at(12)
	replayedAfterSuccessorTerminal, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayedAfterSuccessorTerminal) {
		t.Fatalf("stale retry replay after successor terminal = %+v, %v", replayedAfterSuccessorTerminal, err)
	}
	secondRequest := uuid.NewString()
	params.RequestID = secondRequest
	params.Now = fixture.at(13)
	if _, err := fixture.database.ResolveHeldDraft(ctx, params); !errors.Is(err, ErrHeldDraftNotHeld) {
		t.Fatalf("second stale retry error = %v", err)
	}
	assertAgentRequestCount(t, fixture.database, secondRequest, 0)
}

func TestHeldDraftRetargetFreshPublishesCanonicalMessage(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	item, original := fixture.makeHeldDraft(t, fixture.group, 1)
	targetGroup := fixture.createAgentGroup(t, "Retarget fresh", 7)
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication,
		Target:         MessageTarget{Kind: MessageTargetSpace, ID: targetGroup.ID}, Limit: 20, Now: fixture.at(10),
	})
	if err != nil || observed.Head != 0 {
		t.Fatalf("retarget observe = %+v, %v", observed, err)
	}
	requestID := uuid.NewString()
	params := ResolveHeldDraftParams{
		RequestID: requestID, Authentication: fixture.authentication,
		HeldDraftID: original.ID, Action: DraftResolutionRetarget,
		Target:              MessageTarget{Kind: MessageTargetSpace, ID: targetGroup.ID},
		BasisTargetSequence: observed.Head, Now: fixture.at(11),
	}
	result, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || result.Kind != InboxResultMessage || result.Message == nil || result.Message.SpaceID != targetGroup.ID || result.InboxItem.State != InboxStateDone {
		t.Fatalf("fresh retarget = %+v, %v", result, err)
	}
	resolvedOriginal := readHeldDraft(t, fixture.database, original.ID)
	if resolvedOriginal.State != HeldDraftStateRetargeted || resolvedOriginal.ResolutionAction != DraftResolutionRetarget || resolvedOriginal.ResultKind != InboxResultMessage || resolvedOriginal.ResultID != result.Message.ID {
		t.Fatalf("resolved original = %+v", resolvedOriginal)
	}
	params.Now = fixture.at(12)
	replayed, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("fresh retarget replay = %+v, %v", replayed, err)
	}
	secondRequest := uuid.NewString()
	params.RequestID = secondRequest
	params.Now = fixture.at(13)
	if _, err := fixture.database.ResolveHeldDraft(ctx, params); !errors.Is(err, ErrHeldDraftNotHeld) {
		t.Fatalf("second fresh retarget error = %v", err)
	}
	assertAgentRequestCount(t, fixture.database, secondRequest, 0)
	if current := readInboxItem(t, fixture.database, item.ID); current.Completion != InboxCompletionSent {
		t.Fatalf("retargeted item = %+v", current)
	}
}

func TestHeldDraftRetargetStaleCreatesCanonicalSuccessor(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	item, original := fixture.makeHeldDraft(t, fixture.group, 1)
	targetGroup := fixture.createAgentGroup(t, "Retarget stale", 7)
	target := MessageTarget{Kind: MessageTargetSpace, ID: targetGroup.ID}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: target, Body: "target root", Now: fixture.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: target, Limit: 20, Now: fixture.at(11),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: target, Body: "target advanced", Now: fixture.at(12),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	params := ResolveHeldDraftParams{
		RequestID: requestID, Authentication: fixture.authentication,
		HeldDraftID: original.ID, Action: DraftResolutionRetarget, Target: target,
		BasisTargetSequence: observed.Head, Now: fixture.at(13),
	}
	result, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || result.Kind != InboxResultHeldDraft || result.HeldDraft == nil || result.HeldDraft.Target != target || result.InboxItem.State != InboxStateClaimed {
		t.Fatalf("stale retarget = %+v, %v", result, err)
	}
	resolvedOriginal := readHeldDraft(t, fixture.database, original.ID)
	if resolvedOriginal.State != HeldDraftStateRetargeted || resolvedOriginal.ResolutionAction != DraftResolutionRetarget || resolvedOriginal.ResultKind != InboxResultHeldDraft || resolvedOriginal.ResultID != result.HeldDraft.ID {
		t.Fatalf("resolved original = %+v", resolvedOriginal)
	}
	params.Now = fixture.at(14)
	replayed, err := fixture.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("stale retarget replay = %+v, %v", replayed, err)
	}
	secondRequest := uuid.NewString()
	params.RequestID = secondRequest
	params.Now = fixture.at(15)
	if _, err := fixture.database.ResolveHeldDraft(ctx, params); !errors.Is(err, ErrHeldDraftNotHeld) {
		t.Fatalf("second stale retarget error = %v", err)
	}
	assertAgentRequestCount(t, fixture.database, secondRequest, 0)
	if current := readInboxItem(t, fixture.database, item.ID); current.State != InboxStateClaimed {
		t.Fatalf("stale retarget item = %+v", current)
	}
}

func TestHeldDraftListCursorLimitAndAccessLossFill(t *testing.T) {
	fixture := openInboxFixture(t)
	_, inaccessible := fixture.makeHeldDraft(t, fixture.group, 1)
	secondGroup := fixture.createAgentGroup(t, "Held list", 7)
	_, visible := fixture.makeHeldDraft(t, secondGroup, 10)
	if _, err := fixture.database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: fixture.readGrant.ID, Now: fixture.at(16),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.database.ListHeldDrafts(context.Background(), ListHeldDraftsParams{
		Authentication: fixture.authentication, Limit: 1, Now: fixture.at(17),
	})
	if err != nil || len(result.Drafts) != 1 || result.Drafts[0].ID != visible.ID || result.NextSequence != visible.Sequence || result.NextSequence <= inaccessible.Sequence {
		t.Fatalf("bounded held list = %+v, %v", result, err)
	}
	next, err := fixture.database.ListHeldDrafts(context.Background(), ListHeldDraftsParams{
		Authentication: fixture.authentication, AfterSequence: result.NextSequence, Limit: 1, Now: fixture.at(18),
	})
	if err != nil || len(next.Drafts) != 0 || next.NextSequence != result.NextSequence {
		t.Fatalf("held list next page = %+v, %v", next, err)
	}
	for _, limit := range []uint32{0, maxInboxListLimit + 1} {
		if _, err := fixture.database.ListHeldDrafts(context.Background(), ListHeldDraftsParams{
			Authentication: fixture.authentication, Limit: limit, Now: fixture.at(19),
		}); !errors.Is(err, ErrInvalidInboxLimit) {
			t.Fatalf("limit %d error = %v", limit, err)
		}
	}
}

func TestInboxAccessLossClosesItemsWithoutResurrection(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	for index := 0; index < 2; index++ {
		if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
			RequestID: uuid.NewString(), Actor: fixture.owner,
			Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "pending",
			MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(index + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	secondGroup, err := fixture.database.CreateGroup(ctx, CreateGroupParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Name: "Still readable", Now: fixture.at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.AddMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, SpaceID: secondGroup.ID,
		Member: Principal{Kind: "agent", ID: fixture.agentID}, Now: fixture.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	fixture.issueAgentGrant(t, secondGroup.ID, CapabilitySpaceRead, fixture.at(5))
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: secondGroup.ID}, Body: "still visible",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.RevokeGrant(ctx, RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: fixture.readGrant.ID, Now: fixture.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	items, err := fixture.database.ListInboxItems(ctx, ListInboxItemsParams{
		Authentication: fixture.authentication, Limit: 1, Now: fixture.at(8),
	})
	if err != nil || len(items) != 1 || items[0].SpaceID != secondGroup.ID {
		t.Fatalf("items after access loss = %+v, %v", items, err)
	}
	fixture.claim(t, items[0].ID, uuid.NewString(), fixture.at(9))
	if _, err := fixture.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		InboxItemID: items[0].ID, Now: fixture.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	notice, err := fixture.database.GetInboxNotice(ctx, InboxNoticeParams{Authentication: fixture.authentication, Now: fixture.at(11)})
	if err != nil || notice {
		t.Fatalf("notice after access loss = %v, %v", notice, err)
	}
	var closed int
	if err := fixture.database.db.QueryRow(`
		SELECT count(*) FROM agent_inbox_items
		WHERE agent_id = ? AND state = 'done' AND completion = 'access_lost'
	`, fixture.agentID).Scan(&closed); err != nil || closed != 2 {
		t.Fatalf("access-lost items = %d, %v", closed, err)
	}
	fixture.readGrant = fixture.issueAgentGrant(t, fixture.group.ID, CapabilitySpaceRead, fixture.at(12))
	items = fixture.listItems(t, 10, fixture.at(13))
	if len(items) != 0 {
		t.Fatalf("access-lost items resurrected: %+v", items)
	}
}

func TestInboxSiblingAdvanceDoesNotHoldReply(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	root, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "root",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	item := fixture.listItems(t, 1, fixture.at(2))[0]
	fixture.claim(t, item.ID, uuid.NewString(), fixture.at(3))
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: item.Target, Limit: 20, Now: fixture.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "sibling thread", Now: fixture.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := fixture.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "root reply", Now: fixture.at(6),
	})
	if err != nil || result.Kind != InboxResultMessage || result.Message == nil {
		t.Fatalf("sibling advance reply = %+v, %v", result, err)
	}
}

func TestInboxArchiveAndRuntimeFailuresWriteNoDraftOrReceipt(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "trigger",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	}); err != nil {
		t.Fatal(err)
	}
	item := fixture.listItems(t, 1, fixture.at(2))[0]
	fixture.claim(t, item.ID, uuid.NewString(), fixture.at(3))
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: item.Target, Limit: 20, Now: fixture.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ArchiveSpace(ctx, ChangeSpaceArchiveParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, SpaceID: fixture.group.ID, Now: fixture.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	if _, err := fixture.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: requestID, Authentication: fixture.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "must fail", Now: fixture.at(6),
	}); !errors.Is(err, ErrSpaceArchived) {
		t.Fatalf("archived send error = %v", err)
	}
	assertInboxRequestWrites(t, fixture.database, requestID, 0, 0)

	if err := fixture.database.RevokeAgentRuntimeSession(ctx, RevokeAgentRuntimeSessionParams{
		Proof: fixture.authentication.Proof, ComputerID: fixture.authentication.Proof.computerID,
		RegistrationKey: "computer-registration-key", Now: fixture.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	runtimeRequest := uuid.NewString()
	if _, err := fixture.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: runtimeRequest, Authentication: fixture.authentication, InboxItemID: item.ID, Now: fixture.at(8),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("revoked runtime completion error = %v", err)
	}
	assertInboxRequestWrites(t, fixture.database, runtimeRequest, 0, 0)
}

func TestHeldDraftDoesNotResurrectAfterAccessLoss(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "trigger",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	}); err != nil {
		t.Fatal(err)
	}
	item := fixture.listItems(t, 1, fixture.at(2))[0]
	fixture.claim(t, item.ID, uuid.NewString(), fixture.at(3))
	observed, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: item.Target, Limit: 20, Now: fixture.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: item.Target, Body: "advance", Now: fixture.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	held, err := fixture.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "held", Now: fixture.at(6),
	})
	if err != nil || held.HeldDraft == nil {
		t.Fatalf("held = %+v, %v", held, err)
	}
	if _, err := fixture.database.RevokeGrant(ctx, RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: fixture.readGrant.ID, Now: fixture.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	drafts, err := fixture.database.ListHeldDrafts(ctx, ListHeldDraftsParams{
		Authentication: fixture.authentication, Limit: 10, Now: fixture.at(8),
	})
	if err != nil || len(drafts.Drafts) != 0 || drafts.NextSequence != held.HeldDraft.Sequence {
		t.Fatalf("drafts after access loss = %+v, %v", drafts, err)
	}
	fixture.readGrant = fixture.issueAgentGrant(t, fixture.group.ID, CapabilitySpaceRead, fixture.at(9))
	drafts, err = fixture.database.ListHeldDrafts(ctx, ListHeldDraftsParams{
		Authentication: fixture.authentication, Limit: 10, Now: fixture.at(10),
	})
	if err != nil || len(drafts.Drafts) != 0 || drafts.NextSequence != held.HeldDraft.Sequence {
		t.Fatalf("drafts after restored access = %+v, %v", drafts, err)
	}
	resolveRequest := uuid.NewString()
	if _, err := fixture.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: resolveRequest, Authentication: fixture.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionCancel, Now: fixture.at(11),
	}); !errors.Is(err, ErrInboxItemNotClaimed) {
		t.Fatalf("access-lost draft resolution error = %v", err)
	}
	assertInboxRequestWrites(t, fixture.database, resolveRequest, 0, 1)
}

func TestInboxMentionValidationRejectsInvalidSets(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	nonmember := uuid.NewString()
	if _, err := fixture.database.db.Exec(`
		INSERT INTO agents(id, name, description, driver, created_at, updated_at)
		VALUES(?, 'outside', '', 'native', ?, ?)
	`, nonmember, unixNano(fixture.at(1)), unixNano(fixture.at(1))); err != nil {
		t.Fatal(err)
	}
	for name, mentions := range map[string][]string{
		"duplicate": {fixture.agentID, fixture.agentID},
		"nonmember": {nonmember},
		"invalid":   {"not-a-uuid"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
				RequestID: uuid.NewString(), Actor: fixture.owner,
				Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID},
				Body:   "invalid mention", MentionedAgentIDs: mentions, Now: fixture.at(2),
			}); !errors.Is(err, ErrInvalidMention) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	tooMany := make([]string, maxMentionCount+1)
	for index := range tooMany {
		tooMany[index] = uuid.NewString()
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID},
		Body:   "too many", MentionedAgentIDs: tooMany, Now: fixture.at(3),
	}); !errors.Is(err, ErrInvalidMention) {
		t.Fatalf("too-many error = %v", err)
	}
	var messages int
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM messages WHERE space_id = ?`, fixture.group.ID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("invalid mention messages = %d, %v", messages, err)
	}
}

func TestInboxSchemaRejectsBrokenMentionAndTriggerFacts(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	message, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "valid",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondAgent := uuid.NewString()
	if _, err := fixture.database.db.Exec(`
		INSERT INTO agents(id, name, description, driver, created_at, updated_at)
		VALUES(?, 'second', '', 'native', ?, ?)
	`, secondAgent, unixNano(fixture.at(2)), unixNano(fixture.at(2))); err != nil {
		t.Fatal(err)
	}
	assertExecFails(t, fixture.database, `INSERT INTO message_mentions(message_id, agent_id, ordinal) VALUES(?, ?, 0)`, message.ID, secondAgent)
	assertExecFails(t, fixture.database, `INSERT INTO message_mentions(message_id, agent_id, ordinal) VALUES(?, ?, 64)`, message.ID, secondAgent)
	assertExecFails(t, fixture.database, `INSERT INTO message_mentions(message_id, agent_id, ordinal) VALUES(?, ?, 1)`, uuid.NewString(), secondAgent)
	assertExecFails(t, fixture.database, `
		UPDATE agent_inbox_items SET trigger_target_sequence = trigger_target_sequence + 1
		WHERE agent_id = ? AND trigger_message_id = ?
	`, fixture.agentID, message.ID)
	assertExecFails(t, fixture.database, `
		UPDATE agent_inbox_items SET state = 'done', completion = '', done_at = ?
		WHERE agent_id = ? AND trigger_message_id = ?
	`, unixNano(fixture.at(3)), fixture.agentID, message.ID)
	item := fixture.listItems(t, 1, fixture.at(4))[0]
	draftID := uuid.NewString()
	if _, err := fixture.database.db.Exec(`
		INSERT INTO agent_held_drafts(
			id, agent_id, inbox_item_id, space_id, target_kind, target_id,
			basis_target_sequence, body, held_reason, state, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, 'held', 'target_advanced', 'held', ?, ?)
	`, draftID, fixture.agentID, item.ID, item.SpaceID, item.Target.Kind, item.Target.ID,
		item.TriggerTargetSequence, unixNano(fixture.at(5)), unixNano(fixture.at(5))); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][]string{
		"sent held draft":     {HeldDraftStateSent, DraftResolutionRetry, InboxResultHeldDraft},
		"superseded retarget": {HeldDraftStateSuperseded, DraftResolutionRetarget, InboxResultHeldDraft},
		"retargeted retry":    {HeldDraftStateRetargeted, DraftResolutionRetry, InboxResultMessage},
	} {
		t.Run(name, func(t *testing.T) {
			assertExecFails(t, fixture.database, `
				UPDATE agent_held_drafts
				SET state = ?, resolution_action = ?, result_kind = ?, result_id = ?
				WHERE id = ?
			`, values[0], values[1], values[2], uuid.NewString(), draftID)
		})
	}
	var planID, parentID, unused int
	var detail string
	if err := fixture.database.db.QueryRow(`
		EXPLAIN QUERY PLAN
		SELECT sequence, id FROM agent_held_drafts
		WHERE agent_id = ? AND state = 'held' AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, fixture.agentID, 0, 10).Scan(&planID, &parentID, &unused, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "agent_held_drafts_agent_state_sequence") || !strings.Contains(detail, "sequence>?") {
		t.Fatalf("held draft list query plan = %q", detail)
	}
	rows, err := fixture.database.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check found a violation")
	}
}

func TestInboxMembershipRemovalTerminalizesProjection(t *testing.T) {
	fixture := openInboxFixture(t)
	ctx := context.Background()
	root, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "root",
		MentionedAgentIDs: []string{fixture.agentID}, Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		SpaceID: fixture.group.ID, Muted: true, Now: fixture.at(2),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "create thread", Now: fixture.at(3),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SetThreadFollow(ctx, SetThreadFollowParams{
		RequestID: uuid.NewString(), Authentication: fixture.authentication,
		ThreadID: root.ID, Followed: true, Now: fixture.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: fixture.authentication, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID},
		Limit: 10, Now: fixture.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.RemoveMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, SpaceID: fixture.group.ID,
		Member: Principal{Kind: "agent", ID: fixture.agentID}, Now: fixture.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agent_space_mutes", "agent_thread_follows", "agent_target_cursors"} {
		var count int
		if err := fixture.database.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE agent_id = ?`, fixture.agentID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows = %d, %v", table, count, err)
		}
	}
	var completion string
	if err := fixture.database.db.QueryRow(`SELECT completion FROM agent_inbox_items WHERE trigger_message_id = ? AND agent_id = ?`, root.ID, fixture.agentID).Scan(&completion); err != nil || completion != InboxCompletionAccessLost {
		t.Fatalf("removed item completion = %q, %v", completion, err)
	}
}

type inboxFixture struct {
	database       *Store
	owner          Principal
	rootGrant      Grant
	readGrant      Grant
	agentID        string
	authentication AgentRuntimeAuthentication
	group          Space
	now            time.Time
}

func openInboxFixture(t *testing.T) *inboxFixture {
	t.Helper()
	runtimeFixture := openAgentRuntimeFixtureAt(t, filepath.Join(t.TempDir(), "server.db"))
	t.Cleanup(func() {
		if err := runtimeFixture.database.Close(); err != nil {
			t.Error(err)
		}
	})
	bootstrap, err := runtimeFixture.database.EnsureAuthority(context.Background(), runtimeTestToken(250), runtimeFixture.now)
	if err != nil {
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	group, err := runtimeFixture.database.CreateGroup(context.Background(), CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Inbox", Now: runtimeFixture.now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeFixture.database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID,
		Member: Principal{Kind: "agent", ID: runtimeFixture.agentID}, Now: runtimeFixture.now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	fixture := &inboxFixture{
		database: runtimeFixture.database, owner: owner, rootGrant: bootstrap.RootGrant,
		agentID: runtimeFixture.agentID, group: group, now: runtimeFixture.now.Add(10 * time.Second),
	}
	fixture.readGrant = fixture.issueAgentGrant(t, group.ID, CapabilitySpaceRead, runtimeFixture.now.Add(3*time.Second))
	fixture.issueAgentGrant(t, group.ID, CapabilityMessageSend, runtimeFixture.now.Add(4*time.Second))
	token := runtimeTestToken(42)
	createRuntimeSession(t, runtimeFixture, token, runtimeFixture.now.Add(5*time.Second))
	authentication, err := fixture.database.AuthenticateAgentRuntimeSession(context.Background(), token, runtimeFixture.now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fixture.authentication = authentication
	return fixture
}

func (f *inboxFixture) at(seconds int) time.Time {
	return f.now.Add(time.Duration(seconds) * time.Second)
}

func (f *inboxFixture) issueAgentGrant(t *testing.T, spaceID string, capability Capability, now time.Time) Grant {
	t.Helper()
	grant, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Subject:    Principal{Kind: "agent", ID: f.agentID, OrganizationID: f.owner.OrganizationID},
		Capability: capability, Scope: Scope{Kind: "space", ID: spaceID}, ParentGrantID: f.rootGrant.ID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *inboxFixture) createAgentGroup(t *testing.T, name string, start int) Space {
	t.Helper()
	group, err := f.database.CreateGroup(context.Background(), CreateGroupParams{
		RequestID: uuid.NewString(), Actor: f.owner, Name: name, Now: f.at(start),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: group.ID,
		Member: Principal{Kind: "agent", ID: f.agentID}, Now: f.at(start + 1),
	}); err != nil {
		t.Fatal(err)
	}
	f.issueAgentGrant(t, group.ID, CapabilitySpaceRead, f.at(start+2))
	f.issueAgentGrant(t, group.ID, CapabilityMessageSend, f.at(start+3))
	return group
}

func (f *inboxFixture) makeHeldDraft(t *testing.T, space Space, start int) (InboxItem, HeldDraft) {
	t.Helper()
	ctx := context.Background()
	trigger, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "trigger held",
		MentionedAgentIDs: []string{f.agentID}, Now: f.at(start),
	})
	if err != nil {
		t.Fatal(err)
	}
	items := f.listItems(t, maxInboxListLimit, f.at(start+1))
	var item InboxItem
	for _, candidate := range items {
		if candidate.TriggerMessageID == trigger.ID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		t.Fatalf("inbox item for trigger %s not found", trigger.ID)
	}
	f.claim(t, item.ID, uuid.NewString(), f.at(start+2))
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: f.authentication, Target: item.Target, Limit: 20, Now: f.at(start + 3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner, Target: item.Target,
		Body: "advance held", Now: f.at(start + 4),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: uuid.NewString(), Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "held body", Now: f.at(start + 5),
	})
	if err != nil || result.HeldDraft == nil {
		t.Fatalf("create held draft = %+v, %v", result, err)
	}
	return item, *result.HeldDraft
}

func (f *inboxFixture) createMentionMember(t *testing.T, name string, now time.Time) string {
	t.Helper()
	agent, err := f.database.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: uuid.NewString(), Actor: f.owner, Name: name, Driver: "native", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID,
		Member: Principal{Kind: "agent", ID: agent.ID}, Now: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	return agent.ID
}

func (f *inboxFixture) sendInboxReply(t *testing.T, body string, mentions []string, held bool, start int) (SendInboxReplyParams, SendInboxReplyResult, error) {
	t.Helper()
	ctx := context.Background()
	trigger, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "receipt trigger",
		MentionedAgentIDs: []string{f.agentID}, Now: f.at(start),
	})
	if err != nil {
		return SendInboxReplyParams{}, SendInboxReplyResult{}, err
	}
	var item InboxItem
	for _, candidate := range f.listItems(t, maxInboxListLimit, f.at(start+1)) {
		if candidate.TriggerMessageID == trigger.ID {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		t.Fatalf("inbox item for trigger %s not found", trigger.ID)
	}
	f.claim(t, item.ID, uuid.NewString(), f.at(start+2))
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: f.authentication, Target: item.Target, Limit: 200, Now: f.at(start + 3),
	})
	if err != nil {
		return SendInboxReplyParams{}, SendInboxReplyResult{}, err
	}
	if held {
		if _, err := f.database.SendMessage(ctx, SendMessageParams{
			RequestID: uuid.NewString(), Actor: f.owner, Target: item.Target, Body: "receipt advance", Now: f.at(start + 4),
		}); err != nil {
			return SendInboxReplyParams{}, SendInboxReplyResult{}, err
		}
	}
	params := SendInboxReplyParams{
		RequestID: uuid.NewString(), Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: body, MentionedAgentIDs: mentions, Now: f.at(start + 5),
	}
	result, err := f.database.SendInboxReply(ctx, params)
	return params, result, err
}

func (f *inboxFixture) listItems(t *testing.T, limit uint32, now time.Time) []InboxItem {
	t.Helper()
	items, err := f.database.ListInboxItems(context.Background(), ListInboxItemsParams{
		Authentication: f.authentication, Limit: limit, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func (f *inboxFixture) claim(t *testing.T, itemID, requestID string, now time.Time) InboxItem {
	t.Helper()
	item, err := f.database.ClaimInboxItem(context.Background(), ClaimInboxItemParams{
		RequestID: requestID, Authentication: f.authentication, InboxItemID: itemID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func assertInboxRequestWrites(t *testing.T, database *Store, requestID string, wantRequests, wantDrafts int) {
	t.Helper()
	var requests, drafts int
	if err := database.db.QueryRow(`SELECT count(*) FROM agent_requests WHERE request_id = ?`, requestID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM agent_held_drafts`).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if requests != wantRequests || drafts != wantDrafts {
		t.Fatalf("request writes = %d, drafts = %d; want %d, %d", requests, drafts, wantRequests, wantDrafts)
	}
}

func assertExecFails(t *testing.T, database *Store, statement string, args ...any) {
	t.Helper()
	if _, err := database.db.Exec(statement, args...); err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", statement)
	}
}

func assertAgentRequestCount(t *testing.T, database *Store, requestID string, want int) {
	t.Helper()
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM agent_requests WHERE request_id = ?`, requestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("agent request count = %d, want %d", count, want)
	}
}

func readHeldDraft(t *testing.T, database *Store, id string) HeldDraft {
	t.Helper()
	draft, err := scanHeldDraft(database.db.QueryRow(heldDraftSelect+` WHERE id = ?`, id))
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func readInboxItem(t *testing.T, database *Store, id string) InboxItem {
	t.Helper()
	item, err := scanInboxItem(database.db.QueryRow(inboxItemSelect+` WHERE id = ?`, id))
	if err != nil {
		t.Fatal(err)
	}
	return item
}
