package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type knowledgeSearchSQLiteCodeError int

func (err knowledgeSearchSQLiteCodeError) Error() string { return fmt.Sprintf("SQLite code %d", err) }
func (err knowledgeSearchSQLiteCodeError) Code() int     { return int(err) }

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

func TestKnowledgeIndexHealthRepairsMissingFTS(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	if _, err := database.db.Exec(`DROP TABLE knowledge_fts`); err != nil {
		t.Fatal(err)
	}
	health, err := database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexCorrupt {
		t.Fatalf("health after FTS drop = %v, %v", health, err)
	}
	if err := database.RepairKnowledgeFTS(context.Background()); err != nil {
		t.Fatal(err)
	}
	health, err = database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexHealthy {
		t.Fatalf("health after FTS repair = %v, %v", health, err)
	}
}

func TestKnowledgeIndexHealthDetectsInvalidFTSSchema(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	if _, err := database.db.Exec(`DROP TABLE knowledge_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TABLE knowledge_fts(source_kind TEXT)`); err != nil {
		t.Fatal(err)
	}
	health, err := database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexCorrupt {
		t.Fatalf("health after invalid FTS schema = %v, %v", health, err)
	}
}

func TestKnowledgeIndexHealthDetectsActiveProjectionCorruption(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "health", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "health", Now: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	next, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DELETE FROM knowledge_projection_rows WHERE generation = ?`, next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	health, err := database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexCorrupt {
		t.Fatalf("health after projection deletion = %v, %v", health, err)
	}
	if err := database.RepairKnowledgeFTS(context.Background()); err != nil {
		t.Fatal(err)
	}
	metadata, err := database.KnowledgeIndexMetadata(context.Background())
	if err != nil || metadata.ActiveGeneration != 0 || metadata.NextGeneration != 0 || metadata.Status != KnowledgeIndexDegraded {
		t.Fatalf("metadata after projection repair = %+v, %v", metadata, err)
	}
	var projections, ftsRows int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows`).Scan(&projections); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_fts`).Scan(&ftsRows); err != nil {
		t.Fatal(err)
	}
	if projections != 0 || ftsRows != 0 {
		t.Fatalf("projection repair retained derived rows: projections=%d fts=%d", projections, ftsRows)
	}
}

func TestKnowledgeIndexHealthPreservesActiveGenerationOnTransientProjectionRead(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "health transient", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "health transient", Now: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	next, err := database.StartKnowledgeRebuild(context.Background(), now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.BuildKnowledgeGenerationSnapshot(context.Background(), next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteKnowledgeGeneration(context.Background(), next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ActivateKnowledgeGeneration(context.Background(), next.NextGeneration); err != nil {
		t.Fatal(err)
	}
	beforeMetadata, err := database.KnowledgeIndexMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeProgress, err := database.KnowledgeGenerationProgress(context.Background(), next.NextGeneration)
	if err != nil {
		t.Fatal(err)
	}
	var beforeProjections, beforeFTS int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, next.NextGeneration).Scan(&beforeProjections); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ?`, next.NextGeneration).Scan(&beforeFTS); err != nil {
		t.Fatal(err)
	}
	transient := context.WithValue(context.Background(), knowledgeProjectionCheckErrorContextKey{}, knowledgeProjectionCheckErrorFunc(func(stage string) error {
		if stage == "projection row" {
			return context.DeadlineExceeded
		}
		return nil
	}))
	health, err := database.CheckKnowledgeIndexHealth(transient)
	if health != KnowledgeIndexHealthy || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("health after transient projection read = %v, %v", health, err)
	}
	afterMetadata, err := database.KnowledgeIndexMetadata(context.Background())
	if err != nil || afterMetadata != beforeMetadata {
		t.Fatalf("metadata changed after transient projection read: before=%+v after=%+v err=%v", beforeMetadata, afterMetadata, err)
	}
	afterProgress, err := database.KnowledgeGenerationProgress(context.Background(), next.NextGeneration)
	if err != nil || afterProgress != beforeProgress {
		t.Fatalf("progress changed after transient projection read: before=%+v after=%+v err=%v", beforeProgress, afterProgress, err)
	}
	var afterProjections, afterFTS int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_projection_rows WHERE generation = ?`, next.NextGeneration).Scan(&afterProjections); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ?`, next.NextGeneration).Scan(&afterFTS); err != nil {
		t.Fatal(err)
	}
	if afterProjections != beforeProjections || afterFTS != beforeFTS {
		t.Fatalf("derived rows changed after transient projection read: before=%d/%d after=%d/%d", beforeProjections, beforeFTS, afterProjections, afterFTS)
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
				contextWithIterationError := context.WithValue(context.Background(), knowledgeRowsErrorContextKey{}, knowledgeRowsErrorFunc(func(sourceKind string, rows *sql.Rows) error {
					if sourceKind == source {
						return fmt.Errorf("forced %s iteration error", source)
					}
					return rows.Err()
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

func openKnowledgeStore(t *testing.T) *Store {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestSearchKnowledgeReturnsCurrentAuthorizedSourcesAndQuietlyDropsRawFTSHits(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge search", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "current searchable phrase", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	generation := activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "searchable phrase", Now: now.Add(3 * time.Second)})
	if err != nil || page.Status != KnowledgeIndexReady || len(page.Results) != 1 || page.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID}) || page.Results[0].Snippet != message.Body {
		t.Fatalf("authorized search = %+v, %v", page, err)
	}
	if _, err := database.db.Exec(`INSERT INTO knowledge_fts(source_kind, source_id, source_version, generation, revision, body) VALUES('message', ?, 0, ?, zeroblob(32), 'raw fts secret')`, uuid.NewString(), generation); err != nil {
		t.Fatal(err)
	}
	page, err = database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "secret", Now: now.Add(4 * time.Second)})
	if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("raw FTS hit leaked = %+v, %v", page, err)
	}
}

