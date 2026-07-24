package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	"github.com/google/uuid"
)

func TestInboxAttentionMentionFollowMuteAndDM(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()

	root, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "root",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	items := f.listItems(t, 10, f.at(2))
	if len(items) != 1 || items[0].Reason != InboxReasonMention || items[0].TriggerMessageID != root.ID {
		t.Fatalf("mention items = %+v", items)
	}
	claim := f.claim(t, items[0].ID, uuid.NewString(), f.at(3))
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		InboxItemID: claim.ID, Now: f.at(4),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "thread mention",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	items = f.listItems(t, 10, f.at(6))
	if len(items) != 1 || items[0].Reason != InboxReasonMention {
		t.Fatalf("thread mention items = %+v", items)
	}
	f.claim(t, items[0].ID, uuid.NewString(), f.at(7))
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		InboxItemID: items[0].ID, Now: f.at(8),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "followed", Now: f.at(9),
	}); err != nil {
		t.Fatal(err)
	}
	items = f.listItems(t, 10, f.at(10))
	if len(items) != 1 || items[0].Reason != InboxReasonThreadFollow {
		t.Fatalf("follow items = %+v", items)
	}
	f.claim(t, items[0].ID, uuid.NewString(), f.at(11))
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		InboxItemID: items[0].ID, Now: f.at(12),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		SpaceID: f.group.ID, Muted: true, Now: f.at(13),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "muted ordinary", Now: f.at(14),
	}); err != nil {
		t.Fatal(err)
	}
	if items := f.listItems(t, 10, f.at(15)); len(items) != 0 {
		t.Fatalf("muted ordinary items = %+v", items)
	}
	mentioned, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "muted mention",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(16),
	})
	if err != nil {
		t.Fatal(err)
	}
	items = f.listItems(t, 10, f.at(17))
	if len(items) != 1 || items[0].Reason != InboxReasonMention || !reflect.DeepEqual(mentioned.MentionedPrincipals, agentPrincipals(f.owner.OrganizationID, f.agentID)) {
		t.Fatalf("muted mention = %+v, items = %+v", mentioned, items)
	}

	dm, err := f.database.CreateDM(ctx, CreateDMParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Peer: Principal{Kind: "agent", ID: f.agentID, OrganizationID: f.owner.OrganizationID}, Now: f.at(18),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.issueAgentGrant(t, dm.ID, CapabilitySpaceRead, f.at(19))
	if _, err := f.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		SpaceID: dm.ID, Muted: true, Now: f.at(20),
	}); err != nil {
		t.Fatal(err)
	}
	dmMessage, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: dm.ID}, Body: "dm pierces mute", Now: f.at(21),
	})
	if err != nil {
		t.Fatal(err)
	}
	items = f.listItems(t, 10, f.at(22))
	if len(items) != 2 || items[1].Reason != InboxReasonDM || items[1].TriggerMessageID != dmMessage.ID {
		t.Fatalf("dm items = %+v", items)
	}
}

