package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDMCanonicalConcurrencyReplayRestartAndCredentialIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, owner, peer, credential, now := openCollaborationFixture(t, path)

	const callers = 20
	requests := make([]string, callers)
	results := make([]Space, callers)
	errorsByIndex := make([]error, callers)
	var wait sync.WaitGroup
	for index := range callers {
		requests[index] = uuid.NewString()
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errorsByIndex[index] = database.CreateDM(context.Background(), CreateDMParams{
				RequestID: requests[index], Actor: owner, Peer: peer, Now: now.Add(time.Duration(index+10) * time.Second),
			})
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("create dm %d: %v", index, err)
		}
		if results[index].ID != results[0].ID {
			t.Fatalf("create dm %d id = %q, want %q", index, results[index].ID, results[0].ID)
		}
	}
	var spaces, memberships, receipts int
	if err := database.db.QueryRow(`SELECT count(*) FROM spaces WHERE kind = 'dm'`).Scan(&spaces); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM space_memberships WHERE space_id = ?`, results[0].ID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM collaboration_requests WHERE operation = ?`, operationCreateDM).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if spaces != 1 || memberships != 2 || receipts != callers {
		t.Fatalf("canonical dm facts spaces/memberships/receipts = %d/%d/%d", spaces, memberships, receipts)
	}
	if got := countAuditAction(t, database, owner.OrganizationID, AuditSpaceCreate, "committed"); got != 1 {
		t.Fatalf("committed space.create audits = %d, want 1", got)
	}

	replayed, err := database.CreateDM(context.Background(), CreateDMParams{
		RequestID: requests[0], Actor: owner, Peer: peer, Now: now.Add(24 * time.Hour),
	})
	if err != nil || replayed.ID != results[0].ID {
		t.Fatalf("dm replay = %+v, %v", replayed, err)
	}
	if got := countAuditAction(t, database, owner.OrganizationID, AuditSpaceCreate, "committed"); got != 1 {
		t.Fatalf("dm replay duplicated audit: %d", got)
	}
	if _, err := database.CreateDM(context.Background(), CreateDMParams{
		RequestID: requests[0], Actor: owner, Peer: Principal{Kind: "agent", ID: uuid.NewString()}, Now: now.Add(25 * time.Hour),
	}); !errors.Is(err, ErrCollaborationRequestConflict) {
		t.Fatalf("dm request conflict error = %v", err)
	}

	members, err := database.ListMembers(context.Background(), SpaceReadParams{Actor: owner, SpaceID: results[0].ID, Now: now.Add(time.Hour)})
	if err != nil || len(members) != 2 {
		t.Fatalf("dm members = %+v, %v", members, err)
	}
	encoded, err := json.Marshal(struct {
		Space   Space
		Members []Membership
	}{results[0], members})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), credential) {
		t.Fatal("collaboration facts leaked a human credential")
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	afterRestart, err := database.CreateDM(context.Background(), CreateDMParams{
		RequestID: requests[0], Actor: owner, Peer: peer, Now: now.Add(48 * time.Hour),
	})
	if err != nil || afterRestart.ID != results[0].ID {
		t.Fatalf("dm restart replay = %+v, %v", afterRestart, err)
	}
}