func TestSearchKnowledgeRejectsQueryAndPaginationBudgetViolations(t *testing.T) {
	operators, err := normalizeKnowledgeSearchQuery("OR * NEAR")
	if err != nil || operators.match != `"OR" AND "*" AND "NEAR"` {
		t.Fatalf("operator literal query = %+v, %v", operators, err)
	}
	for name, test := range map[string]struct {
		query string
		valid bool
	}{
		"empty":          {query: ""},
		"query max":      {query: strings.Join([]string{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 61)}, " "), valid: true},
		"query max plus": {query: strings.Repeat("a", knowledgeSearchQueryMaxBytes+1)},
		"terms max":      {query: strings.TrimSpace(strings.Repeat("a ", knowledgeSearchTermsMax)), valid: true},
		"terms max plus": {query: strings.TrimSpace(strings.Repeat("a ", knowledgeSearchTermsMax+1))},
		"term max":       {query: strings.Repeat("a", knowledgeSearchTermMaxBytes), valid: true},
		"term max plus":  {query: strings.Repeat("a", knowledgeSearchTermMaxBytes+1)},
		"invalid UTF-8":  {query: string([]byte{0xff})},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeKnowledgeSearchQuery(test.query)
			if test.valid && err != nil {
				t.Fatalf("normalize %q = %v", name, err)
			}
			if !test.valid && !errors.Is(err, ErrKnowledgeSearchInvalid) {
				t.Fatalf("normalize %q error = %v", name, err)
			}
		})
	}
	database := openKnowledgeStore(t)
	defer database.Close()
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: Principal{Kind: "human", ID: "human", OrganizationID: "organization"}, Query: "valid", Limit: knowledgeSearchMaxPageSize, Now: time.Now()}); errors.Is(err, ErrKnowledgeSearchInvalid) {
		t.Fatalf("max limit rejected: %v", err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: Principal{Kind: "human", ID: "human", OrganizationID: "organization"}, Query: "valid", Limit: knowledgeSearchMaxPageSize + 1, Now: time.Now()}); !errors.Is(err, ErrKnowledgeSearchInvalid) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestSearchKnowledgeLiteralPagingCursorAndStatusContracts(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge paging", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{}{}
	for _, body := range []string{"literal OR NEAR one", "literal OR NEAR two", "literal OR NEAR three"} {
		message, err := database.SendMessage(context.Background(), SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: body, Now: now.Add(time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		want[message.ID] = struct{}{}
	}
	activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))

	seen := map[string]struct{}{}
	cursor := ""
	for range len(want) {
		page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "OR NEAR", Cursor: cursor, Limit: 1, Now: now.Add(3 * time.Second)})
		if err != nil || len(page.Results) != 1 || page.Status != KnowledgeIndexReady {
			t.Fatalf("paged literal search = %+v, %v", page, err)
		}
		id := page.Results[0].Source.ID
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("duplicate paged source %s", id)
		}
		seen[id] = struct{}{}
		cursor = page.NextCursor
	}
	if len(seen) != len(want) || cursor != "" {
		t.Fatalf("paged sources/cursor = %v/%q, want %v/empty", seen, cursor, want)
	}
	first, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "OR NEAR", Limit: 1, Now: now.Add(4 * time.Second)})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first cursor = %+v, %v", first, err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "different", Cursor: first.NextCursor, Limit: 1, Now: now.Add(4 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchCursorUnavailable) {
		t.Fatalf("query-bound cursor error = %v", err)
	}
	peer, err := database.CreateHuman(context.Background(), CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Cursor Peer", Role: "member",
		Credential: "cursor-peer-credential-abcdefghijklmnopqrstuvwxyz", Now: now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: Principal{Kind: "human", ID: peer.ID, OrganizationID: owner.OrganizationID}, Query: "OR NEAR", Cursor: first.NextCursor, Limit: 1, Now: now.Add(4 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchCursorUnavailable) {
		t.Fatalf("principal-bound cursor error = %v", err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "OR NEAR", Cursor: first.NextCursor + "x", Limit: 1, Now: now.Add(4 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchCursorUnavailable) {
		t.Fatalf("tampered cursor error = %v", err)
	}
	activateKnowledgeSearchGeneration(t, database, now.Add(5*time.Second))
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "OR NEAR", Cursor: first.NextCursor, Limit: 1, Now: now.Add(6 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchCursorUnavailable) {
		t.Fatalf("generation-bound cursor error = %v", err)
	}
	if _, err := database.db.Exec(`UPDATE knowledge_index_metadata SET status = 'degraded', active_generation = 0 WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "OR", Now: now.Add(5 * time.Second)})
	if err != nil || page.Status != KnowledgeIndexDegraded || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("degraded search = %+v, %v", page, err)
	}
}

func TestSearchKnowledgeRechecksCurrentRuntimeAndSpaceAccess(t *testing.T) {
	fixture := openDeliveryFixture(t)
	message, err := fixture.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "runtime searchable", Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(2))
	page, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "runtime", Now: fixture.at(3)})
	if err != nil || len(page.Results) != 1 || page.Results[0].Source.ID != message.ID {
		t.Fatalf("runtime search = %+v, %v", page, err)
	}
	if _, err := fixture.database.RemoveMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, SpaceID: fixture.group.ID, Member: Principal{Kind: "agent", ID: fixture.agentID}, Now: fixture.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	page, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "runtime", Now: fixture.at(5)})
	if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("removed member search = %+v, %v", page, err)
	}
	stale := fixture.authentication
	rotated := rotateDeliveryRuntime(t, fixture, 91, fixture.at(6))
	if _, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: stale, Query: "runtime", Now: fixture.at(8)}); !errors.Is(err, ErrKnowledgeSearchUnauthenticated) {
		t.Fatalf("stale runtime error = %v", err)
	}
	if _, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: rotated, Query: "runtime", Now: fixture.at(8)}); err != nil {
		t.Fatalf("current runtime search = %v", err)
	}
}

func TestSearchKnowledgeUsesAllCurrentSourceKindsAndWritesNothing(t *testing.T) {
	fixture := openArtifactFixture(t)
	published, err := fixture.artifacts.Publish(context.Background(), fixture.humanPublishParams(uuid.NewString(), "artifact search content", fixture.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(2))
	before := knowledgeSearchMutationSnapshot(t, fixture.database)
	page, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: fixture.owner, Query: "artifact", Now: fixture.at(3)})
	if err != nil {
		t.Fatal(err)
	}
	if after := knowledgeSearchMutationSnapshot(t, fixture.database); after != before {
		t.Fatalf("search mutated facts: before=%+v after=%+v", before, after)
	}
	found := map[KnowledgeSource]KnowledgeSearchResult{}
	for _, result := range page.Results {
		found[result.Source] = result
	}
	for _, source := range []KnowledgeSource{
		{Kind: KnowledgeSourceMessage, ID: fixture.source.ID},
		{Kind: KnowledgeSourceWork, ID: fixture.work.ID},
		{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version},
	} {
		if _, ok := found[source]; !ok {
			t.Fatalf("search omitted current source %+v: %+v", source, page)
		}
	}
	if got := found[KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version}].Snippet; !utf8.ValidString(got) || len(got) > knowledgeSearchSnippetMaxBytes {
		t.Fatalf("artifact snippet = %q", got)
	}
}

func TestSearchKnowledgeStopsAtHiddenCandidateWindowWithoutCursor(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	generation := activateKnowledgeSearchGeneration(t, database, now)
	for index := range knowledgeSearchCandidateLimit {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
		if _, err := database.db.Exec(`INSERT INTO knowledge_fts(source_kind, source_id, source_version, generation, revision, body) VALUES('message', ?, 0, ?, zeroblob(32), 'hidden candidate')`, id, generation); err != nil {
			t.Fatal(err)
		}
	}
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "hidden", Now: now.Add(time.Second)})
	if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("hidden candidate window = %+v, %v", page, err)
	}
}

func TestSearchKnowledgeFailsClosedForCorruptFTS(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	activateKnowledgeSearchGeneration(t, database, now)
	if _, err := database.db.Exec(`DROP TABLE knowledge_fts`); err != nil {
		t.Fatal(err)
	}
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "anything", Now: now.Add(time.Second)})
	if err != nil || page.Status != KnowledgeIndexDegraded || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("corrupt FTS search = %+v, %v", page, err)
	}
}

func TestKnowledgeSearchPropagatesBackendReadsButQuietlyDropsMissingFacts(t *testing.T) {
	fixture := openDeliveryFixture(t)
	message, err := fixture.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "backend source", Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	generation := activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(2))
	tx, err := fixture.database.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := knowledgeSearchAuthentication(canceled, tx, KnowledgeSearchParams{Human: fixture.owner}, fixture.at(3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("human backend authentication error = %v", err)
	}
	if _, _, err := knowledgeSearchAuthentication(canceled, tx, KnowledgeSearchParams{Agent: fixture.authentication}, fixture.at(3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime backend authentication error = %v", err)
	}
	candidate := knowledgeSearchCandidate{seek: KnowledgeCursorSeekKey{Rank: 0, SourceKind: KnowledgeSourceMessage, SourceID: message.ID, RowID: 1}}
	if _, _, err := currentKnowledgeSearchDocument(canceled, tx, fixture.owner, AgentRuntimeAuthentication{}, generation, candidate, fixture.at(3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("projection backend error = %v", err)
	}
	if _, err := knowledgeSearchSourceReadable(canceled, tx, fixture.owner, fixture.authentication, KnowledgeSourceDocument{Source: KnowledgeSource{Kind: KnowledgeSourceWork, ID: uuid.NewString()}}, fixture.at(3)); !errors.Is(err, context.Canceled) {
		t.Fatalf("work backend error = %v", err)
	}
}

func TestSearchKnowledgeDegradesDeterministicCorruptionAndPropagatesTransient(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "search corruption", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "corruptible search", Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	generation := activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))
	if _, err := database.db.Exec(`UPDATE knowledge_projection_rows SET fts_rowid = fts_rowid + 1 WHERE generation = ? AND source_kind = ? AND source_id = ?`, generation, KnowledgeSourceMessage, message.ID); err != nil {
		t.Fatal(err)
	}
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "corruptible", Now: now.Add(3 * time.Second)})
	if err != nil || page.Status != KnowledgeIndexDegraded || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("projection corruption search = %+v, %v", page, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := database.SearchKnowledge(canceled, KnowledgeSearchParams{Human: owner, Query: "corruptible", Now: now.Add(4 * time.Second)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("transient search error = %v", err)
	}
}

func TestSearchKnowledgeCandidateStagesClassifyCorruptionAndTransient(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "candidate failures", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "candidate failure", Now: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))
	for _, stage := range []string{"query", "scan", "iterate"} {
		t.Run(stage+" corruption", func(t *testing.T) {
			ctx := context.WithValue(context.Background(), knowledgeSearchCandidateFaultContextKey{}, knowledgeSearchCandidateFaultFunc(func(actual string, _ *knowledgeSearchCandidate, err error) error {
				if actual == stage {
					return errKnowledgeSearchCorrupt
				}
				return err
			}))
			page, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "candidate", Now: now.Add(3 * time.Second)})
			if err != nil || page.Status != KnowledgeIndexDegraded || len(page.Results) != 0 || page.NextCursor != "" {
				t.Fatalf("%s corruption = %+v, %v", stage, page, err)
			}
		})
		t.Run(stage+" transient", func(t *testing.T) {
			ctx := context.WithValue(context.Background(), knowledgeSearchCandidateFaultContextKey{}, knowledgeSearchCandidateFaultFunc(func(actual string, _ *knowledgeSearchCandidate, err error) error {
				if actual == stage {
					return context.Canceled
				}
				return err
			}))
			if _, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "candidate", Now: now.Add(3 * time.Second)}); !errors.Is(err, context.Canceled) {
				t.Fatalf("%s transient error = %v", stage, err)
			}
		})
		for _, code := range []int{sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_CORRUPT_VTAB, sqlite3.SQLITE_NOTADB} {
			t.Run(fmt.Sprintf("%s SQLite corruption %d", stage, code), func(t *testing.T) {
				injected := knowledgeSearchCandidateSQLiteFault(code)
				ctx := context.WithValue(context.Background(), knowledgeSearchCandidateFaultContextKey{}, knowledgeSearchCandidateFaultFunc(func(actual string, _ *knowledgeSearchCandidate, err error) error {
					if actual == stage {
						return injected
					}
					return err
				}))
				page, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "candidate", Now: now.Add(3 * time.Second)})
				if err != nil || page.Status != KnowledgeIndexDegraded || len(page.Results) != 0 || page.NextCursor != "" {
					t.Fatalf("%s SQLite corruption %d = %+v, %v", stage, code, page, err)
				}
			})
		}
		for _, code := range []int{sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED, sqlite3.SQLITE_INTERRUPT, sqlite3.SQLITE_ERROR} {
			t.Run(fmt.Sprintf("%s SQLite transient %d", stage, code), func(t *testing.T) {
				injected := knowledgeSearchCandidateSQLiteFault(code)
				ctx := context.WithValue(context.Background(), knowledgeSearchCandidateFaultContextKey{}, knowledgeSearchCandidateFaultFunc(func(actual string, _ *knowledgeSearchCandidate, err error) error {
					if actual == stage {
						return injected
					}
					return err
				}))
				_, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "candidate", Now: now.Add(3 * time.Second)})
				var propagated knowledgeSearchCandidateSQLiteFault
				if !errors.As(err, &propagated) || propagated != injected {
					t.Fatalf("%s SQLite transient %d error = %v", stage, code, err)
				}
			})
		}
		for _, code := range []int{sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB, sqlite3.SQLITE_CORRUPT_VTAB} {
			t.Run(fmt.Sprintf("%s non-SQLite coded backend %d", stage, code), func(t *testing.T) {
				injected := knowledgeSearchSQLiteCodeError(code)
				ctx := context.WithValue(context.Background(), knowledgeSearchCandidateFaultContextKey{}, knowledgeSearchCandidateFaultFunc(func(actual string, _ *knowledgeSearchCandidate, err error) error {
					if actual == stage {
						return injected
					}
					return err
				}))
				_, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "candidate", Now: now.Add(3 * time.Second)})
				var propagated knowledgeSearchSQLiteCodeError
				if !errors.As(err, &propagated) || propagated != injected {
					t.Fatalf("%s non-SQLite coded backend %d error = %v", stage, code, err)
				}
			})
		}
	}
	for _, rank := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprintf("rank %v", rank), func(t *testing.T) {
			ctx := context.WithValue(context.Background(), knowledgeSearchCandidateFaultContextKey{}, knowledgeSearchCandidateFaultFunc(func(stage string, candidate *knowledgeSearchCandidate, err error) error {
				if stage == "scan" {
					candidate.seek.Rank = rank
				}
				return err
			}))
			page, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "candidate", Now: now.Add(4 * time.Second)})
			if err != nil || page.Status != KnowledgeIndexDegraded || len(page.Results) != 0 || page.NextCursor != "" {
				t.Fatalf("rank %v search = %+v, %v", rank, page, err)
			}
		})
	}
}

func TestKnowledgeSearchSQLiteCorruptionCodeAllowlist(t *testing.T) {
	for code, want := range map[int]bool{
		sqlite3.SQLITE_CORRUPT:      true,
		sqlite3.SQLITE_CORRUPT_VTAB: true,
		sqlite3.SQLITE_NOTADB:       true,
		sqlite3.SQLITE_BUSY:         false,
		sqlite3.SQLITE_LOCKED:       false,
		sqlite3.SQLITE_INTERRUPT:    false,
		sqlite3.SQLITE_ERROR:        false,
	} {
		if got := isKnowledgeSearchSQLiteCorruptionCode(code); got != want {
			t.Fatalf("SQLite code %d corruption = %t, want %t", code, got, want)
		}
	}
}

func TestSearchKnowledgeQuietlyDropsStaleCurrentSourceRevisions(t *testing.T) {
	for _, test := range []struct {
		name   string
		query  string
		source func(*artifactFixture, PublishArtifactResult) KnowledgeSource
	}{
		{name: "message", query: "source", source: func(fixture *artifactFixture, _ PublishArtifactResult) KnowledgeSource {
			return KnowledgeSource{Kind: KnowledgeSourceMessage, ID: fixture.source.ID}
		}},
		{name: "work", query: "produce", source: func(fixture *artifactFixture, _ PublishArtifactResult) KnowledgeSource {
			return KnowledgeSource{Kind: KnowledgeSourceWork, ID: fixture.work.ID}
		}},
		{name: "artifact", query: "revision-only", source: func(_ *artifactFixture, published PublishArtifactResult) KnowledgeSource {
			return KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := openArtifactFixture(t)
			publish := fixture.humanPublishParams(uuid.NewString(), "stale artifact body", fixture.at(1))
			publish.Summary = "revision-only artifact"
			published, err := fixture.artifacts.Publish(context.Background(), publish)
			if err != nil {
				t.Fatal(err)
			}
			generation := activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(2))
			source := test.source(fixture, published)
			currentBefore, found, err := readKnowledgeSourceDocument(context.Background(), fixture.database.db, source)
			if err != nil || !found {
				t.Fatalf("read current %s before mutation = %+v, %t, %v", test.name, currentBefore, found, err)
			}
			var projectionBefore, ftsBefore []byte
			if err := fixture.database.db.QueryRow(`
				SELECT p.revision, f.revision
				FROM knowledge_projection_rows p JOIN knowledge_fts f ON f.rowid = p.fts_rowid
				WHERE p.generation = ? AND p.source_kind = ? AND p.source_id = ? AND p.source_version = ?
			`, generation, source.Kind, source.ID, source.Version).Scan(&projectionBefore, &ftsBefore); err != nil {
				t.Fatal(err)
			}
			switch source.Kind {
			case KnowledgeSourceMessage:
				if _, err = fixture.database.db.Exec(`PRAGMA foreign_keys = OFF`); err == nil {
					_, err = fixture.database.db.Exec(`UPDATE messages SET target_sequence = target_sequence + 100 WHERE id = ?`, source.ID)
				}
			case KnowledgeSourceWork:
				_, err = fixture.database.TransitionWork(context.Background(), TransitionWorkParams{
					RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: source.ID,
					ToState: WorkStateBlocked, Reason: "current revision changed", Now: fixture.at(3),
				})
			case KnowledgeSourceArtifactVersion:
				if _, err = fixture.database.db.Exec(`DROP TRIGGER artifact_versions_immutable_update`); err == nil {
					_, err = fixture.database.db.Exec(`INSERT OR IGNORE INTO artifact_blobs(digest, size, integrity_state, created_at, checked_at) SELECT zeroblob(32), size, 'ready', created_at, created_at FROM artifact_versions WHERE artifact_id = ? AND version = ?`, source.ID, source.Version)
					if err == nil {
						_, err = fixture.database.db.Exec(`UPDATE artifact_versions SET digest = zeroblob(32) WHERE artifact_id = ? AND version = ?`, source.ID, source.Version)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			currentAfter, found, err := readKnowledgeSourceDocument(context.Background(), fixture.database.db, source)
			if err != nil || !found {
				t.Fatalf("read current %s after mutation = %+v, %t, %v", test.name, currentAfter, found, err)
			}
			if currentAfter.Revision == currentBefore.Revision {
				t.Fatalf("current %s revision did not change", test.name)
			}
			var projectionAfter, ftsAfter []byte
			if err := fixture.database.db.QueryRow(`
				SELECT p.revision, f.revision
				FROM knowledge_projection_rows p JOIN knowledge_fts f ON f.rowid = p.fts_rowid
				WHERE p.generation = ? AND p.source_kind = ? AND p.source_id = ? AND p.source_version = ?
			`, generation, source.Kind, source.ID, source.Version).Scan(&projectionAfter, &ftsAfter); err != nil {
				t.Fatal(err)
			}
			if string(projectionAfter) != string(projectionBefore) || string(ftsAfter) != string(ftsBefore) {
				t.Fatal("current source mutation changed indexed revisions")
			}
			page, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: fixture.owner, Query: test.query, Now: fixture.at(4)})
			if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
				t.Fatalf("stale %s search = %+v, %v", test.name, page, err)
			}
		})
	}
}

func TestSearchKnowledgeQuietlyDropsRawIndexRevisionMismatches(t *testing.T) {
	for _, test := range []struct {
		name   string
		query  string
		source func(*artifactFixture, PublishArtifactResult) KnowledgeSource
	}{
		{name: "message", query: "source", source: func(fixture *artifactFixture, _ PublishArtifactResult) KnowledgeSource {
			return KnowledgeSource{Kind: KnowledgeSourceMessage, ID: fixture.source.ID}
		}},
		{name: "work", query: "produce", source: func(fixture *artifactFixture, _ PublishArtifactResult) KnowledgeSource {
			return KnowledgeSource{Kind: KnowledgeSourceWork, ID: fixture.work.ID}
		}},
		{name: "artifact", query: "revision-only", source: func(_ *artifactFixture, published PublishArtifactResult) KnowledgeSource {
			return KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := openArtifactFixture(t)
			publish := fixture.humanPublishParams(uuid.NewString(), "stale artifact body", fixture.at(1))
			publish.Summary = "revision-only artifact"
			published, err := fixture.artifacts.Publish(context.Background(), publish)
			if err != nil {
				t.Fatal(err)
			}
			generation := activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(2))
			source := test.source(fixture, published)
			for _, table := range []string{"knowledge_fts", "knowledge_projection_rows"} {
				if _, err := fixture.database.db.Exec(`UPDATE `+table+` SET revision = zeroblob(32) WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = ?`, generation, source.Kind, source.ID, source.Version); err != nil {
					t.Fatal(err)
				}
			}
			page, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: fixture.owner, Query: test.query, Now: fixture.at(3)})
			if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
				t.Fatalf("raw-index %s mismatch search = %+v, %v", test.name, page, err)
			}
		})
	}
}

func TestSearchKnowledgePaginationStaysQuietAcrossStateChangesAndReopen(t *testing.T) {
	t.Run("revoke leaves a short page without cursor", func(t *testing.T) {
		fixture := openDeliveryFixture(t)
		for range 2 {
			if _, err := fixture.database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: fixture.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "page revoke", Now: fixture.at(1)}); err != nil {
				t.Fatal(err)
			}
		}
		activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(2))
		first, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "page revoke", Limit: 1, Now: fixture.at(3)})
		if err != nil || len(first.Results) != 1 || first.NextCursor == "" {
			t.Fatalf("first revoke page = %+v, %v", first, err)
		}
		if _, err := fixture.database.RemoveMember(context.Background(), ChangeMemberParams{RequestID: uuid.NewString(), Actor: fixture.owner, SpaceID: fixture.group.ID, Member: Principal{Kind: "agent", ID: fixture.agentID}, Now: fixture.at(4)}); err != nil {
			t.Fatal(err)
		}
		second, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "page revoke", Cursor: first.NextCursor, Limit: 1, Now: fixture.at(5)})
		if err != nil || len(second.Results) != 0 || second.NextCursor != "" {
			t.Fatalf("revoked second page = %+v, %v", second, err)
		}
	})
	t.Run("stale leaves a short page without cursor", func(t *testing.T) {
		database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
		defer database.Close()
		space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "page stale", Now: now})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, 2)
		for range 2 {
			message, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "page stale", Now: now.Add(time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, message.ID)
		}
		generation := activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))
		first, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "page stale", Limit: 1, Now: now.Add(3 * time.Second)})
		if err != nil || len(first.Results) != 1 || first.NextCursor == "" {
			t.Fatalf("first stale page = %+v, %v", first, err)
		}
		staleID := ids[0]
		if staleID == first.Results[0].Source.ID {
			staleID = ids[1]
		}
		for _, table := range []string{"knowledge_fts", "knowledge_projection_rows"} {
			if _, err := database.db.Exec(`UPDATE `+table+` SET revision = zeroblob(32) WHERE generation = ? AND source_kind = 'message' AND source_id = ?`, generation, staleID); err != nil {
				t.Fatal(err)
			}
		}
		second, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "page stale", Cursor: first.NextCursor, Limit: 1, Now: now.Add(4 * time.Second)})
		if err != nil || len(second.Results) != 0 || second.NextCursor != "" {
			t.Fatalf("stale second page = %+v, %v", second, err)
		}
	})
	t.Run("same database reopen continues a Search cursor", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "server.db")
		database, owner, _, _, now := openCollaborationFixture(t, path)
		space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "page reopen", Now: now})
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "page reopen", Now: now.Add(time.Second)}); err != nil {
				database.Close()
				t.Fatal(err)
			}
		}
		activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))
		first, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "page reopen", Limit: 1, Now: now.Add(3 * time.Second)})
		if err != nil || len(first.Results) != 1 || first.NextCursor == "" {
			database.Close()
			t.Fatalf("first reopen page = %+v, %v", first, err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		second, err := reopened.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "page reopen", Cursor: first.NextCursor, Limit: 1, Now: now.Add(4 * time.Second)})
		if err != nil || len(second.Results) != 1 || second.Results[0].Source.ID == first.Results[0].Source.ID || second.NextCursor != "" {
			t.Fatalf("reopened second page = %+v, %v", second, err)
		}
	})
}

func TestSearchKnowledgeSnippetAggregateStatusAndCursorBudgets(t *testing.T) {
	if snippet, ok := knowledgeSearchSnippet(string([]byte{0xff})); ok || snippet != "" {
		t.Fatalf("invalid UTF-8 snippet = %q, %t", snippet, ok)
	}
	body := strings.Repeat("界", 170) + "a界"
	if snippet, ok := knowledgeSearchSnippet(body); !ok || len(snippet) != knowledgeSearchSnippetMaxBytes-1 || !utf8.ValidString(snippet) {
		t.Fatalf("rune-boundary snippet = %q, %t, %d", snippet, ok, len(snippet))
	}

	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "search budgets", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for range 33 {
		if _, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: body + " aggregate", Now: now.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	generation := activateKnowledgeSearchGeneration(t, database, now.Add(2*time.Second))
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "aggregate", Limit: knowledgeSearchMaxPageSize, Now: now.Add(3 * time.Second)})
	if err != nil || len(page.Results) != knowledgeSearchAggregateMaxBytes/knowledgeSearchSnippetMaxBytes || page.NextCursor == "" {
		t.Fatalf("aggregate search = %+v, %v", page, err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "aggregate", Cursor: strings.Repeat("x", knowledgeCursorTokenMax+1), Now: now.Add(3 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchCursorUnavailable) {
		t.Fatalf("oversize cursor error = %v", err)
	}
	if _, err := database.db.Exec(`UPDATE knowledge_index_metadata SET status = 'rebuilding' WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	page, err = database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "aggregate", Now: now.Add(4 * time.Second)})
	if err != nil || page.Status != KnowledgeIndexRebuilding || len(page.Results) == 0 {
		t.Fatalf("rebuilding active search = %+v, %v", page, err)
	}
	if _, err := database.db.Exec(`UPDATE knowledge_index_metadata SET active_generation = 0 WHERE singleton = 1`); err != nil {
		t.Fatal(err)
	}
	page, err = database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "aggregate", Now: now.Add(5 * time.Second)})
	if err != nil || page.Status != KnowledgeIndexRebuilding || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("rebuilding no-active search = %+v, %v", page, err)
	}
	_ = generation
}

