package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestListWorkAttentionItemsRechecksWorkAndSpaceAccess(t *testing.T) {
	fixture := openWorkQueryFixture(t)
	work := fixture.createWork(t, "approval requires attention", fixture.now.Add(time.Second))
	if _, err := fixture.database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{
		RequestID: uuid.NewString(),
		Actor:     fixture.owner,
		WorkID:    work.ID,
		Question:  "approve the release?",
		Now:       fixture.now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	agentID, _ := fixture.createPlacedAgents(t, 1)
	if _, err := fixture.database.AssignWork(context.Background(), AssignWorkParams{
		RequestID: uuid.NewString(),
		Actor:     fixture.owner,
		WorkID:    work.ID,
		Role:      WorkAssignmentContributor,
		AgentID:   agentID,
		Now:       fixture.now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	inboxID := uuid.NewString()
	if _, err := fixture.database.db.Exec(`
		INSERT INTO inbox_items(
			id, recipient_kind, recipient_id, space_id, target_kind, target_id, trigger_message_id,
			trigger_target_sequence, reason, state, claimed_at, created_at
		) VALUES(?, 'agent', ?, ?, 'space', ?, ?, ?, 'mention', 'claimed', ?, ?)
	`, inboxID, agentID, fixture.space.ID, fixture.space.ID, fixture.message.ID, fixture.message.TargetSequence, unixNano(fixture.now.Add(4*time.Second)), unixNano(fixture.now.Add(4*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.db.Exec(`
		INSERT INTO agent_held_drafts(
			id, agent_id, inbox_item_id, space_id, target_kind, target_id,
			basis_target_sequence, body, held_reason, state, created_at, updated_at
		) VALUES(?, ?, ?, ?, 'space', ?, 1, 'private draft body must not project', 'target_advanced', 'held', ?, ?)
	`, uuid.NewString(), agentID, inboxID, fixture.space.ID, fixture.space.ID, unixNano(fixture.now.Add(5*time.Second)), unixNano(fixture.now.Add(5*time.Second))); err != nil {
		t.Fatal(err)
	}

	ownerItems, err := fixture.database.ListWorkAttentionItems(context.Background(), WorkAttentionQuery{
		Human: fixture.owner,
		Limit: 2,
		Now:   fixture.now.Add(6 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerItems) != 2 {
		t.Fatalf("owner attention = %+v", ownerItems)
	}
	item := ownerItems[0]
	if item.WorkID != work.ID || item.SpaceID != fixture.space.ID || item.Kind != "work_approval" || item.Status != "pending" || item.ReasonCode != "pending_approval" {
		t.Fatalf("owner attention metadata = %+v", item)
	}
	if exception := ownerItems[1]; exception.WorkID != work.ID || exception.SpaceID != fixture.space.ID || exception.AgentID != agentID || exception.Kind != "agent_exception" || exception.Status != "claimed" || exception.ReasonCode != "held_draft" {
		t.Fatalf("owner exception metadata = %+v", exception)
	}

	member := fixture.createMember(t, "attention-member")
	if items, err := fixture.database.ListWorkAttentionItems(context.Background(), WorkAttentionQuery{Human: member, Now: fixture.now.Add(7 * time.Second)}); err != nil || len(items) != 0 {
		t.Fatalf("ungranted member attention = %+v, %v", items, err)
	}
	if _, err := fixture.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID:     uuid.NewString(),
		Actor:         fixture.owner,
		Subject:       member,
		Capability:    CapabilityWorkRead,
		Scope:         Scope{Kind: "work", ID: work.ID},
		ParentGrantID: fixture.rootGrant.ID,
		Now:           fixture.now.Add(8 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID:     uuid.NewString(),
		Actor:         fixture.owner,
		Subject:       member,
		Capability:    CapabilitySpaceRead,
		Scope:         Scope{Kind: "space", ID: fixture.space.ID},
		ParentGrantID: fixture.rootGrant.ID,
		Now:           fixture.now.Add(9 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(),
		Actor:     fixture.owner,
		SpaceID:   fixture.space.ID,
		Member:    member,
		Now:       fixture.now.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	items, err := fixture.database.ListWorkAttentionItems(context.Background(), WorkAttentionQuery{Human: member, Now: fixture.now.Add(11 * time.Second)})
	if err != nil || len(items) != 2 || items[0].WorkID != work.ID || items[1].WorkID != work.ID {
		t.Fatalf("authorized member attention = %+v, %v", items, err)
	}
	if _, err := fixture.database.RemoveMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(),
		Actor:     fixture.owner,
		SpaceID:   fixture.space.ID,
		Member:    member,
		Now:       fixture.now.Add(12 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	items, err = fixture.database.ListWorkAttentionItems(context.Background(), WorkAttentionQuery{Human: member, Now: fixture.now.Add(13 * time.Second)})
	if err != nil || len(items) != 0 {
		t.Fatalf("removed member attention = %+v, %v", items, err)
	}
}
