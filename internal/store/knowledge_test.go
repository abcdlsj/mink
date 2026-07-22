package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestKnowledgeFTSAvailableAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	available, err := database.KnowledgeFTSAvailable(context.Background())
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if !available {
		database.Close()
		t.Fatal("knowledge FTS is unavailable")
	}
	if _, err := database.db.Exec(`INSERT INTO knowledge_fts(source_kind, source_id, source_version, generation, revision, body) VALUES('message', ?, 0, 1, zeroblob(32), 'knowledge capability probe')`, uuid.NewString()); err != nil {
		database.Close()
		t.Fatal(err)
	}
	var matches int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE knowledge_fts MATCH 'capability'`).Scan(&matches); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if matches != 1 {
		database.Close()
		t.Fatalf("knowledge FTS matches = %d, want 1", matches)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	available, err = reopened.KnowledgeFTSAvailable(context.Background())
	if err != nil || !available {
		t.Fatalf("knowledge FTS after reopen = %t, %v", available, err)
	}
}

func TestKnowledgeDirtySourcesUseGlobalCommittedSequence(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	revision := KnowledgeWorkRevision(KnowledgeWorkFields{Goal: "index"})
	sources := []KnowledgeSource{
		{Kind: KnowledgeSourceMessage, ID: uuid.NewString()},
		{Kind: KnowledgeSourceWork, ID: uuid.NewString()},
		{Kind: KnowledgeSourceArtifactVersion, ID: uuid.NewString(), Version: 1},
	}
	for index, source := range sources {
		entry, err := database.EnqueueKnowledgeDirtySource(context.Background(), source, revision, now.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if entry.Sequence != uint64(index+1) {
			t.Fatalf("sequence = %d, want %d", entry.Sequence, index+1)
		}
	}
}

func TestKnowledgeDirtySourceRollbackLeavesNoVisibleEntry(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = enqueueKnowledgeDirtySource(context.Background(), tx, KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString()}, KnowledgeWorkRevision(KnowledgeWorkFields{Goal: "rollback"}), time.Now())
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("visible dirty sources = %d, want 0", count)
	}
}

func TestKnowledgeArtifactVersionKeysDoNotCollide(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	id := uuid.NewString()
	revision := KnowledgeWorkRevision(KnowledgeWorkFields{Goal: "artifact"})
	first, err := database.EnqueueKnowledgeDirtySource(context.Background(), KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: id, Version: 1}, revision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.EnqueueKnowledgeDirtySource(context.Background(), KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: id, Version: 2}, revision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Source == second.Source || first.Sequence == second.Sequence {
		t.Fatalf("artifact entries collided: %+v / %+v", first, second)
	}
}

func TestKnowledgeWorkRevisionIsStructuredAndOrdered(t *testing.T) {
	base := KnowledgeWorkFields{Goal: "same", AcceptanceCriteria: []string{"one", "two"}, Constraints: []string{"limit"}, BlockingReason: "blocked", Result: "result"}
	cases := []KnowledgeWorkFields{
		{Goal: "", AcceptanceCriteria: []string{"one", "two"}, Constraints: []string{"limit"}, BlockingReason: "same", Result: "result"},
		{Goal: "same", AcceptanceCriteria: []string{"two", "one"}, Constraints: []string{"limit"}, BlockingReason: "blocked", Result: "result"},
		{Goal: "same", AcceptanceCriteria: []string{"one", "two", ""}, Constraints: []string{"limit"}, BlockingReason: "blocked", Result: "result"},
	}
	for _, changed := range cases {
		if KnowledgeWorkRevision(base) == KnowledgeWorkRevision(changed) {
			t.Fatalf("work revision did not change for %+v", changed)
		}
	}
}

func TestKnowledgeSourceRevisionsBindTheirAuthoritativeFields(t *testing.T) {
	messageID := uuid.NewString()
	if KnowledgeMessageRevision(messageID, 1) == KnowledgeMessageRevision(messageID, 2) {
		t.Fatal("message revision ignored target sequence")
	}
	artifactID := uuid.NewString()
	var digest [32]byte
	digest[0] = 1
	if KnowledgeArtifactVersionRevision(artifactID, 1, digest) == KnowledgeArtifactVersionRevision(artifactID, 2, digest) {
		t.Fatal("artifact revision ignored version")
	}
	digest[0] = 2
	if KnowledgeArtifactVersionRevision(artifactID, 2, [32]byte{1}) == KnowledgeArtifactVersionRevision(artifactID, 2, digest) {
		t.Fatal("artifact revision ignored content digest")
	}
}

func TestKnowledgeGenerationRequiresCompletePendingGeneration(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	initial, err := database.KnowledgeIndexMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Status != KnowledgeIndexDegraded || initial.ActiveGeneration != 0 || initial.NextGeneration != 0 {
		t.Fatalf("initial metadata = %+v", initial)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilding.Status != KnowledgeIndexRebuilding || rebuilding.NextGeneration == 0 {
		t.Fatalf("rebuilding metadata = %+v", rebuilding)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err == nil {
		t.Fatal("activated incomplete knowledge generation")
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	ready, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != KnowledgeIndexReady || ready.ActiveGeneration != rebuilding.NextGeneration || ready.NextGeneration != 0 {
		t.Fatalf("ready metadata = %+v", ready)
	}
}

func TestKnowledgeProjectionAcknowledgesOnlyCurrentRevisionAtomically(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "current source", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	progress, err := database.KnowledgeGenerationProgress(context.Background(), rebuilding.NextGeneration)
	if err != nil || progress.AppliedSequence != 1 {
		t.Fatalf("knowledge progress = %+v, %v", progress, err)
	}
	var rows int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, rebuilding.NextGeneration).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("knowledge projection rows = %d, want 1", rows)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER fail_knowledge_ack BEFORE UPDATE OF applied_sequence ON knowledge_generation_progress BEGIN SELECT RAISE(ABORT, 'ack failed'); END`); err != nil {
		t.Fatal(err)
	}
	second, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "rollback source", Now: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), KnowledgeSource{Kind: KnowledgeSourceMessage, ID: second.ID})
	if err != nil || !found {
		t.Fatalf("read rollback source = %+v, %t, %v", document, found, err)
	}
	if _, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, 2, document); err == nil || !strings.Contains(err.Error(), "ack failed") {
		t.Fatalf("apply with failed acknowledgement = %v", err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, rebuilding.NextGeneration).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("failed acknowledgement kept projection row: %d", rows)
	}
	progress, err = database.KnowledgeGenerationProgress(context.Background(), rebuilding.NextGeneration)
	if err != nil || progress.AppliedSequence != 1 {
		t.Fatalf("failed acknowledgement advanced progress = %+v, %v", progress, err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER fail_knowledge_ack`); err != nil {
		t.Fatal(err)
	}
	projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, 2, document)
	if err != nil || !projected {
		t.Fatalf("replay projection after rollback = %t, %v", projected, err)
	}
}

func TestKnowledgeProjectionAcknowledgesStaleWorkReference(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge work", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	work, err := database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: space.ID, SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence, Goal: "index work", AcceptanceCriteria: []string{"indexed"}, Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.TransitionWork(context.Background(), TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateBlocked, Reason: "waiting", Now: now.Add(5 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	approval, err := database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Question: "clear block", Now: now.Add(6 * time.Second)})
	if err != nil || approval.ID == "" {
		t.Fatalf("request approval = %+v, %v", approval, err)
	}
	for sequence := uint64(3); sequence <= 4; sequence++ {
		dirty, found, err := database.NextKnowledgeDirtySource(context.Background(), rebuilding.NextGeneration)
		if err != nil || !found || dirty.Sequence != sequence {
			t.Fatalf("next dirty %d = %+v, %t, %v", sequence, dirty, found, err)
		}
		if sequence == 3 {
			stale := KnowledgeSourceDocument{Source: dirty.Source, Revision: dirty.Revision}
			if projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, sequence, stale); err != nil || projected {
				t.Fatalf("apply stale work projection = %t, %v", projected, err)
			}
			continue
		}
		document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), dirty.Source)
		if err != nil || !found {
			t.Fatalf("read current work document = %+v, %t, %v", document, found, err)
		}
		if projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, sequence, document); err != nil || !projected {
			t.Fatalf("apply current work projection = %t, %v", projected, err)
		}
	}
	progress, err := database.KnowledgeGenerationProgress(context.Background(), rebuilding.NextGeneration)
	if err != nil || progress.AppliedSequence != 4 {
		t.Fatalf("stale work progress = %+v, %v", progress, err)
	}
	var rows int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ? AND source_kind = 'work'`, rebuilding.NextGeneration).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("current work projection rows = %d", rows)
	}
}