func TestSearchKnowledgeAggregateOverflowResumesWithoutOmission(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "aggregate paging", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	lengths := append(make([]int, 31), 500, 20, 10)
	for index := range 31 {
		lengths[index] = knowledgeSearchSnippetMaxBytes
	}
	messages := make([]Message, 0, len(lengths))
	for index, length := range lengths {
		prefix := "aggregate "
		message, err := database.SendMessage(context.Background(), SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID},
			Body: prefix + strings.Repeat("x", length-len(prefix)), Now: now.Add(time.Duration(index+1) * time.Millisecond),
		})
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, message)
	}
	generation := activateKnowledgeSearchGeneration(t, database, now.Add(time.Second))
	candidates := make([]knowledgeSearchCandidate, 0, len(messages))
	for index, message := range messages {
		var candidate knowledgeSearchCandidate
		var revision []byte
		if err := database.db.QueryRow(`
			SELECT fts_rowid, revision FROM knowledge_projection_rows
			WHERE generation = ? AND source_kind = ? AND source_id = ? AND source_version = 0
		`, generation, KnowledgeSourceMessage, message.ID).Scan(&candidate.seek.RowID, &revision); err != nil {
			t.Fatal(err)
		}
		candidate.seek.Rank = float64(index + 1)
		candidate.seek.SourceKind = KnowledgeSourceMessage
		candidate.seek.SourceID = message.ID
		copy(candidate.revision[:], revision)
		candidates = append(candidates, candidate)
	}
	ctx := context.WithValue(context.Background(), knowledgeSearchCandidateOverrideContextKey{}, knowledgeSearchCandidateOverrideFunc(func(actualGeneration uint64) []knowledgeSearchCandidate {
		if actualGeneration != generation {
			t.Fatalf("override generation = %d, want %d", actualGeneration, generation)
		}
		return candidates
	}))
	first, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "aggregate", Limit: knowledgeSearchMaxPageSize, Now: now.Add(2 * time.Second)})
	if err != nil || len(first.Results) != 32 || first.NextCursor == "" {
		t.Fatalf("aggregate first page = %d results, cursor=%t, %v", len(first.Results), first.NextCursor != "", err)
	}
	for index, result := range first.Results {
		if result.Source.ID != messages[index].ID || len(result.Snippet) != lengths[index] {
			t.Fatalf("aggregate first page result %d = %s/%dB, want %s/%dB", index, result.Source.ID, len(result.Snippet), messages[index].ID, lengths[index])
		}
	}
	binding := KnowledgeCursorBinding{
		PrincipalFingerprint: knowledgeSearchPrincipalFingerprint(owner),
		QueryHash:            sha256.Sum256([]byte("aggregate")),
		Generation:           generation,
	}
	seek, err := database.OpenKnowledgeCursor(first.NextCursor, binding)
	if err != nil || seek != candidates[31].seek {
		t.Fatalf("aggregate first cursor = %+v, %v; want %+v", seek, err, candidates[31].seek)
	}
	second, err := database.SearchKnowledge(ctx, KnowledgeSearchParams{Human: owner, Query: "aggregate", Cursor: first.NextCursor, Limit: knowledgeSearchMaxPageSize, Now: now.Add(3 * time.Second)})
	if err != nil || len(second.Results) != 2 || second.NextCursor != "" {
		t.Fatalf("aggregate second page = %d results, cursor=%q, %v", len(second.Results), second.NextCursor, err)
	}
	if second.Results[0].Source.ID != messages[32].ID || len(second.Results[0].Snippet) != lengths[32] || second.Results[1].Source.ID != messages[33].ID || len(second.Results[1].Snippet) != lengths[33] {
		t.Fatalf("aggregate second page = %+v, want %s then %s", second.Results, messages[32].ID, messages[33].ID)
	}
	seen := make(map[string]struct{}, len(messages))
	for _, page := range []KnowledgeSearchPage{first, second} {
		for _, result := range page.Results {
			if _, duplicate := seen[result.Source.ID]; duplicate {
				t.Fatalf("aggregate paging duplicated %s", result.Source.ID)
			}
			seen[result.Source.ID] = struct{}{}
		}
	}
	if len(seen) != len(messages) {
		t.Fatalf("aggregate paging returned %d sources, want %d", len(seen), len(messages))
	}
}

