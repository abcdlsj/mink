package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func TestKnowledgeRebuildSearchAndAuthorization(t *testing.T) {
	database, owner, peer, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID},
		Body: "durable searchable phrase", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "durable", Now: now.Add(2 * time.Second)})
	if err != nil || before.Status != KnowledgeIndexDegraded || len(before.Results) != 0 {
		t.Fatalf("search before rebuild = %+v, %v", before, err)
	}
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "durable phrase", Now: now.Add(3 * time.Second)})
	if err != nil || page.Status != KnowledgeIndexReady || len(page.Results) != 1 || page.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID}) {
		t.Fatalf("owner search = %+v, %v", page, err)
	}
	hidden, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: peer, Query: "durable", Now: now.Add(3 * time.Second)})
	if err != nil || hidden.Status != KnowledgeIndexReady || len(hidden.Results) != 0 {
		t.Fatalf("non-member search = %+v, %v", hidden, err)
	}
	state, err := database.KnowledgeIndexState(context.Background())
	if err != nil || state.Status != KnowledgeIndexReady {
		t.Fatalf("knowledge state = %+v, %v", state, err)
	}
	if _, found, err := database.NextKnowledgeDirtySource(context.Background()); err != nil || found {
		t.Fatalf("rebuild retained dirty source: found=%t err=%v", found, err)
	}
}

func TestKnowledgeProjectionAcknowledgesOnlyCurrentRevision(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "projection", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "projection body", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	wrong := [sha256.Size]byte{9}
	dirty, err := database.EnqueueKnowledgeDirtySource(context.Background(), KnowledgeSource{Kind: KnowledgeSourceMessage, ID: message.ID}, wrong, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	document, found, err := database.ReadKnowledgeSourceDocument(context.Background(), dirty.Source)
	if err != nil || !found {
		t.Fatalf("document = %+v, %t, %v", document, found, err)
	}
	applied, err := database.ApplyKnowledgeProjection(context.Background(), dirty.Sequence, document)
	if err != nil || applied {
		t.Fatalf("stale projection = %t, %v", applied, err)
	}
	state, err := database.KnowledgeIndexState(context.Background())
	if err != nil || state.AppliedSequence != dirty.Sequence {
		t.Fatalf("state after stale projection = %+v, %v", state, err)
	}
}

func TestKnowledgeProjectionFailsClosedWithoutDurableDirtySource(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	document := KnowledgeSourceDocument{
		Source: KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString()},
		Body:   "not durable",
	}
	if _, err := database.ApplyKnowledgeProjection(context.Background(), 1, document); err == nil {
		t.Fatal("projection without a durable dirty source succeeded")
	}
	state, err := database.KnowledgeIndexState(context.Background())
	if err != nil || state.AppliedSequence != 0 || state.Status != KnowledgeIndexReady {
		t.Fatalf("state after rejected projection = %+v, %v", state, err)
	}
}

func TestKnowledgeDirtySourceSequenceAndRollback(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	for index, source := range []KnowledgeSource{
		{Kind: KnowledgeSourceMessage, ID: uuid.NewString()},
		{Kind: KnowledgeSourceWork, ID: uuid.NewString()},
		{Kind: KnowledgeSourceArtifactVersion, ID: uuid.NewString(), Version: 1},
	} {
		entry, err := database.EnqueueKnowledgeDirtySource(context.Background(), source, [sha256.Size]byte{byte(index + 1)}, now.Add(time.Duration(index)*time.Second))
		if err != nil || entry.Sequence != uint64(index+1) {
			t.Fatalf("dirty source %d = %+v, %v", index, entry, err)
		}
	}
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enqueueKnowledgeDirtySource(context.Background(), tx, KnowledgeSource{Kind: KnowledgeSourceMessage, ID: uuid.NewString()}, [sha256.Size]byte{9}, now.Add(time.Minute)); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("visible dirty source count = %d, %v", count, err)
	}
}