func TestHumanInboxParityMentionMuteFollowSelfSuppressionAccessLossAndDM(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	humanRecord, err := f.database.CreateHuman(ctx, CreateHumanParams{
		RequestID: uuid.NewString(), Actor: f.owner, Name: "Inbox Human", Role: "member",
		Credential: "inbox-human-credential-abcdefghijklmnopqrstuvwxyz", Now: f.at(40),
	})
	if err != nil {
		t.Fatal(err)
	}
	human := Principal{Kind: PrincipalHuman, ID: humanRecord.ID, OrganizationID: f.owner.OrganizationID}
	if _, err := f.database.AddMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID, Member: human, Now: f.at(41),
	}); err != nil {
		t.Fatal(err)
	}
	for index, capability := range []Capability{CapabilitySpaceRead, CapabilityMessageSend} {
		if _, err := f.database.IssueGrant(ctx, IssueGrantParams{
			RequestID: uuid.NewString(), Actor: f.owner, Subject: human, Capability: capability,
			Scope: Scope{Kind: "space", ID: f.group.ID}, ParentGrantID: f.rootGrant.ID, Now: f.at(42 + index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	authentication := executionapp.HumanInboxAuthentication(human)
	root, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "human mention",
		MentionedPrincipals: []Principal{human}, Now: f.at(44),
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.database.ListInboxItems(ctx, ListInboxItemsParams{Authentication: authentication, Limit: 20, Now: f.at(45)})
	if err != nil || len(items) != 1 || items[0].Recipient != human || items[0].Reason != InboxReasonMention {
		t.Fatalf("human mention items = %+v, %v", items, err)
	}
	var runs int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM runs WHERE inbox_item_id = ?`, items[0].ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("human inbox runs = %d, %v", runs, err)
	}
	claimed, err := f.database.ClaimInboxItem(ctx, ClaimInboxItemParams{
		RequestID: uuid.NewString(), Authentication: authentication, InboxItemID: items[0].ID, Now: f.at(46),
	})
	if err != nil || claimed.State != InboxStateClaimed {
		t.Fatalf("human claim = %+v, %v", claimed, err)
	}
	if _, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: authentication, Target: claimed.Target, Limit: 20, Now: f.at(47),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: authentication, InboxItemID: claimed.ID, Now: f.at(48),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "create thread", Now: f.at(49),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetThreadFollow(ctx, SetThreadFollowParams{
		RequestID: uuid.NewString(), Authentication: authentication, ThreadID: root.ID, Followed: true, Now: f.at(50),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: authentication, SpaceID: f.group.ID, Muted: true, Now: f.at(51),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "muted follow", Now: f.at(52),
	}); err != nil {
		t.Fatal(err)
	}
	items, err = f.database.ListInboxItems(ctx, ListInboxItemsParams{Authentication: authentication, Limit: 20, Now: f.at(53)})
	if err != nil || len(items) != 0 {
		t.Fatalf("muted human follow items = %+v, %v", items, err)
	}
	mentioned, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "muted explicit mention",
		MentionedPrincipals: []Principal{human}, Now: f.at(54),
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err = f.database.ListInboxItems(ctx, ListInboxItemsParams{Authentication: authentication, Limit: 20, Now: f.at(55)})
	if err != nil || len(items) != 1 || items[0].TriggerMessageID != mentioned.ID || items[0].Reason != InboxReasonMention {
		t.Fatalf("muted human mention items = %+v, %v", items, err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: human,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "self mention",
		MentionedPrincipals: []Principal{human}, Now: f.at(56),
	}); err != nil {
		t.Fatal(err)
	}
	items, err = f.database.ListInboxItems(ctx, ListInboxItemsParams{Authentication: authentication, Limit: 20, Now: f.at(57)})
	if err != nil || len(items) != 1 {
		t.Fatalf("human self notification = %+v, %v", items, err)
	}
	if _, err := f.database.RemoveMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID, Member: human, Now: f.at(58),
	}); err != nil {
		t.Fatal(err)
	}
	var completion string
	if err := f.database.db.QueryRow(`
		SELECT completion FROM inbox_items
		WHERE recipient_kind = 'human' AND recipient_id = ? AND trigger_message_id = ?
	`, human.ID, mentioned.ID).Scan(&completion); err != nil || completion != InboxCompletionAccessLost {
		t.Fatalf("human access-loss completion = %q, %v", completion, err)
	}
	for _, table := range []string{"principal_space_mutes", "principal_thread_follows", "principal_target_cursors"} {
		var count int
		if err := f.database.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE principal_kind = 'human' AND principal_id = ?`, human.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("human %s rows = %d, %v", table, count, err)
		}
	}
	if _, err := f.database.AddMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID, Member: human, Now: f.at(59),
	}); err != nil {
		t.Fatal(err)
	}
	items, err = f.database.ListInboxItems(ctx, ListInboxItemsParams{Authentication: authentication, Limit: 20, Now: f.at(60)})
	if err != nil || len(items) != 0 {
		t.Fatalf("restored human items resurrected = %+v, %v", items, err)
	}
	dm, err := f.database.CreateDM(ctx, CreateDMParams{
		RequestID: uuid.NewString(), Actor: f.owner, Peer: human, Now: f.at(61),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.IssueGrant(ctx, IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, Subject: human, Capability: CapabilitySpaceRead,
		Scope: Scope{Kind: "space", ID: dm.ID}, ParentGrantID: f.rootGrant.ID, Now: f.at(62),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: authentication, SpaceID: dm.ID, Muted: true, Now: f.at(63),
	}); err != nil {
		t.Fatal(err)
	}
	dmMessage, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: dm.ID}, Body: "muted dm", Now: f.at(64),
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err = f.database.ListInboxItems(ctx, ListInboxItemsParams{Authentication: authentication, Limit: 20, Now: f.at(65)})
	if err != nil || len(items) != 1 || items[0].Reason != InboxReasonDM || items[0].TriggerMessageID != dmMessage.ID {
		t.Fatalf("muted human dm items = %+v, %v", items, err)
	}
}