func TestMessageTargetSequencesThreadIsolationCursorReplayAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, owner, _, _, now := openCollaborationFixture(t, path)
	group, err := database.CreateGroup(context.Background(), CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "sequence-lab", Now: now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	rootParams := SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: group.ID}, Body: "root", Now: now.Add(11 * time.Second),
	}
	root, err := database.SendMessage(context.Background(), rootParams)
	if err != nil {
		t.Fatal(err)
	}
	if root.TargetSequence != 1 || root.Target.Kind != MessageTargetSpace {
		t.Fatalf("root message = %+v", root)
	}
	if root.RequestID != rootParams.RequestID {
		t.Fatalf("root request id = %q, want %q", root.RequestID, rootParams.RequestID)
	}
	rootAudit := auditEventByTarget(t, database, owner.OrganizationID, AuditMessageSend, root.ID, "committed")
	if rootAudit.ContextKind != "space" || rootAudit.ContextID != group.ID {
		t.Fatalf("root message audit context = %+v", rootAudit)
	}
	var storedFingerprint []byte
	if err := database.db.QueryRow(`SELECT payload_fingerprint FROM messages WHERE id = ?`, root.ID).Scan(&storedFingerprint); err != nil {
		t.Fatal(err)
	}
	if len(storedFingerprint) != 32 {
		t.Fatalf("message fingerprint length = %d", len(storedFingerprint))
	}

	const concurrent = 20
	mainParams := make([]SendMessageParams, concurrent)
	threadParams := make([]SendMessageParams, concurrent)
	mainResults := make([]Message, concurrent)
	threadResults := make([]Message, concurrent)
	errorsByIndex := make([]error, concurrent*2)
	var wait sync.WaitGroup
	for index := range concurrent {
		mainParams[index] = SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner,
			Target: MessageTarget{Kind: MessageTargetSpace, ID: group.ID},
			Body:   fmt.Sprintf("main-%02d", index), Now: now.Add(time.Duration(index+20) * time.Second),
		}
		if index == 0 {
			mainParams[index].MentionedPrincipals = []Principal{owner}
		}
		threadParams[index] = SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner,
			Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID},
			Body:   fmt.Sprintf("thread-%02d", index), Now: now.Add(time.Duration(index+50) * time.Second),
		}
		wait.Add(2)
		go func(index int) {
			defer wait.Done()
			mainResults[index], errorsByIndex[index] = database.SendMessage(context.Background(), mainParams[index])
		}(index)
		go func(index int) {
			defer wait.Done()
			threadResults[index], errorsByIndex[concurrent+index] = database.SendMessage(context.Background(), threadParams[index])
		}(index)
	}
	wait.Wait()
	for index, err := range errorsByIndex {
		if err != nil {
			t.Fatalf("concurrent message %d: %v", index, err)
		}
	}
	assertSequences(t, append([]Message{root}, mainResults...), concurrent+1)
	assertSequences(t, threadResults, concurrent)
	if got := countAuditAction(t, database, owner.OrganizationID, AuditThreadCreate, "committed"); got != 1 {
		t.Fatalf("thread.create audits = %d, want 1", got)
	}
	mainAudit := auditEventByTarget(t, database, owner.OrganizationID, AuditMessageSend, mainResults[0].ID, "committed")
	if mainAudit.ContextKind != "space" || mainAudit.ContextID != group.ID {
		t.Fatalf("main message audit context = %+v", mainAudit)
	}
	replyAudit := auditEventByTarget(t, database, owner.OrganizationID, AuditMessageSend, threadResults[0].ID, "committed")
	if replyAudit.ContextKind != "thread" || replyAudit.ContextID != root.ID {
		t.Fatalf("thread reply audit context = %+v", replyAudit)
	}
	threadAudit := auditEventByTarget(t, database, owner.OrganizationID, AuditThreadCreate, root.ID, "committed")
	if threadAudit.ContextKind != "space" || threadAudit.ContextID != group.ID {
		t.Fatalf("thread creation audit context = %+v", threadAudit)
	}

	auditsBeforeReplay := auditCount(t, database, owner.OrganizationID)
	replayed, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: mainParams[0].RequestID, Actor: owner, Target: mainParams[0].Target,
		Body: mainParams[0].Body, MentionedPrincipals: mainParams[0].MentionedPrincipals, Now: now.Add(72 * time.Hour),
	})
	if err != nil || replayed.ID != mainResults[0].ID || replayed.TargetSequence != mainResults[0].TargetSequence {
		t.Fatalf("message replay = %+v, %v", replayed, err)
	}
	if len(replayed.MentionedPrincipals) != 1 || replayed.MentionedPrincipals[0] != owner {
		t.Fatalf("message replay mention lost canonical organization: %+v", replayed.MentionedPrincipals)
	}
	if auditCount(t, database, owner.OrganizationID) != auditsBeforeReplay {
		t.Fatal("message replay duplicated audit")
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: mainParams[0].RequestID, Actor: owner, Target: mainParams[0].Target,
		Body: "changed", Now: now.Add(73 * time.Hour),
	}); !errors.Is(err, ErrCollaborationRequestConflict) {
		t.Fatalf("message request conflict error = %v", err)
	}

	mainMessages, err := database.ListMessages(context.Background(), ListMessagesParams{
		Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: group.ID}, Limit: maxMessageListLimit, Now: now.Add(time.Hour),
	})
	if err != nil || len(mainMessages) != concurrent+1 {
		t.Fatalf("main messages = %d, %v", len(mainMessages), err)
	}
	threadMessages, err := database.ListMessages(context.Background(), ListMessagesParams{
		Actor: owner, Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Limit: maxMessageListLimit, Now: now.Add(time.Hour),
	})
	if err != nil || len(threadMessages) != concurrent {
		t.Fatalf("thread messages = %d, %v", len(threadMessages), err)
	}
	page, err := database.ListMessages(context.Background(), ListMessagesParams{
		Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: group.ID}, AfterSequence: 5, Limit: 3, Now: now.Add(time.Hour),
	})
	if err != nil || len(page) != 3 || page[0].TargetSequence != 6 || page[2].TargetSequence != 8 {
		t.Fatalf("cursor page = %+v, %v", page, err)
	}
	thread, err := database.GetThread(context.Background(), GetThreadParams{Actor: owner, ThreadID: root.ID, Now: now.Add(time.Hour)})
	if err != nil || thread.ID != root.ID || thread.SpaceID != group.ID {
		t.Fatalf("thread fact = %+v, %v", thread, err)
	}

	messagesBeforeWrongRoot := len(mainMessages) + len(threadMessages)
	if _, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner,
		Target: MessageTarget{Kind: MessageTargetThread, ID: threadMessages[0].ID},
		Body:   "nested", Now: now.Add(80 * time.Hour),
	}); !errors.Is(err, ErrInvalidMessageTarget) {
		t.Fatalf("nested thread error = %v", err)
	}
	if latest := latestAudit(t, database, owner.OrganizationID); latest.Action != AuditMessageSend || latest.Outcome != "denied" || latest.ReasonCode != "target_invalid" {
		t.Fatalf("wrong-root audit = %+v", latest)
	}
	var messageCount int
	if err := database.db.QueryRow(`SELECT count(*) FROM messages`).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if messageCount != messagesBeforeWrongRoot {
		t.Fatalf("wrong root changed messages: %d", messageCount)
	}

	if _, err := database.ArchiveSpace(context.Background(), ChangeSpaceArchiveParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Now: now.Add(81 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ListMessages(context.Background(), ListMessagesParams{
		Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: group.ID}, Limit: 1, Now: now.Add(82 * time.Hour),
	}); err != nil {
		t.Fatalf("archived group should remain readable: %v", err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: group.ID},
		Body: "blocked", Now: now.Add(83 * time.Hour),
	}); !errors.Is(err, ErrSpaceArchived) {
		t.Fatalf("archived send error = %v", err)
	}
	if _, err := database.UnarchiveSpace(context.Background(), ChangeSpaceArchiveParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Now: now.Add(84 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	afterRestart, err := database.ListMessages(context.Background(), ListMessagesParams{
		Actor: owner, Target: MessageTarget{Kind: MessageTargetThread, ID: root.ID}, Limit: maxMessageListLimit, Now: now.Add(90 * time.Hour),
	})
	if err != nil || len(afterRestart) != concurrent {
		t.Fatalf("thread messages after restart = %d, %v", len(afterRestart), err)
	}
}

func TestGroupMembershipLifecycleDMImmutabilityAndDeniedSenders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, owner, peer, credential, now := openCollaborationFixture(t, path)
	defer database.Close()
	group, err := database.CreateGroup(context.Background(), CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "membership-lab", Now: now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RemoveMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Member: owner, Now: now.Add(11 * time.Second),
	}); !errors.Is(err, ErrLastActiveHumanMember) {
		t.Fatalf("remove last active human error = %v", err)
	}
	if latest := latestAudit(t, database, owner.OrganizationID); latest.Action != AuditSpaceMemberRemove || latest.Outcome != "denied" || latest.ReasonCode != "last_active_human" || latest.TargetKind != "human" || latest.TargetID != owner.ID || latest.ContextKind != "space" || latest.ContextID != group.ID {
		t.Fatalf("last-human audit = %+v", latest)
	}

	addRequest := uuid.NewString()
	addParams := ChangeMemberParams{RequestID: addRequest, Actor: owner, SpaceID: group.ID, Member: peer, Now: now.Add(12 * time.Second)}
	firstReceipt, err := database.AddMember(context.Background(), addParams)
	if err != nil {
		t.Fatal(err)
	}
	addAudit := auditEventByTarget(t, database, owner.OrganizationID, AuditSpaceMemberAdd, peer.ID, "committed")
	if addAudit.TargetKind != "human" || addAudit.ContextKind != "space" || addAudit.ContextID != group.ID {
		t.Fatalf("human membership add audit = %+v", addAudit)
	}
	addParams.Now = now.Add(24 * time.Hour)
	secondReceipt, err := database.AddMember(context.Background(), addParams)
	if err != nil || !secondReceipt.CommittedAt.Equal(firstReceipt.CommittedAt) {
		t.Fatalf("membership replay = %+v / %+v, %v", firstReceipt, secondReceipt, err)
	}
	if _, err := database.GetSpace(context.Background(), SpaceReadParams{Actor: peer, SpaceID: group.ID, Now: now.Add(time.Hour)}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("member without space.read grant error = %v", err)
	}
	if _, err := database.RemoveMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Member: peer, Now: now.Add(13 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	removeAudit := auditEventByTarget(t, database, owner.OrganizationID, AuditSpaceMemberRemove, peer.ID, "committed")
	if removeAudit.TargetKind != "human" || removeAudit.ContextKind != "space" || removeAudit.ContextID != group.ID {
		t.Fatalf("human membership remove audit = %+v", removeAudit)
	}
	dormant := createTestHuman(t, database, owner, "Dormant Member", "member", "dormant-member-credential-abcdefghijklmnopqrstuvwxyz", now.Add(14*time.Second))
	dormantPrincipal := Principal{Kind: "human", ID: dormant.ID, OrganizationID: owner.OrganizationID}
	if _, err := database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Member: dormantPrincipal, Now: now.Add(15 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SetHumanStatus(context.Background(), SetHumanStatusParams{
		RequestID: uuid.NewString(), Actor: owner, HumanID: dormant.ID, Status: "disabled", Now: now.Add(16 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RemoveMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Member: dormantPrincipal, Now: now.Add(17 * time.Second),
	}); err != nil {
		t.Fatalf("remove disabled human member: %v", err)
	}

	agent, err := database.CreateAgent(context.Background(), CreateAgentParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "membership-agent", Driver: "native", Now: now.Add(18 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ArchiveSpace(context.Background(), ChangeSpaceArchiveParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID, Now: now.Add(19 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: group.ID,
		Member: Principal{Kind: "agent", ID: agent.ID}, Now: now.Add(20 * time.Second),
	}); !errors.Is(err, ErrSpaceArchived) {
		t.Fatalf("archived membership mutation error = %v", err)
	}
	if latest := latestAudit(t, database, owner.OrganizationID); latest.Action != AuditSpaceMemberAdd || latest.Outcome != "denied" || latest.TargetKind != "agent" || latest.TargetID != agent.ID || latest.ContextKind != "space" || latest.ContextID != group.ID {
		t.Fatalf("denied agent membership audit = %+v", latest)
	}
	if _, err := database.GetSpace(context.Background(), SpaceReadParams{Actor: owner, SpaceID: group.ID, Now: now.Add(21 * time.Second)}); err != nil {
		t.Fatalf("archived space read error = %v", err)
	}

	dm, err := database.CreateDM(context.Background(), CreateDMParams{
		RequestID: uuid.NewString(), Actor: owner, Peer: peer, Now: now.Add(22 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: dm.ID,
		Member: Principal{Kind: "agent", ID: agent.ID}, Now: now.Add(23 * time.Second),
	}); !errors.Is(err, ErrDMImmutable) {
		t.Fatalf("dm third-member error = %v", err)
	}
	if _, err := database.ArchiveSpace(context.Background(), ChangeSpaceArchiveParams{
		RequestID: uuid.NewString(), Actor: owner, SpaceID: dm.ID, Now: now.Add(24 * time.Second),
	}); !errors.Is(err, ErrDMImmutable) {
		t.Fatalf("dm archive error = %v", err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: peer,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: dm.ID}, Body: "no grant", Now: now.Add(25 * time.Second),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("no-grant send error = %v", err)
	}
	if latest := latestAudit(t, database, owner.OrganizationID); latest.Action != AuditMessageSend || latest.Outcome != "denied" || latest.ReasonCode != "permission_missing" {
		t.Fatalf("no-grant send audit = %+v", latest)
	}
	bootstrap, err := database.EnsureAuthority(context.Background(), credential, now.Add(26*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	outsiderHuman := createTestHuman(t, database, owner, "Outside Member", "member", "outside-member-credential-abcdefghijklmnopqrstuvwxyz", now.Add(27*time.Second))
	outsider := Principal{Kind: "human", ID: outsiderHuman.ID, OrganizationID: owner.OrganizationID}
	issueTestGrant(t, database, IssueGrantParams{
		RequestID: uuid.NewString(), Actor: owner, Subject: outsider, Capability: CapabilityMessageSend,
		Scope: Scope{Kind: "space", ID: dm.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: now.Add(28 * time.Second),
	})
	if _, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: outsider,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: dm.ID}, Body: "not a member", Now: now.Add(29 * time.Second),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("non-member send error = %v", err)
	}
	issueTestGrant(t, database, IssueGrantParams{
		RequestID: uuid.NewString(), Actor: owner, Subject: peer, Capability: CapabilityMessageSend,
		Scope: Scope{Kind: "space", ID: dm.ID}, ParentGrantID: bootstrap.RootGrant.ID, Now: now.Add(30 * time.Second),
	})
	if _, err := database.SetHumanStatus(context.Background(), SetHumanStatusParams{
		RequestID: uuid.NewString(), Actor: owner, HumanID: peer.ID, Status: "disabled", Now: now.Add(31 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: peer,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: dm.ID}, Body: "disabled", Now: now.Add(32 * time.Second),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("disabled send error = %v", err)
	}
	if latest := latestAudit(t, database, owner.OrganizationID); latest.Action != AuditMessageSend || latest.Outcome != "denied" || latest.ReasonCode != "principal_inactive" {
		t.Fatalf("disabled send audit = %+v", latest)
	}
	var messages int
	if err := database.db.QueryRow(`SELECT count(*) FROM messages WHERE space_id = ?`, dm.ID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 0 {
		t.Fatalf("denied sends persisted %d messages", messages)
	}
	members, err := database.ListMembers(context.Background(), SpaceReadParams{Actor: owner, SpaceID: dm.ID, Now: now.Add(33 * time.Second)})
	if err != nil || len(members) != 2 {
		t.Fatalf("dm membership after denied mutations = %+v, %v", members, err)
	}
}

func openCollaborationFixture(t *testing.T, path string) (*Store, Principal, Principal, string, time.Time) {
	t.Helper()
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 13, 0, 0, 123456789, time.UTC)
	credential := "collaboration-owner-credential-abcdefghijklmnopqrstuvwxyz"
	bootstrap, err := database.EnsureAuthority(context.Background(), credential, now)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	owner := Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	peerHuman := createTestHuman(t, database, owner, "Collaboration Peer", "member", "collaboration-peer-credential-abcdefghijklmnopqrstuvwxyz", now.Add(time.Second))
	peer := Principal{Kind: "human", ID: peerHuman.ID, OrganizationID: bootstrap.Organization.ID}
	return database, owner, peer, credential, now
}

func countAuditAction(t *testing.T, database *Store, organizationID, action, outcome string) int {
	t.Helper()
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE organization_id = ? AND action = ? AND outcome = ?`, organizationID, action, outcome).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func auditEventByTarget(t *testing.T, database *Store, organizationID, action, targetID, outcome string) AuditEvent {
	t.Helper()
	events, err := database.ListAuditEvents(context.Background(), ListAuditEventsParams{OrganizationID: organizationID, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Action == action && event.TargetID == targetID && event.Outcome == outcome {
			return event
		}
	}
	t.Fatalf("audit event not found: action=%q target=%q outcome=%q", action, targetID, outcome)
	return AuditEvent{}
}

func assertSequences(t *testing.T, messages []Message, count int) {
	t.Helper()
	sequences := make([]int, len(messages))
	for index, message := range messages {
		sequences[index] = int(message.TargetSequence)
	}
	sort.Ints(sequences)
	if len(sequences) != count {
		t.Fatalf("sequence count = %d, want %d", len(sequences), count)
	}
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("sequences = %v", sequences)
		}
	}
}