func TestKnowledgeHealthDetectsAndRebuildsDerivedCorruption(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`DROP TABLE knowledge_fts`); err != nil {
		t.Fatal(err)
	}
	health, err := database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexCorrupt {
		t.Fatalf("health = %v, %v", health, err)
	}
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	health, err = database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexHealthy {
		t.Fatalf("health after rebuild = %v, %v", health, err)
	}
}

func TestKnowledgeSearchFailsClosedOnDerivedCorruption(t *testing.T) {
	database, owner, _, _, now := openCollaborationFixture(t, filepath.Join(t.TempDir(), "server.db"))
	defer database.Close()
	space, err := database.CreateGroup(context.Background(), CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "search corruption", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	message, err := database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: space.ID}, Body: "corruptible search", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`UPDATE knowledge_projection_rows SET fts_rowid = fts_rowid + 100 WHERE source_kind = 'message' AND source_id = ?`, message.ID); err != nil {
		t.Fatal(err)
	}
	output, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: owner, Query: "corruptible", Now: now.Add(2 * time.Second)})
	if err != nil || output.Status != KnowledgeIndexDegraded || len(output.Results) != 0 {
		t.Fatalf("corrupt projection search = %+v, %v", output, err)
	}
	health, err := database.CheckKnowledgeIndexHealth(context.Background())
	if err != nil || health != KnowledgeIndexCorrupt {
		t.Fatalf("corrupt projection health = %v, %v", health, err)
	}
}

func TestKnowledgeSearchRechecksCurrentRuntimeAndSpaceAccess(t *testing.T) {
	fixture := openRunFixture(t)
	message, err := fixture.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, Target: MessageTarget{Kind: MessageTargetSpace, ID: fixture.group.ID}, Body: "runtime searchable", Now: fixture.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "runtime", Now: fixture.at(3)})
	if err != nil || len(output.Results) != 1 || output.Results[0].Source.ID != message.ID {
		t.Fatalf("runtime search = %+v, %v", output, err)
	}
	if _, err := fixture.database.RemoveMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, SpaceID: fixture.group.ID, Member: Principal{Kind: "agent", ID: fixture.agentID}, Now: fixture.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	output, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "runtime", Now: fixture.at(5)})
	if err != nil || len(output.Results) != 0 {
		t.Fatalf("removed member search = %+v, %v", output, err)
	}
	stale := fixture.authentication
	rotated := rotateRunRuntime(t, fixture, 91, fixture.at(6))
	if _, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: stale, Query: "runtime", Now: fixture.at(8)}); !errors.Is(err, ErrKnowledgeSearchUnauthenticated) {
		t.Fatalf("stale runtime error = %v", err)
	}
	if _, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: rotated, Query: "runtime", Now: fixture.at(8)}); err != nil {
		t.Fatalf("current runtime search = %v", err)
	}
}

func TestKnowledgeSearchUsesAllSourceKindsAndIsReadOnly(t *testing.T) {
	fixture := openArtifactFixture(t)
	published, err := fixture.artifacts.Publish(context.Background(), fixture.humanPublishParams(uuid.NewString(), "artifact search content", fixture.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := knowledgeMutationCounts(t, fixture.database)
	output, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: fixture.owner, Query: "artifact", Now: fixture.at(3)})
	if err != nil {
		t.Fatal(err)
	}
	if after := knowledgeMutationCounts(t, fixture.database); after != before {
		t.Fatalf("search mutated facts: before=%+v after=%+v", before, after)
	}
	found := make(map[KnowledgeSource]KnowledgeSearchResult, len(output.Results))
	for _, result := range output.Results {
		found[result.Source] = result
	}
	for _, source := range []KnowledgeSource{
		{Kind: KnowledgeSourceMessage, ID: fixture.source.ID},
		{Kind: KnowledgeSourceWork, ID: fixture.work.ID},
		{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version},
	} {
		if _, ok := found[source]; !ok {
			t.Fatalf("search omitted current source %+v: %+v", source, output)
		}
	}
}