func TestInboxExactFreshnessHeldDraftAndCanonicalReplay(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	trigger, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "trigger",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	item := f.listItems(t, 1, f.at(2))[0]
	claimRequest := uuid.NewString()
	claimed := f.claim(t, item.ID, claimRequest, f.at(3))
	replayedClaim := f.claim(t, item.ID, claimRequest, f.at(4))
	if !reflect.DeepEqual(claimed, replayedClaim) {
		t.Fatalf("claim replay changed: %+v != %+v", claimed, replayedClaim)
	}
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(5),
	})
	if err != nil || observed.Head != trigger.TargetSequence {
		t.Fatalf("observe = %+v, %v", observed, err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner, Target: item.Target,
		Body: "advanced exact target", Now: f.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	sendRequest := uuid.NewString()
	held, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: sendRequest, Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "agent result", MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(7),
	})
	if err != nil || held.Kind != InboxResultHeldDraft || held.HeldDraft == nil {
		t.Fatalf("held reply = %+v, %v", held, err)
	}
	replayedHeld, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: sendRequest, Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "agent result", MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(8),
	})
	if err != nil || !reflect.DeepEqual(held, replayedHeld) {
		t.Fatalf("held replay = %+v, %v", replayedHeld, err)
	}
	var sentWithHeldRequest int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM messages WHERE request_id = ?`, sendRequest).Scan(&sentWithHeldRequest); err != nil || sentWithHeldRequest != 0 {
		t.Fatalf("held request messages = %d, %v", sentWithHeldRequest, err)
	}
	observed, err = f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveRequest := uuid.NewString()
	resolved, err := f.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: resolveRequest, Authentication: f.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: f.at(10),
	})
	if err != nil || resolved.Kind != InboxResultMessage || resolved.Message == nil || resolved.InboxItem.Completion != InboxCompletionSent {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	replayedResolved, err := f.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: resolveRequest, Authentication: f.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: f.at(11),
	})
	if err != nil || !reflect.DeepEqual(resolved, replayedResolved) {
		t.Fatalf("resolve replay = %+v, %v", replayedResolved, err)
	}
	replayedHeldAfterDone, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: sendRequest, Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: trigger.TargetSequence, Body: "agent result", MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(12),
	})
	if err != nil || !reflect.DeepEqual(held, replayedHeldAfterDone) {
		t.Fatalf("held replay after item done = %+v, %v", replayedHeldAfterDone, err)
	}
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: sendRequest, Authentication: executionapp.AgentInboxAuthentication(f.authentication), InboxItemID: item.ID, Now: f.at(13),
	}); !errors.Is(err, ErrInboxRequestConflict) {
		t.Fatalf("cross-operation request reuse error = %v", err)
	}
	items := f.listItems(t, 10, f.at(14))
	if len(items) != 0 {
		t.Fatalf("completed item remained pending: %+v", items)
	}
}

func TestInboxRequestReceiptsExcludeBusinessContentAndCredentials(t *testing.T) {
	f := openInboxFixture(t)
	mentionedAgentID := f.createMentionMember(t, "receipt-mentioned", f.at(1))
	freshBody := "fresh-receipt-body-6b71c7"
	heldBody := "held-receipt-body-e2a194"

	if _, _, err := f.sendInboxReply(t, freshBody, []string{mentionedAgentID}, false, 2); err != nil {
		t.Fatal(err)
	}
	_, heldResult, err := f.sendInboxReply(t, heldBody, []string{mentionedAgentID}, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ResolveHeldDraft(context.Background(), ResolveHeldDraftParams{
		RequestID: uuid.NewString(), Authentication: f.authentication,
		HeldDraftID: heldResult.HeldDraft.ID, Action: DraftResolutionCancel, Now: f.at(20),
	}); err != nil {
		t.Fatal(err)
	}

	var messageBodies, draftBodies, messageMentions, draftMentions int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM messages WHERE body = ?`, freshBody).Scan(&messageBodies); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM agent_held_drafts WHERE body = ?`, heldBody).Scan(&draftBodies); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM message_mentions WHERE principal_kind = 'agent' AND principal_id = ?`, mentionedAgentID).Scan(&messageMentions); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM agent_held_draft_mentions WHERE principal_kind = 'agent' AND principal_id = ?`, mentionedAgentID).Scan(&draftMentions); err != nil {
		t.Fatal(err)
	}
	if messageBodies != 1 || draftBodies != 1 || messageMentions != 1 || draftMentions != 1 {
		t.Fatalf("business facts = message bodies %d, draft bodies %d, message mentions %d, draft mentions %d", messageBodies, draftBodies, messageMentions, draftMentions)
	}

	rows, err := f.database.db.Query(`SELECT response_snapshot FROM inbox_requests`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	privateValues := []string{
		freshBody,
		heldBody,
		mentionedAgentID,
		rtToken(42),
		rtToken(250),
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
	if err := f.database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE actor_kind = 'agent' AND actor_id = ?`, f.agentID).Scan(&agentAuditCount); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE actor_kind = 'agent' AND actor_id = ? AND action = ?`, f.agentID, AuditMessageSend).Scan(&messageSendAuditCount); err != nil {
		t.Fatal(err)
	}
	if agentAuditCount != 1 || messageSendAuditCount != 1 {
		t.Fatalf("agent audits = %d, message.send = %d; held/cancel must not fake publish", agentAuditCount, messageSendAuditCount)
	}
}

func TestInboxRequestReplayFailsClosedOnBrokenBusinessFacts(t *testing.T) {
	t.Run("message missing", func(t *testing.T) {
		f := openInboxFixture(t)
		params, result, err := f.sendInboxReply(t, "missing-message-body", nil, false, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.database.db.Exec(`DELETE FROM messages WHERE id = ?`, result.Message.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = f.at(20)
		if _, err := f.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("missing message replay error = %v", err)
		}
	})

	t.Run("message mentions missing", func(t *testing.T) {
		f := openInboxFixture(t)
		mentionedAgentID := f.createMentionMember(t, "missing-mention", f.at(1))
		params, result, err := f.sendInboxReply(t, "missing-mention-body", []string{mentionedAgentID}, false, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.database.db.Exec(`DELETE FROM message_mentions WHERE message_id = ?`, result.Message.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = f.at(20)
		if _, err := f.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("missing message mentions replay error = %v", err)
		}
	})

	t.Run("message owner mismatch", func(t *testing.T) {
		f := openInboxFixture(t)
		otherAgentID := f.createMentionMember(t, "wrong-owner", f.at(1))
		params, result, err := f.sendInboxReply(t, "wrong-owner-body", nil, false, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.database.db.Exec(`UPDATE messages SET author_id = ? WHERE id = ?`, otherAgentID, result.Message.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = f.at(20)
		if _, err := f.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("message owner mismatch replay error = %v", err)
		}
	})

	t.Run("held draft missing", func(t *testing.T) {
		f := openInboxFixture(t)
		params, result, err := f.sendInboxReply(t, "missing-draft-body", nil, true, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.database.db.Exec(`DELETE FROM agent_held_drafts WHERE id = ?`, result.HeldDraft.ID); err != nil {
			t.Fatal(err)
		}
		params.Now = f.at(20)
		if _, err := f.database.SendInboxReply(context.Background(), params); !errors.Is(err, ErrInboxIntegrity) {
			t.Fatalf("missing held draft replay error = %v", err)
		}
	})
}

func TestHeldDraftRetryStaleCreatesCanonicalSuccessor(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	item, original := f.makeHeldDraft(t, f.group, 1)
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner, Target: item.Target,
		Body: "advance again", Now: f.at(8),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	params := ResolveHeldDraftParams{
		RequestID: requestID, Authentication: f.authentication,
		HeldDraftID: original.ID, Action: DraftResolutionRetry,
		BasisTargetSequence: observed.Head, Now: f.at(9),
	}
	result, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || result.Kind != InboxResultHeldDraft || result.HeldDraft == nil || result.InboxItem.State != InboxStateClaimed {
		t.Fatalf("stale retry = %+v, %v", result, err)
	}
	if result.HeldDraft.PredecessorDraftID != original.ID || result.HeldDraft.Sequence <= original.Sequence {
		t.Fatalf("successor = %+v, original = %+v", result.HeldDraft, original)
	}
	resolvedOriginal := readHeldDraft(t, f.database, original.ID)
	if resolvedOriginal.State != HeldDraftStateSuperseded || resolvedOriginal.ResolutionAction != DraftResolutionRetry || resolvedOriginal.ResultKind != InboxResultHeldDraft || resolvedOriginal.ResultID != result.HeldDraft.ID {
		t.Fatalf("resolved original = %+v", resolvedOriginal)
	}
	params.Now = f.at(10)
	replayed, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("stale retry replay = %+v, %v", replayed, err)
	}
	if _, err := f.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: uuid.NewString(), Authentication: f.authentication,
		HeldDraftID: result.HeldDraft.ID, Action: DraftResolutionCancel, Now: f.at(11),
	}); err != nil {
		t.Fatal(err)
	}
	params.Now = f.at(12)
	replayedAfterSuccessorTerminal, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayedAfterSuccessorTerminal) {
		t.Fatalf("stale retry replay after successor terminal = %+v, %v", replayedAfterSuccessorTerminal, err)
	}
	secondRequest := uuid.NewString()
	params.RequestID = secondRequest
	params.Now = f.at(13)
	if _, err := f.database.ResolveHeldDraft(ctx, params); !errors.Is(err, ErrHeldDraftNotHeld) {
		t.Fatalf("second stale retry error = %v", err)
	}
	assertAgentRequestCount(t, f.database, secondRequest, 0)
}

func TestHeldDraftRetargetFreshPublishesCanonicalMessage(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	item, original := f.makeHeldDraft(t, f.group, 1)
	targetGroup := f.createAgentGroup(t, "Retarget fresh", 7)
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		Target:         MessageTarget{Kind: MessageTargetSpace, ID: targetGroup.ID}, Limit: 20, Now: f.at(10),
	})
	if err != nil || observed.Head != 0 {
		t.Fatalf("retarget observe = %+v, %v", observed, err)
	}
	requestID := uuid.NewString()
	params := ResolveHeldDraftParams{
		RequestID: requestID, Authentication: f.authentication,
		HeldDraftID: original.ID, Action: DraftResolutionRetarget,
		Target:              MessageTarget{Kind: MessageTargetSpace, ID: targetGroup.ID},
		BasisTargetSequence: observed.Head, Now: f.at(11),
	}
	result, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || result.Kind != InboxResultMessage || result.Message == nil || result.Message.SpaceID != targetGroup.ID || result.InboxItem.State != InboxStateDone {
		t.Fatalf("fresh retarget = %+v, %v", result, err)
	}
	resolvedOriginal := readHeldDraft(t, f.database, original.ID)
	if resolvedOriginal.State != HeldDraftStateRetargeted || resolvedOriginal.ResolutionAction != DraftResolutionRetarget || resolvedOriginal.ResultKind != InboxResultMessage || resolvedOriginal.ResultID != result.Message.ID {
		t.Fatalf("resolved original = %+v", resolvedOriginal)
	}
	params.Now = f.at(12)
	replayed, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("fresh retarget replay = %+v, %v", replayed, err)
	}
	secondRequest := uuid.NewString()
	params.RequestID = secondRequest
	params.Now = f.at(13)
	if _, err := f.database.ResolveHeldDraft(ctx, params); !errors.Is(err, ErrHeldDraftNotHeld) {
		t.Fatalf("second fresh retarget error = %v", err)
	}
	assertAgentRequestCount(t, f.database, secondRequest, 0)
	if current := readInboxItem(t, f.database, item.ID); current.Completion != InboxCompletionSent {
		t.Fatalf("retargeted item = %+v", current)
	}
}

func TestHeldDraftRetargetStaleCreatesCanonicalSuccessor(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	item, original := f.makeHeldDraft(t, f.group, 1)
	targetGroup := f.createAgentGroup(t, "Retarget stale", 7)
	target := MessageTarget{Kind: MessageTargetSpace, ID: targetGroup.ID}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner, Target: target, Body: "target root", Now: f.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: target, Limit: 20, Now: f.at(11),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner, Target: target, Body: "target advanced", Now: f.at(12),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	params := ResolveHeldDraftParams{
		RequestID: requestID, Authentication: f.authentication,
		HeldDraftID: original.ID, Action: DraftResolutionRetarget, Target: target,
		BasisTargetSequence: observed.Head, Now: f.at(13),
	}
	result, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || result.Kind != InboxResultHeldDraft || result.HeldDraft == nil || result.HeldDraft.Target != target || result.InboxItem.State != InboxStateClaimed {
		t.Fatalf("stale retarget = %+v, %v", result, err)
	}
	resolvedOriginal := readHeldDraft(t, f.database, original.ID)
	if resolvedOriginal.State != HeldDraftStateRetargeted || resolvedOriginal.ResolutionAction != DraftResolutionRetarget || resolvedOriginal.ResultKind != InboxResultHeldDraft || resolvedOriginal.ResultID != result.HeldDraft.ID {
		t.Fatalf("resolved original = %+v", resolvedOriginal)
	}
	params.Now = f.at(14)
	replayed, err := f.database.ResolveHeldDraft(ctx, params)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("stale retarget replay = %+v, %v", replayed, err)
	}
	secondRequest := uuid.NewString()
	params.RequestID = secondRequest
	params.Now = f.at(15)
	if _, err := f.database.ResolveHeldDraft(ctx, params); !errors.Is(err, ErrHeldDraftNotHeld) {
		t.Fatalf("second stale retarget error = %v", err)
	}
	assertAgentRequestCount(t, f.database, secondRequest, 0)
	if current := readInboxItem(t, f.database, item.ID); current.State != InboxStateClaimed {
		t.Fatalf("stale retarget item = %+v", current)
	}
}

func TestHeldDraftListCursorLimitAndAccessLossFill(t *testing.T) {
	f := openInboxFixture(t)
	_, inaccessible := f.makeHeldDraft(t, f.group, 1)
	secondGroup := f.createAgentGroup(t, "Held list", 7)
	_, visible := f.makeHeldDraft(t, secondGroup, 10)
	if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, GrantID: f.readGrant.ID, Now: f.at(16),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := f.database.ListHeldDrafts(context.Background(), ListHeldDraftsParams{
		Authentication: f.authentication, Limit: 1, Now: f.at(17),
	})
	if err != nil || len(result.Drafts) != 1 || result.Drafts[0].ID != visible.ID || result.NextSequence != visible.Sequence || result.NextSequence <= inaccessible.Sequence {
		t.Fatalf("bounded held list = %+v, %v", result, err)
	}
	next, err := f.database.ListHeldDrafts(context.Background(), ListHeldDraftsParams{
		Authentication: f.authentication, AfterSequence: result.NextSequence, Limit: 1, Now: f.at(18),
	})
	if err != nil || len(next.Drafts) != 0 || next.NextSequence != result.NextSequence {
		t.Fatalf("held list next page = %+v, %v", next, err)
	}
	for _, limit := range []uint32{0, maxInboxListLimit + 1} {
		if _, err := f.database.ListHeldDrafts(context.Background(), ListHeldDraftsParams{
			Authentication: f.authentication, Limit: limit, Now: f.at(19),
		}); !errors.Is(err, ErrInvalidInboxLimit) {
			t.Fatalf("limit %d error = %v", limit, err)
		}
	}
}

func TestInboxAccessLossClosesItemsWithoutResurrection(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	for index := 0; index < 2; index++ {
		if _, err := f.database.SendMessage(ctx, SendMessageParams{
			RequestID: uuid.NewString(), Actor: f.owner,
			Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "pending",
			MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(index + 1),
		}); err != nil {
			t.Fatal(err)
		}
	}
	secondGroup, err := f.database.CreateGroup(ctx, CreateGroupParams{
		RequestID: uuid.NewString(), Actor: f.owner, Name: "Still readable", Now: f.at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.AddMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: secondGroup.ID,
		Member: Principal{Kind: "agent", ID: f.agentID}, Now: f.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	f.issueAgentGrant(t, secondGroup.ID, CapabilitySpaceRead, f.at(5))
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: secondGroup.ID}, Body: "still visible",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RevokeGrant(ctx, RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, GrantID: f.readGrant.ID, Now: f.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	items, err := f.database.ListInboxItems(ctx, ListInboxItemsParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Limit: 1, Now: f.at(8),
	})
	if err != nil || len(items) != 1 || items[0].SpaceID != secondGroup.ID {
		t.Fatalf("items after access loss = %+v, %v", items, err)
	}
	f.claim(t, items[0].ID, uuid.NewString(), f.at(9))
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		InboxItemID: items[0].ID, Now: f.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	notice, err := f.database.GetInboxNotice(ctx, InboxNoticeParams{Authentication: executionapp.AgentInboxAuthentication(f.authentication), Now: f.at(11)})
	if err != nil || notice {
		t.Fatalf("notice after access loss = %v, %v", notice, err)
	}
	var closed int
	if err := f.database.db.QueryRow(`
		SELECT count(*) FROM inbox_items
		WHERE recipient_kind = 'agent' AND recipient_id = ? AND state = 'done' AND completion = 'access_lost'
	`, f.agentID).Scan(&closed); err != nil || closed != 2 {
		t.Fatalf("access-lost items = %d, %v", closed, err)
	}
	f.readGrant = f.issueAgentGrant(t, f.group.ID, CapabilitySpaceRead, f.at(12))
	items = f.listItems(t, 10, f.at(13))
	if len(items) != 0 {
		t.Fatalf("access-lost items resurrected: %+v", items)
	}
}

func TestInboxSiblingAdvanceDoesNotHoldReply(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	root, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "root",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	item := f.listItems(t, 1, f.at(2))[0]
	f.claim(t, item.ID, uuid.NewString(), f.at(3))
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "sibling thread", Now: f.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: uuid.NewString(), Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "root reply", Now: f.at(6),
	})
	if err != nil || result.Kind != InboxResultMessage || result.Message == nil {
		t.Fatalf("sibling advance reply = %+v, %v", result, err)
	}
}

func TestInboxArchiveAndRuntimeFailuresWriteNoDraftOrReceipt(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "trigger",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	}); err != nil {
		t.Fatal(err)
	}
	item := f.listItems(t, 1, f.at(2))[0]
	f.claim(t, item.ID, uuid.NewString(), f.at(3))
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ArchiveSpace(ctx, ChangeSpaceArchiveParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID, Now: f.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	if _, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: requestID, Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "must fail", Now: f.at(6),
	}); !errors.Is(err, ErrSpaceArchived) {
		t.Fatalf("archived send error = %v", err)
	}
	assertInboxRequestWrites(t, f.database, requestID, 0, 0)

	if err := f.database.RevokeSession(ctx, RevokeAgentRuntimeSessionParams{
		Proof: f.authentication.Proof, ComputerID: f.authentication.Proof.ComputerID(),
		RegistrationKey: "computer-registration-key", Now: f.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	runtimeRequest := uuid.NewString()
	if _, err := f.database.CompleteInboxItem(ctx, CompleteInboxItemParams{
		RequestID: runtimeRequest, Authentication: executionapp.AgentInboxAuthentication(f.authentication), InboxItemID: item.ID, Now: f.at(8),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("revoked runtime completion error = %v", err)
	}
	assertInboxRequestWrites(t, f.database, runtimeRequest, 0, 0)
}

func TestHeldDraftDoesNotResurrectAfterAccessLoss(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "trigger",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	}); err != nil {
		t.Fatal(err)
	}
	item := f.listItems(t, 1, f.at(2))[0]
	f.claim(t, item.ID, uuid.NewString(), f.at(3))
	observed, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: item.Target, Body: "advance", Now: f.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	held, err := f.database.SendInboxReply(ctx, SendInboxReplyParams{
		RequestID: uuid.NewString(), Authentication: f.authentication, InboxItemID: item.ID,
		BasisTargetSequence: observed.Head, Body: "held", Now: f.at(6),
	})
	if err != nil || held.HeldDraft == nil {
		t.Fatalf("held = %+v, %v", held, err)
	}
	if _, err := f.database.RevokeGrant(ctx, RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, GrantID: f.readGrant.ID, Now: f.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	drafts, err := f.database.ListHeldDrafts(ctx, ListHeldDraftsParams{
		Authentication: f.authentication, Limit: 10, Now: f.at(8),
	})
	if err != nil || len(drafts.Drafts) != 0 || drafts.NextSequence != held.HeldDraft.Sequence {
		t.Fatalf("drafts after access loss = %+v, %v", drafts, err)
	}
	f.readGrant = f.issueAgentGrant(t, f.group.ID, CapabilitySpaceRead, f.at(9))
	drafts, err = f.database.ListHeldDrafts(ctx, ListHeldDraftsParams{
		Authentication: f.authentication, Limit: 10, Now: f.at(10),
	})
	if err != nil || len(drafts.Drafts) != 0 || drafts.NextSequence != held.HeldDraft.Sequence {
		t.Fatalf("drafts after restored access = %+v, %v", drafts, err)
	}
	resolveRequest := uuid.NewString()
	if _, err := f.database.ResolveHeldDraft(ctx, ResolveHeldDraftParams{
		RequestID: resolveRequest, Authentication: f.authentication,
		HeldDraftID: held.HeldDraft.ID, Action: DraftResolutionCancel, Now: f.at(11),
	}); !errors.Is(err, ErrInboxItemNotClaimed) {
		t.Fatalf("access-lost draft resolution error = %v", err)
	}
	assertInboxRequestWrites(t, f.database, resolveRequest, 0, 1)
}

func TestInboxMentionValidationRejectsInvalidSets(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	nonmemberAgent, err := f.database.CreateAgent(ctx, testCreateAgentParams(f.owner, "outside", f.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	nonmember := nonmemberAgent.ID
	for name, mentions := range map[string][]string{
		"duplicate": {f.agentID, f.agentID},
		"nonmember": {nonmember},
		"invalid":   {"not-a-uuid"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.database.SendMessage(ctx, SendMessageParams{
				RequestID: uuid.NewString(), Actor: f.owner,
				Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID},
				Body:   "invalid mention", MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, mentions...), Now: f.at(2),
			}); !errors.Is(err, ErrInvalidMention) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	tooMany := make([]string, maxMentionCount+1)
	for index := range tooMany {
		tooMany[index] = uuid.NewString()
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID},
		Body:   "too many", MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, tooMany...), Now: f.at(3),
	}); !errors.Is(err, ErrInvalidMention) {
		t.Fatalf("too-many error = %v", err)
	}
	var messages int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM messages WHERE space_id = ?`, f.group.ID).Scan(&messages); err != nil || messages != 0 {
		t.Fatalf("invalid mention messages = %d, %v", messages, err)
	}
}