func TestSearchKnowledgeRechecksWorkAndArtifactReadGrants(t *testing.T) {
	fixture := openArtifactFixture(t)
	publish := fixture.humanPublishParams(uuid.NewString(), "artifact revocable search", fixture.at(1))
	publish.Summary = "revocable artifact"
	published, err := fixture.artifacts.Publish(context.Background(), publish)
	if err != nil {
		t.Fatal(err)
	}
	artifactGrant, err := fixture.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: fixture.owner}, ArtifactID: published.Artifact.ID,
		TargetKind: ArtifactGrantTargetAgent, TargetID: fixture.agentID, Capability: ArtifactGrantRead, Now: fixture.at(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	activateKnowledgeSearchGeneration(t, fixture.database, fixture.at(3))
	page, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "revocable", Now: fixture.at(4)})
	if err != nil || len(page.Results) != 1 || page.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version}) {
		t.Fatalf("granted artifact search = %+v, %v", page, err)
	}
	if _, err := fixture.artifacts.RevokeGrant(context.Background(), RevokeArtifactGrantParams{RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: fixture.owner}, GrantID: artifactGrant.ID, Now: fixture.at(5)}); err != nil {
		t.Fatal(err)
	}
	page, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "revocable", Now: fixture.at(6)})
	if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("revoked artifact search = %+v, %v", page, err)
	}
	workGrant := fixture.issueAgentWorkGrant(t, CapabilityWorkRead, fixture.at(7))
	page, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "produce", Now: fixture.at(8)})
	if err != nil || len(page.Results) != 1 || page.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceWork, ID: fixture.work.ID}) {
		t.Fatalf("granted work search = %+v, %v", page, err)
	}
	if _, err := fixture.database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: workGrant.ID, Now: fixture.at(9)}); err != nil {
		t.Fatal(err)
	}
	page, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "produce", Now: fixture.at(10)})
	if err != nil || len(page.Results) != 0 || page.NextCursor != "" {
		t.Fatalf("revoked work search = %+v, %v", page, err)
	}
}