func TestKnowledgeActivationRequiresExactCurrentHighWater(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge activation", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "before activation", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err == nil {
		t.Fatal("completed zero snapshot")
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	second, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "after snapshot", Now: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err == nil {
		t.Fatal("completed generation before current dirty high water")
	}
	document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), KnowledgeSource{Kind: KnowledgeSourceMessage, ID: second.ID})
	if err != nil || !found {
		t.Fatalf("read activation document = %+v, %t, %v", document, found, err)
	}
	if projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, 2, document); err != nil || !projected {
		t.Fatalf("apply activation projection = %t, %v", projected, err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	metadata, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration)
	if err != nil || metadata.ActiveGeneration != rebuilding.NextGeneration || metadata.NextGeneration != 0 || metadata.Status != KnowledgeIndexReady {
		t.Fatalf("activate exact high water = %+v, %v", metadata, err)
	}
}

func TestKnowledgeDiscardKeepsHealthyActiveGenerationReady(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Date(2026, 7, 22, 2, 0, 0, 0, time.UTC)
	first, err := database.StartKnowledgeRebuild(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), first.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), first.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), first.NextGeneration); err != nil {
		t.Fatal(err)
	}
	next, err := database.StartKnowledgeRebuild(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := database.DiscardKnowledgeGeneration(context.Background(), next.NextGeneration)
	if err != nil || metadata.ActiveGeneration != first.NextGeneration || metadata.NextGeneration != 0 || metadata.Status != KnowledgeIndexReady {
		t.Fatalf("discard next generation = %+v, %v", metadata, err)
	}
	metadata, err = database.MarkKnowledgeActiveGenerationCorrupt(context.Background(), first.NextGeneration)
	if err != nil || metadata.ActiveGeneration != 0 || metadata.Status != KnowledgeIndexDegraded {
		t.Fatalf("mark active corrupt = %+v, %v", metadata, err)
	}
	metadata, err = database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil || metadata.ActiveGeneration != 0 || metadata.Status != KnowledgeIndexRebuilding {
		t.Fatalf("start fresh rebuild after active corruption = %+v, %v", metadata, err)
	}
}

