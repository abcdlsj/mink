package store

import (
	"context"
	"database/sql"
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
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "current source", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID})
	if err != nil || !found {
		t.Fatalf("read knowledge source = %+v, %t, %v", document, found, err)
	}
	projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, 1, document)
	if err != nil || !projected {
		t.Fatalf("apply knowledge projection = %t, %v", projected, err)
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
	document, found, err = database.ReadKnowledgeSourceDocument(context.Background(), KnowledgeSource{Kind: KnowledgeSourceMessage, ID: second.ID})
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
	projected, err = database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, 2, document)
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
	if _, err := database.TransitionWork(context.Background(), TransitionWorkParams{RequestID: uuid.NewString(), Actor: owner, WorkID: work.ID, ToState: WorkStateBlocked, Reason: "waiting", Now: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 2; sequence++ {
		dirty, found, err := database.NextKnowledgeDirtySource(context.Background(), rebuilding.NextGeneration)
		if err != nil || !found || dirty.Sequence != sequence {
			t.Fatalf("next dirty %d = %+v, %t, %v", sequence, dirty, found, err)
		}
		if sequence == 1 {
			document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), dirty.Source)
			if err != nil || !found {
				t.Fatalf("read message document = %+v, %t, %v", document, found, err)
			}
			if projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, sequence, document); err != nil || !projected {
				t.Fatalf("apply message projection = %t, %v", projected, err)
			}
			continue
		}
		stale := KnowledgeSourceDocument{Source: dirty.Source, Revision: dirty.Revision}
		if projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, sequence, stale); err != nil || projected {
			t.Fatalf("apply stale work projection = %t, %v", projected, err)
		}
	}
	progress, err := database.KnowledgeGenerationProgress(context.Background(), rebuilding.NextGeneration)
	if err != nil || progress.AppliedSequence != 2 {
		t.Fatalf("stale work progress = %+v, %v", progress, err)
	}
	var rows int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ? AND source_kind = 'work'`, rebuilding.NextGeneration).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("stale work created projection rows = %d", rows)
	}
}

func TestKnowledgeActivationRequiresExactCurrentHighWater(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge activation", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "before activation", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), rebuilding.NextGeneration); err == nil {
		t.Fatal("activated generation before its current dirty high water")
	}
	document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID})
	if err != nil || !found {
		t.Fatalf("read activation document = %+v, %t, %v", document, found, err)
	}
	if projected, err := database.ApplyKnowledgeProjection(context.Background(), rebuilding.NextGeneration, 1, document); err != nil || !projected {
		t.Fatalf("apply activation projection = %t, %v", projected, err)
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