func TestKnowledgeSearchDropsStaleProjectionUntilDirtyIsApplied(t *testing.T) {
	fixture := openArtifactFixture(t)
	if err := fixture.database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.TransitionWork(context.Background(), TransitionWorkParams{
		RequestID: uuid.NewString(), Actor: fixture.owner, WorkID: fixture.work.ID,
		ToState: WorkStateBlocked, Reason: "projection revision changed", Now: fixture.at(1),
	}); err != nil {
		t.Fatal(err)
	}
	output, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: fixture.owner, Query: "produce", Now: fixture.at(2)})
	if err != nil || len(output.Results) != 0 {
		t.Fatalf("stale projection search = %+v, %v", output, err)
	}
	dirty, found, err := fixture.database.NextKnowledgeDirtySource(context.Background())
	if err != nil || !found {
		t.Fatalf("next dirty = %+v, %t, %v", dirty, found, err)
	}
	document, found, err := fixture.database.ReadKnowledgeSourceDocument(context.Background(), dirty.Source)
	if err != nil || !found {
		t.Fatalf("current source = %+v, %t, %v", document, found, err)
	}
	if applied, err := fixture.database.ApplyKnowledgeProjection(context.Background(), dirty.Sequence, document); err != nil || !applied {
		t.Fatalf("apply dirty = %t, %v", applied, err)
	}
	output, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: fixture.owner, Query: "produce", Now: fixture.at(3)})
	if err != nil || len(output.Results) != 1 || output.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceWork, ID: fixture.work.ID}) {
		t.Fatalf("current projection search = %+v, %v", output, err)
	}
}

func TestKnowledgeSearchRechecksWorkAndArtifactGrants(t *testing.T) {
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
	if err := fixture.database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "revocable", Now: fixture.at(4)})
	if err != nil || len(output.Results) != 1 || output.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version}) {
		t.Fatalf("granted artifact search = %+v, %v", output, err)
	}
	if _, err := fixture.artifacts.RevokeGrant(context.Background(), RevokeArtifactGrantParams{RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: fixture.owner}, GrantID: artifactGrant.ID, Now: fixture.at(5)}); err != nil {
		t.Fatal(err)
	}
	output, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "revocable", Now: fixture.at(6)})
	if err != nil || len(output.Results) != 0 {
		t.Fatalf("revoked artifact search = %+v, %v", output, err)
	}
	workGrant := fixture.issueAgentWorkGrant(t, CapabilityWorkRead, fixture.at(7))
	output, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "produce", Now: fixture.at(8)})
	if err != nil || len(output.Results) != 1 || output.Results[0].Source != (KnowledgeSource{Kind: KnowledgeSourceWork, ID: fixture.work.ID}) {
		t.Fatalf("granted work search = %+v, %v", output, err)
	}
	if _, err := fixture.database.RevokeGrant(context.Background(), RevokeGrantParams{RequestID: uuid.NewString(), Actor: fixture.owner, GrantID: workGrant.ID, Now: fixture.at(9)}); err != nil {
		t.Fatal(err)
	}
	output, err = fixture.database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Agent: fixture.authentication, Query: "produce", Now: fixture.at(10)})
	if err != nil || len(output.Results) != 0 {
		t.Fatalf("revoked work search = %+v, %v", output, err)
	}
}