func TestKnowledgeHooksStayOutOfFTSBusinessTransactions(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge write", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DROP TABLE knowledge_fts`); err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "still authoritative", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatalf("message write depended on FTS: %v", err)
	}
	var dirty int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources WHERE source_kind = 'message' AND source_id = ?`, message.ID).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if dirty != 1 {
		t.Fatalf("message dirty refs = %d, want 1", dirty)
	}
}

func TestKnowledgeWorkHooksTrackCanonicalChangesOnly(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge hooks", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "delegate", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	work, err := database.CreateWork(context.Background(), WorkCreateParams{RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: space.ID, SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence, Goal: "canonical work", AcceptanceCriteria: []string{"complete"}, Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.TransitionWork(context.Background(), TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateBlocked, Reason: "needs approval", Now: now.Add(2500 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	before := knowledgeDirtyCount(t, database, work.ID)
	agent, err := database.CreateAgent(context.Background(), CreateAgentParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge worker", Description: "worker", Driver: "native", Now: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	computerID := uuid.NewString()
	if _, err := database.db.Exec(`INSERT INTO computers(id, registration_key_hash, name, os, arch, created_at, last_seen_at) VALUES(?, zeroblob(32), 'computer', 'linux', 'amd64', ?, ?)`, computerID, unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`INSERT INTO agent_placements(agent_id, computer_id, generation, state, error_code, created_at, updated_at) VALUES(?, ?, 1, 'active', '', ?, ?)`, agent.ID, computerID, unixNano(now), unixNano(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignWork(context.Background(), AssignWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Role: WorkAssignmentContributor, AgentID: agent.ID, Now: now.Add(4 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if after := knowledgeDirtyCount(t, database, work.ID); after != before {
		t.Fatalf("non-canonical assignment enqueued work: %d, want %d", after, before)
	}
	request := RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Question: "approve?", Now: now.Add(5 * time.Second)}
	approval, err := database.RequestWorkApproval(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if after := knowledgeDirtyCount(t, database, work.ID); after != before+1 {
		t.Fatalf("approval request dirty refs = %d, want %d", after, before+1)
	}
	if _, err := database.RequestWorkApproval(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if after := knowledgeDirtyCount(t, database, work.ID); after != before+1 {
		t.Fatalf("approval replay enqueued work: %d, want %d", after, before+1)
	}
	if _, err := database.ResolveWorkApproval(context.Background(), ResolveWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, ApprovalID: approval.ID, Decision: "approved", Now: now.Add(6 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if after := knowledgeDirtyCount(t, database, work.ID); after != before+1 {
		t.Fatalf("non-canonical approval resolution enqueued work: %d, want %d", after, before+1)
	}
	second, err := database.RequestWorkApproval(context.Background(), RequestWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, Question: "reject?", Now: now.Add(7 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ResolveWorkApproval(context.Background(), ResolveWorkApprovalParams{RequestID: uuid.NewString(), Actor: owner, ApprovalID: second.ID, Decision: "rejected", Note: "blocked again", Now: now.Add(8 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if after := knowledgeDirtyCount(t, database, work.ID); after != before+2 {
		t.Fatalf("canonical approval resolution dirty refs = %d, want %d", after, before+2)
	}
}

func TestKnowledgeSnapshotRejectsZeroPartialAndFailedProjection(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "snapshot", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "preexisting", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	zero, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`UPDATE knowledge_generation_progress SET snapshot_high_water = 1, applied_sequence = 1 WHERE generation = ?`, zero.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), zero.NextGeneration); err == nil {
		t.Fatal("completed zero snapshot with a current source")
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), zero.NextGeneration); err == nil {
		t.Fatal("activated zero snapshot with a current source")
	}
	if _, err := database.DiscardKnowledgeGeneration(context.Background(), zero.NextGeneration); err != nil {
		t.Fatal(err)
	}
	failed, err := database.StartKnowledgeRebuild(context.Background(), now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER fail_knowledge_snapshot BEFORE INSERT ON knowledge_projection_rows BEGIN SELECT RAISE(ABORT, 'snapshot failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), failed.NextGeneration); err == nil || !strings.Contains(err.Error(), "snapshot failed") {
		t.Fatalf("failed snapshot = %v", err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER fail_knowledge_snapshot`); err != nil {
		t.Fatal(err)
	}
	progress, err := database.KnowledgeGenerationProgress(context.Background(), failed.NextGeneration)
	if err != nil || progress.SnapshotHighWater != 0 || progress.AppliedSequence != 0 {
		t.Fatalf("failed snapshot progress = %+v, %v", progress, err)
	}
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, failed.NextGeneration).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed snapshot left projection rows = %d", count)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), failed.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`INSERT INTO knowledge_fts(source_kind, source_id, source_version, generation, revision, body) VALUES('message', ?, 0, ?, zeroblob(32), 'orphan')`, uuid.NewString(), failed.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), failed.NextGeneration); err == nil {
		t.Fatal("completed snapshot with orphan FTS row")
	}
	if _, err := database.db.Exec(`DELETE FROM knowledge_fts WHERE generation = ? AND body = 'orphan'`, failed.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DELETE FROM knowledge_projection_rows WHERE generation = ? AND source_id = ?`, failed.NextGeneration, message.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), failed.NextGeneration); err == nil {
		t.Fatal("completed partial snapshot")
	}
}

func TestKnowledgeGenerationRejectsPartialSourceIteration(t *testing.T) {
	for _, source := range []string{KnowledgeSourceMessage, KnowledgeSourceWork, KnowledgeSourceArtifactVersion} {
		for _, operation := range []string{"build", "complete", "activate"} {
			t.Run(source+"/"+operation, func(t *testing.T) {
				fixture := openArtifactFixture(t)
				defer fixture.database.Close()
				if _, err := fixture.artifacts.Publish(context.Background(), fixture.humanPublishParams(uuid.NewString(), "iteration artifact", fixture.at(1))); err != nil {
					t.Fatal(err)
				}
				rebuilding, err := fixture.database.StartKnowledgeRebuild(context.Background(), fixture.at(2))
				if err != nil {
					t.Fatal(err)
				}
				if operation != "build" {
					if err := fixture.database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
						t.Fatal(err)
					}
				}
				if operation == "activate" {
					if err := fixture.database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
						t.Fatal(err)
					}
				}
				contextWithIterationError := context.WithValue(context.Background(), knowledgeSourceDocumentsContextKey{}, knowledgeSourceDocumentsFunc(func(ctx context.Context, queryer knowledgeSourceQueryer) ([]KnowledgeSourceDocument, error) {
					documents, err := listKnowledgeSourceDocuments(ctx, queryer)
					if err != nil {
						return nil, err
					}
					for index, document := range documents {
						if document.Source.Kind == source {
							return documents[:index+1], fmt.Errorf("forced %s iteration error", source)
						}
					}
					return nil, fmt.Errorf("missing %s source for forced iteration error", source)
				}))
				var operationErr error
				switch operation {
				case "build":
					operationErr = fixture.database.BuildKnowledgeGenerationSnapshot(contextWithIterationError, rebuilding.NextGeneration)
				case "complete":
					operationErr = fixture.database.CompleteKnowledgeGeneration(contextWithIterationError, rebuilding.NextGeneration)
				case "activate":
					_, operationErr = fixture.database.ActivateKnowledgeGeneration(contextWithIterationError, rebuilding.NextGeneration)
				}
				if operationErr == nil || !strings.Contains(operationErr.Error(), "forced "+source+" iteration error") {
					t.Fatalf("%s with partial %s iteration = %v", operation, source, operationErr)
				}
				progress, err := fixture.database.KnowledgeGenerationProgress(context.Background(), rebuilding.NextGeneration)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := fixture.database.KnowledgeIndexMetadata(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				var state string
				if err := fixture.database.db.QueryRow(`SELECT state FROM knowledge_index_generations WHERE generation = ?`, rebuilding.NextGeneration).Scan(&state); err != nil {
					t.Fatal(err)
				}
				switch operation {
				case "build":
					if progress.SnapshotHighWater != 0 || progress.AppliedSequence != 0 || state != "building" || metadata.Status != KnowledgeIndexRebuilding || metadata.NextGeneration != rebuilding.NextGeneration {
						t.Fatalf("failed build advanced generation: progress=%+v state=%s metadata=%+v", progress, state, metadata)
					}
					var projections int
					if err := fixture.database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, rebuilding.NextGeneration).Scan(&projections); err != nil {
						t.Fatal(err)
					}
					if projections != 0 {
						t.Fatalf("failed build left projections = %d", projections)
					}
				case "complete":
					if progress.SnapshotHighWater == 0 || progress.AppliedSequence != progress.SnapshotHighWater || state != "building" || metadata.Status != KnowledgeIndexRebuilding || metadata.NextGeneration != rebuilding.NextGeneration {
						t.Fatalf("failed completion advanced generation: progress=%+v state=%s metadata=%+v", progress, state, metadata)
					}
				case "activate":
					if progress.SnapshotHighWater == 0 || progress.AppliedSequence != progress.SnapshotHighWater || state != "complete" || metadata.ActiveGeneration != 0 || metadata.Status != KnowledgeIndexRebuilding || metadata.NextGeneration != rebuilding.NextGeneration {
						t.Fatalf("failed activation advanced generation: progress=%+v state=%s metadata=%+v", progress, state, metadata)
					}
				}
			})
		}
	}
}

func TestKnowledgeSnapshotRebuildsExistingFactsAfterFTSRepair(t *testing.T) {
	fixture := openArtifactFixture(t)
	defer fixture.database.Close()
	published, err := fixture.artifacts.Publish(context.Background(), fixture.humanPublishParams(uuid.NewString(), "artifact content", fixture.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.db.Exec(`DROP TABLE knowledge_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: fixture.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "write survives dropped fts", Now: fixture.at(2)}); err != nil {
		t.Fatalf("authoritative write after FTS drop: %v", err)
	}
	fixture.restart(t)
	if err := fixture.database.RepairKnowledgeFTS(context.Background()); err != nil {
		t.Fatal(err)
	}
	metadata, err := fixture.database.KnowledgeIndexMetadata(context.Background())
	if err != nil || metadata.Status != KnowledgeIndexDegraded || metadata.ActiveGeneration != 0 || metadata.NextGeneration != 0 {
		t.Fatalf("metadata after FTS repair = %+v, %v", metadata, err)
	}
	rebuilding, err := fixture.database.StartKnowledgeRebuild(context.Background(), fixture.at(3))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	metadata, err = fixture.database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration)
	if err != nil || metadata.Status != KnowledgeIndexReady || metadata.ActiveGeneration != rebuilding.NextGeneration {
		t.Fatalf("metadata after repaired rebuild = %+v, %v", metadata, err)
	}
	var exact int
	if err := fixture.database.db.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ? AND source_kind = 'artifact_version' AND source_id = ? AND source_version = ?`, rebuilding.NextGeneration, published.Artifact.ID, published.Version.Version).Scan(&exact); err != nil {
		t.Fatal(err)
	}
	if exact != 1 {
		t.Fatalf("repaired artifact projection = %d, want 1", exact)
	}
}

func TestKnowledgeSnapshotIncludesExistingFactsWithoutDirtyReferences(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "legacy snapshot", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "legacy fact", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DELETE FROM knowledge_dirty_sources`); err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	var projected int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ? AND source_kind = 'message' AND source_id = ?`, rebuilding.NextGeneration, message.ID).Scan(&projected); err != nil {
		t.Fatal(err)
	}
	if projected != 1 {
		t.Fatalf("legacy message projection = %d, want 1", projected)
	}
}

func TestKnowledgeMessageDirtyHookReplaysExactlyAndRollsBack(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "message dirty", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	params := SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "exact dirty", Now: now.Add(time.Second)}
	message, err := database.SendMessage(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	assertKnowledgeDirty(t, database, KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID}, KnowledgeMessageRevision(message.ID, message.TargetSequence), 1)
	if _, err := database.db.Exec(`CREATE TRIGGER fail_message_dirty BEFORE INSERT ON knowledge_dirty_sources BEGIN SELECT RAISE(ABORT, 'message dirty failed'); END`); err != nil {
		t.Fatal(err)
	}
	failed := SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "rollback", Now: now.Add(2 * time.Second)}
	if _, err := database.SendMessage(context.Background(), failed); err == nil || !strings.Contains(err.Error(), "message dirty failed") {
		t.Fatalf("message dirty rollback = %v", err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER fail_message_dirty`); err != nil {
		t.Fatal(err)
	}
	var messages, dirty int
	if err := database.db.QueryRow(`SELECT count(*) FROM messages WHERE request_id = ?`, failed.RequestID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources WHERE source_kind = 'message' AND source_id NOT IN (?)`, message.ID).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || dirty != 0 {
		t.Fatalf("failed message left facts/dirty = %d/%d", messages, dirty)
	}
}

func assertKnowledgeDirty(t *testing.T, database *Store, source KnowledgeSource, revision [sha256.Size]byte, want int) {
	t.Helper()
	var count int
	var actual []byte
	if err := database.db.QueryRow(`SELECT count(*), COALESCE((SELECT revision FROM knowledge_dirty_sources WHERE source_kind = ? AND source_id = ? AND source_version = ? ORDER BY sequence DESC LIMIT 1), zeroblob(32)) FROM knowledge_dirty_sources WHERE source_kind = ? AND source_id = ? AND source_version = ?`, source.Kind, source.ID, source.Version, source.Kind, source.ID, source.Version).Scan(&count, &actual); err != nil {
		t.Fatal(err)
	}
	if count != want || len(actual) != sha256.Size || string(actual) != string(revision[:]) {
		t.Fatalf("knowledge dirty %s/%s/%d = count=%d revision=%x, want count=%d revision=%x", source.Kind, source.ID, source.Version, count, actual, want, revision)
	}
}

func knowledgeDirtyCount(t *testing.T, database *Store, sourceID string) int {
	t.Helper()
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources WHERE source_kind = 'work' AND source_id = ?`, sourceID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestKnowledgeMigrationDownUpRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := configure(database); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := database.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name = 'knowledge_fts')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("knowledge FTS survived migration down")
	}
	if err := goose.Up(database, "migrations"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	available, err := reopened.KnowledgeFTSAvailable(context.Background())
	if err != nil || !available {
		t.Fatalf("knowledge FTS after migration round-trip = %t, %v", available, err)
	}
}

func openKnowledgeStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestKnowledgeSourceValidation(t *testing.T) {
	err := validateKnowledgeSource(KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString(), Version: 1})
	if err == nil {
		t.Fatal("versioned message source was accepted")
	}
}