func TestInboxSchemaRejectsBrokenMentionAndTriggerFacts(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	message, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "valid",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.database.CreateAgent(ctx, testCreateAgentParams(f.owner, "second", f.at(2)))
	if err != nil {
		t.Fatal(err)
	}
	secondAgent := second.ID
	assertExecFails(t, f.database, `INSERT INTO message_mentions(message_id, principal_kind, principal_id, ordinal) VALUES(?, 'agent', ?, 0)`, message.ID, secondAgent)
	assertExecFails(t, f.database, `INSERT INTO message_mentions(message_id, principal_kind, principal_id, ordinal) VALUES(?, 'agent', ?, 64)`, message.ID, secondAgent)
	assertExecFails(t, f.database, `INSERT INTO message_mentions(message_id, principal_kind, principal_id, ordinal) VALUES(?, 'agent', ?, 1)`, uuid.NewString(), secondAgent)
	assertExecFails(t, f.database, `
		UPDATE inbox_items SET trigger_target_sequence = trigger_target_sequence + 1
		WHERE recipient_kind = 'agent' AND recipient_id = ? AND trigger_message_id = ?
	`, f.agentID, message.ID)
	assertExecFails(t, f.database, `
		UPDATE inbox_items SET state = 'done', completion = '', done_at = ?
		WHERE recipient_kind = 'agent' AND recipient_id = ? AND trigger_message_id = ?
	`, unixNano(f.at(3)), f.agentID, message.ID)
	item := f.listItems(t, 1, f.at(4))[0]
	draftID := uuid.NewString()
	if _, err := f.database.db.Exec(`
		INSERT INTO agent_held_drafts(
			id, agent_id, inbox_item_id, space_id, target_kind, target_id,
			basis_target_sequence, body, held_reason, state, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, 'held', 'target_advanced', 'held', ?, ?)
	`, draftID, f.agentID, item.ID, item.SpaceID, item.Target.Kind, item.Target.ID,
		item.TriggerTargetSequence, unixNano(f.at(5)), unixNano(f.at(5))); err != nil {
		t.Fatal(err)
	}
	for name, values := range map[string][]string{
		"sent held draft":     {HeldDraftStateSent, DraftResolutionRetry, InboxResultHeldDraft},
		"superseded retarget": {HeldDraftStateSuperseded, DraftResolutionRetarget, InboxResultHeldDraft},
		"retargeted retry":    {HeldDraftStateRetargeted, DraftResolutionRetry, InboxResultMessage},
	} {
		t.Run(name, func(t *testing.T) {
			assertExecFails(t, f.database, `
				UPDATE agent_held_drafts
				SET state = ?, resolution_action = ?, result_kind = ?, result_id = ?
				WHERE id = ?
			`, values[0], values[1], values[2], uuid.NewString(), draftID)
		})
	}
	var planID, parentID, unused int
	var detail string
	if err := f.database.db.QueryRow(`
		EXPLAIN QUERY PLAN
		SELECT sequence, id FROM agent_held_drafts
		WHERE agent_id = ? AND state = 'held' AND sequence > ?
		ORDER BY sequence
		LIMIT ?
	`, f.agentID, 0, 10).Scan(&planID, &parentID, &unused, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "agent_held_drafts_agent_state_sequence") || !strings.Contains(detail, "sequence>?") {
		t.Fatalf("held draft list query plan = %q", detail)
	}
	rows, err := f.database.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check found a violation")
	}
}