func TestKnowledgeSearchRejectsDisabledHuman(t *testing.T) {
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
	if err := database.RebuildKnowledgeIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: principal, Query: "disabled", Now: now.Add(6 * time.Second)}); err != nil || len(output.Results) != 1 {
		t.Fatalf("active human search = %+v, %v", output, err)
	}
	if _, err := database.SetHumanStatus(context.Background(), SetHumanStatusParams{RequestID: uuid.NewString(), Actor: owner, HumanID: peer.ID, Status: "disabled", Now: now.Add(7 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SearchKnowledge(context.Background(), KnowledgeSearchParams{Human: principal, Query: "disabled", Now: now.Add(8 * time.Second)}); !errors.Is(err, ErrKnowledgeSearchUnauthenticated) {
		t.Fatalf("disabled human search error = %v", err)
	}
}

func TestKnowledgeSearchBoundsAndSourceRevisions(t *testing.T) {
	database := openKnowledgeStore(t)
	defer database.Close()
	now := time.Now()
	for name, params := range map[string]KnowledgeSearchParams{
		"empty":       {Query: "", Now: now},
		"query bytes": {Query: strings.Repeat("q", knowledgeSearchQueryMaxBytes+1), Now: now},
		"terms":       {Query: "one two three four five six seven eight nine", Now: now},
		"term bytes":  {Query: strings.Repeat("q", knowledgeSearchTermMaxBytes+1), Now: now},
		"limit":       {Query: "query", Limit: knowledgeSearchMaxLimit + 1, Now: now},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.SearchKnowledge(context.Background(), params); !errors.Is(err, ErrKnowledgeSearchInvalid) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	work := KnowledgeWorkFields{Goal: "goal", AcceptanceCriteria: []string{"a"}, Constraints: []string{"c"}}
	changed := work
	changed.AcceptanceCriteria = []string{"b"}
	if KnowledgeWorkRevision(work) == KnowledgeWorkRevision(changed) {
		t.Fatal("work revision ignored structured field change")
	}
	firstMessageID, secondMessageID := uuid.NewString(), uuid.NewString()
	if KnowledgeMessageRevision(firstMessageID, 1) == KnowledgeMessageRevision(secondMessageID, 1) {
		t.Fatal("message revision ignored identity")
	}
	artifactID := uuid.NewString()
	if KnowledgeArtifactVersionRevision(artifactID, 1, [32]byte{}) == KnowledgeArtifactVersionRevision(artifactID, 2, [32]byte{}) {
		t.Fatal("artifact revision ignored version")
	}
	for _, source := range []KnowledgeSource{
		{Kind: "future", ID: uuid.NewString()},
		{Kind: KnowledgeSourceMessage, ID: "not-a-uuid"},
		{Kind: KnowledgeSourceMessage, ID: uuid.NewString(), Version: 1},
		{Kind: KnowledgeSourceArtifactVersion, ID: uuid.NewString()},
	} {
		if _, err := database.EnqueueKnowledgeDirtySource(context.Background(), source, [sha256.Size]byte{}, now); err == nil {
			t.Fatalf("invalid source was enqueued: %+v", source)
		}
	}
}

func TestKnowledgeSnippetPreservesUTF8Boundary(t *testing.T) {
	body := strings.Repeat("a", knowledgeSearchSnippetMaxBytes-1) + "界"
	snippet, ok := knowledgeSearchSnippet(body)
	if !ok || !utf8.ValidString(snippet) || len(snippet) > knowledgeSearchSnippetMaxBytes {
		t.Fatalf("snippet = %q, %t", snippet, ok)
	}
}

type knowledgeMutationState struct {
	Messages, Works, Artifacts, Versions, Audits, Dirty, Projections, FTS int
	IndexState                                                            KnowledgeIndexState
}

func knowledgeMutationCounts(t *testing.T, database *Store) knowledgeMutationState {
	t.Helper()
	state := knowledgeMutationState{}
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
	indexState, err := database.KnowledgeIndexState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.IndexState = indexState
	return state
}

func assertKnowledgeDirty(t *testing.T, database *Store, source KnowledgeSource, revision [sha256.Size]byte, want int) {
	t.Helper()
	var count int
	var actual []byte
	if err := database.db.QueryRow(`SELECT count(*), COALESCE((SELECT revision FROM knowledge_dirty_sources WHERE source_kind = ? AND source_id = ? AND source_version = ? ORDER BY sequence DESC LIMIT 1), zeroblob(32)) FROM knowledge_dirty_sources WHERE source_kind = ? AND source_id = ? AND source_version = ?`, source.Kind, source.ID, source.Version, source.Kind, source.ID, source.Version).Scan(&count, &actual); err != nil {
		t.Fatal(err)
	}
	if count != want || len(actual) != sha256.Size || string(actual) != string(revision[:]) {
		t.Fatalf("knowledge dirty = count=%d revision=%x, want count=%d revision=%x", count, actual, want, revision)
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