func TestSearchKnowledgeRejectsDisabledHumanCurrentProof(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "disabled search", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SendMessage(context.Background(), SendMessageParams{RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "disabled human search", Now: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	peer, err := database.CreateHuman(context.Background(), CreateHumanParams{RequestID: uuid.NewString(), Actor: owner, Name: "Search Peer", Role: "member", Credential: "search-peer-credential-abcdefghijklmnopqrstuvwxyz", Now: now.Add(2 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{Kind: "human", ID: peer.ID, OrganizationID: owner.OrganizationID}
	if _, err := database.AddMember(context.Background(), ChangeMemberParams{RequestID: uuid.NewString(), Actor: owner, SpaceID: space.ID, Member: principal, Now: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	root, err := scanGrant(database.db.QueryRow(grantSelect+` WHERE subject_kind = 'human' AND subject_id = ? AND parent_grant_id = ''`, owner.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.IssueGrant(context.Background(), IssueGrantParams{RequestID: uuid.NewString(), Actor: owner, Subject: principal, Capability: CapabilitySpaceRead, Scope: Scope{Kind: "space", ID: space.ID}, ParentGrantID: root.ID, Now: now.Add(4 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	activateKnowledgeSearchGeneration(t, database, now.Add(5*time.Second))
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: principal, Query: "disabled", Now: now.Add(6 * time.Second)})
	if err != nil || len(page.Results) != 1 {
		t.Fatalf("active human search = %+v, %v", page, err)
	}
	if _, err := database.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: owner, HumanID: peer.ID, Status: "disabled", Now: now.Add(7 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: principal, Query: "disabled", Now: now.Add(8 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchUnauthenticated) {
		t.Fatalf("disabled human search error = %v", err)
	}
}

type knowledgeSearchMutationState struct {
	Messages, Works, Artifacts, Versions, Audits, Dirty, Projections, FTS int
	Metadata                                                              KnowledgeIndexMetadata
}

func knowledgeSearchMutationSnapshot(t *testing.T, database *Store) knowledgeSearchMutationState {
	t.Helper()
	state := knowledgeSearchMutationState{}
	for table, destination := range map[string]*int{
		"messages":                  &state.Messages,
		"works":                     &state.Works,
		"artifacts":                 &state.Artifacts,
		"artifact_versions":         &state.Versions,
		"audit_events":              &state.Audits,
		"knowledge_dirty_sources":   &state.Dirty,
		"knowledge_projection_rows": &state.Projections,
		"knowledge_fts":             &state.FTS,
	} {
		if err := database.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	metadata, err := database.KnowledgeIndexMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.Metadata = metadata
	return state
}

func activateKnowledgeSearchGeneration(t *testing.T, database *Store, now time.Time) uint64 {
	t.Helper()
	rebuilding, err := database.StartKnowledgeRebuild(context.Background(), now)
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
	return rebuilding.NextGeneration
}

func TestKnowledgeSourceValidation(t *testing.T) {
	err := validateKnowledgeSource(KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString(), Version: 1})
	if err == nil {
		t.Fatal("versioned message source was accepted")
	}
}