func TestInboxMembershipRemovalTerminalizesProjection(t *testing.T) {
	f := openInboxFixture(t)
	ctx := context.Background()
	root, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "root",
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetSpaceMute(ctx, SetSpaceMuteParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		SpaceID: f.group.ID, Muted: true, Now: f.at(2),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SendMessage(ctx, SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Body: "create thread", Now: f.at(3),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.SetThreadFollow(ctx, SetThreadFollowParams{
		RequestID: uuid.NewString(), Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		ThreadID: root.ID, Followed: true, Now: f.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ObserveTarget(ctx, ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID},
		Limit: 10, Now: f.at(5),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RemoveMember(ctx, ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID,
		Member: Principal{Kind: "agent", ID: f.agentID}, Now: f.at(6),
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"principal_space_mutes", "principal_thread_follows", "principal_target_cursors"} {
		var count int
		if err := f.database.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE principal_kind = 'agent' AND principal_id = ?`, f.agentID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows = %d, %v", table, count, err)
		}
	}
	var completion string
	if err := f.database.db.QueryRow(`SELECT completion FROM inbox_items WHERE trigger_message_id = ? AND recipient_kind = 'agent' AND recipient_id = ?`, root.ID, f.agentID).Scan(&completion); err != nil || completion != InboxCompletionAccessLost {
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
	bootstrap, err := runtimeFixture.database.EnsureAuthority(context.Background(), rtToken(250), runtimeFixture.now)
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
	f := &inboxFixture{
		database: runtimeFixture.database, owner: owner, rootGrant: bootstrap.RootGrant,
		agentID: runtimeFixture.agentID, group: group, now: runtimeFixture.now.Add(10 * time.Second),
	}
	f.readGrant = f.issueAgentGrant(t, group.ID, CapabilitySpaceRead, runtimeFixture.now.Add(3*time.Second))
	f.issueAgentGrant(t, group.ID, CapabilityMessageSend, runtimeFixture.now.Add(4*time.Second))
	token := rtToken(42)
	createRuntimeSession(t, runtimeFixture, token, runtimeFixture.now.Add(5*time.Second))
	authentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), token, runtimeFixture.now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	f.authentication = authentication
	return f
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
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(start),
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
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 20, Now: f.at(start + 3),
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
	agent, err := f.database.CreateAgent(context.Background(), testCreateAgentParams(f.owner, name, now))
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
		MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID), Now: f.at(start),
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
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Target: item.Target, Limit: 200, Now: f.at(start + 3),
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
		BasisTargetSequence: observed.Head, Body: body, MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, mentions...), Now: f.at(start + 5),
	}
	result, err := f.database.SendInboxReply(ctx, params)
	return params, result, err
}

func (f *inboxFixture) listItems(t *testing.T, limit uint32, now time.Time) []InboxItem {
	t.Helper()
	items, err := f.database.ListInboxItems(context.Background(), ListInboxItemsParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication), Limit: limit, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func (f *inboxFixture) claim(t *testing.T, itemID, requestID string, now time.Time) InboxItem {
	t.Helper()
	item, err := f.database.ClaimInboxItem(context.Background(), ClaimInboxItemParams{
		RequestID: requestID, Authentication: executionapp.AgentInboxAuthentication(f.authentication), InboxItemID: itemID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func assertInboxRequestWrites(t *testing.T, database *Store, requestID string, wantRequests, wantDrafts int) {
	t.Helper()
	var requests, drafts int
	if err := database.db.QueryRow(`SELECT count(*) FROM inbox_requests WHERE request_id = ?`, requestID).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM agent_held_drafts`).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if requests != wantRequests || drafts != wantDrafts {
		t.Fatalf("request writes = %d, drafts = %d; want %d, %d", requests, drafts, wantRequests, wantDrafts)
	}
}

func agentPrincipals(organizationID string, ids ...string) []Principal {
	principals := make([]Principal, 0, len(ids))
	for _, id := range ids {
		principals = append(principals, Principal{Kind: PrincipalAgent, ID: id, OrganizationID: organizationID})
	}
	return principals
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
	if err := database.db.QueryRow(`SELECT count(*) FROM inbox_requests WHERE request_id = ?`, requestID).Scan(&count); err != nil {
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
