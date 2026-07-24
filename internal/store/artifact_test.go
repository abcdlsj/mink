package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	artifactblob "github.com/abcdlsj/sumi/internal/artifact/blob"
	"github.com/google/uuid"
)

func TestArtifactHumanPublishVersionsReplayFetchAndRestart(t *testing.T) {
	f := openArtifactFixture(t)
	requestID := uuid.NewString()
	content := "artifact-content-not-for-sqlite"
	params := f.humanPublishParams(requestID, content, f.at(1))
	params.Sources = []ArtifactSourceInput{{Kind: ArtifactSourceMessage, MessageID: f.source.ID}}
	first, err := f.artifacts.Publish(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version.Version != 1 || first.Version.Author != f.owner || first.Version.Execution != nil {
		t.Fatalf("first publish = %+v", first)
	}
	if len(first.Version.Sources) != 1 || first.Version.Sources[0].MessageID != f.source.ID {
		t.Fatalf("first sources = %+v", first.Version.Sources)
	}

	params.Content = strings.NewReader(content)
	params.Now = f.at(2)
	replayed, err := f.artifacts.Publish(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Artifact.ID != first.Artifact.ID || replayed.Version.Version != 1 || !replayed.CommittedAt.Equal(first.CommittedAt) {
		t.Fatalf("publish replay = %+v", replayed)
	}
	params.Summary = "conflicting summary"
	params.Content = strings.NewReader(content)
	if _, err := f.artifacts.Publish(context.Background(), params); !errors.Is(err, ErrArtifactRequestConflict) {
		t.Fatalf("publish conflict = %v", err)
	}

	secondContent := "second immutable version"
	second, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: first.Artifact.ID, OwningWorkID: f.work.ID,
		Name: first.Artifact.Name, MediaType: first.Artifact.MediaType, Summary: "second summary",
		Content: strings.NewReader(secondContent), Now: f.at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Version.Version != 2 || first.Artifact.Name != second.Artifact.Name || first.Artifact.MediaType != second.Artifact.MediaType {
		t.Fatalf("second publish = %+v", second)
	}
	if _, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: first.Artifact.ID, OwningWorkID: f.work.ID,
		Name: "renamed", MediaType: first.Artifact.MediaType, Summary: "invalid mutation",
		Content: strings.NewReader("ignored"), Now: f.at(4),
	}); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("family mutation = %v", err)
	}

	latest, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: ArtifactAuthentication{Human: f.owner}, ArtifactID: first.Artifact.ID, Now: f.at(5),
	})
	if err != nil || latest.Version.Version != 2 {
		t.Fatalf("latest = %+v, %v", latest, err)
	}
	listed, err := f.artifacts.List(context.Background(), ListArtifactsParams{
		Authentication: ArtifactAuthentication{Human: f.owner}, OwningWorkID: f.work.ID, Limit: 50, Now: f.at(5),
	})
	if err != nil || len(listed.Views) != 1 || listed.Views[0].Version.Version != 2 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	assertFetchedArtifact(t, f.artifacts, f.owner, first.Artifact.ID, 1, content, f.at(6))
	assertFetchedArtifact(t, f.artifacts, f.owner, first.Artifact.ID, 2, secondContent, f.at(6))

	var versions, receipts, audits int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_versions WHERE artifact_id = ?`, first.Artifact.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_requests WHERE operation = 'artifact.publish'`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action = 'artifact.publish' AND outcome = 'committed'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if versions != 2 || receipts != 2 || audits != 2 {
		t.Fatalf("versions/receipts/audits = %d/%d/%d", versions, receipts, audits)
	}

	f.restart(t)
	assertFetchedArtifact(t, f.artifacts, f.owner, first.Artifact.ID, 2, secondContent, f.at(7))
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, []byte(content)) || bytes.Contains(databaseBytes, []byte(secondContent)) || bytes.Contains(databaseBytes, []byte(f.blobRoot)) {
		t.Fatal("artifact body or blob path leaked into SQLite")
	}
}

func TestArtifactListIterationErrorDoesNotReturnPartialPage(t *testing.T) {
	f := openArtifactFixture(t)
	published, err := f.artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "partial list first", f.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "partial list second", f.at(2))); err != nil {
		t.Fatal(err)
	}
	before := readArtifactMutationCounts(t, f.database, published.Artifact.ID)
	auditsBefore := auditCount(t, f.database, f.owner.OrganizationID)
	queryContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanned := 0
	cancellationObserved := false
	ctx := context.WithValue(queryContext, artifactBatchAfterScanContextKey{}, artifactBatchAfterScanFunc(func(actual int, rows *sql.Rows) {
		scanned = actual
		if actual == 1 {
			cancel()
			deadline := time.Now().Add(time.Second)
			for rows.Err() == nil && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			cancellationObserved = errors.Is(rows.Err(), context.Canceled)
		}
	}))
	result, err := f.artifacts.List(ctx, ListArtifactsParams{
		Authentication: ArtifactAuthentication{Human: f.owner},
		OwningWorkID:   f.work.ID,
		Limit:          50,
		Now:            f.at(3),
	})
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "iterate artifact batch") {
		t.Fatalf("list with iteration error = %v", err)
	}
	if len(result.Views) != 0 || result.NextArtifactID != "" {
		t.Fatalf("list returned partial page: %+v", result)
	}
	if scanned != 1 {
		t.Fatalf("artifact rows scanned before iteration error = %d", scanned)
	}
	if !cancellationObserved {
		t.Fatal("artifact rows did not observe query cancellation")
	}
	if after := readArtifactMutationCounts(t, f.database, published.Artifact.ID); after != before {
		t.Fatalf("iteration error changed artifact facts: before=%+v after=%+v", before, after)
	}
	if auditsAfter := auditCount(t, f.database, f.owner.OrganizationID); auditsAfter != auditsBefore {
		t.Fatalf("iteration error changed audit count: before=%d after=%d", auditsBefore, auditsAfter)
	}
}

func TestArtifactMessageSourceReadPropagatesBackendError(t *testing.T) {
	f := openArtifactFixture(t)
	tx, err := f.database.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	readable, err := messageSourceReadable(canceled, tx, f.owner, f.source.ID, f.at(1))
	if readable || !errors.Is(err, context.Canceled) {
		t.Fatalf("message source backend error = %v, %v", readable, err)
	}
}

func TestArtifactPublishKnowledgeDirtyFailureRollsBackAllFacts(t *testing.T) {
	f := openArtifactFixture(t)
	defer f.database.Close()
	initial := f.humanPublishParams(uuid.NewString(), "artifact exact", f.at(1))
	published, err := f.artifacts.Publish(context.Background(), initial)
	if err != nil {
		t.Fatal(err)
	}
	initial.Content = strings.NewReader("artifact exact")
	if _, err := f.artifacts.Publish(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	assertKnowledgeDirty(t, f.database, KnowledgeSource{Kind: KnowledgeSourceArtifactVersion, ID: published.Artifact.ID, Version: published.Version.Version}, KnowledgeArtifactVersionRevision(published.Artifact.ID, published.Version.Version, published.Version.Digest), 1)
	var artifactsBefore, versionsBefore, associatedVersionsBefore, receiptsBefore, dirtyBefore int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifacts`).Scan(&artifactsBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_versions`).Scan(&versionsBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_versions WHERE artifact_id = ?`, published.Artifact.ID).Scan(&associatedVersionsBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_requests`).Scan(&receiptsBefore); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources`).Scan(&dirtyBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.db.Exec(`CREATE TRIGGER fail_artifact_dirty BEFORE INSERT ON knowledge_dirty_sources BEGIN SELECT RAISE(ABORT, 'artifact dirty failed'); END`); err != nil {
		t.Fatal(err)
	}
	failed := PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: published.Artifact.ID, OwningWorkID: f.work.ID,
		Name: published.Artifact.Name, MediaType: published.Artifact.MediaType, Summary: "artifact rollback",
		Content: strings.NewReader("artifact rollback"), Now: f.at(2),
	}
	if _, err := f.artifacts.Publish(context.Background(), failed); err == nil || !strings.Contains(err.Error(), "artifact dirty failed") {
		t.Fatalf("artifact dirty rollback = %v", err)
	}
	if _, err := f.database.db.Exec(`DROP TRIGGER fail_artifact_dirty`); err != nil {
		t.Fatal(err)
	}
	var artifactsAfter, versionsAfter, associatedVersionsAfter, receiptsAfter, dirtyAfter, failedReceipt int
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifacts`).Scan(&artifactsAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_versions`).Scan(&versionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_versions WHERE artifact_id = ?`, published.Artifact.ID).Scan(&associatedVersionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_requests`).Scan(&receiptsAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources`).Scan(&dirtyAfter); err != nil {
		t.Fatal(err)
	}
	if err := f.database.db.QueryRow(`SELECT count(*) FROM artifact_requests WHERE request_id = ?`, failed.RequestID).Scan(&failedReceipt); err != nil {
		t.Fatal(err)
	}
	if artifactsAfter != artifactsBefore || versionsAfter != versionsBefore || associatedVersionsAfter != associatedVersionsBefore || receiptsAfter != receiptsBefore || dirtyAfter != dirtyBefore || failedReceipt != 0 {
		t.Fatalf("failed artifact publish changed facts: artifacts %d/%d versions %d/%d associated %d/%d receipts %d/%d dirty %d/%d request receipt %d", artifactsAfter, artifactsBefore, versionsAfter, versionsBefore, associatedVersionsAfter, associatedVersionsBefore, receiptsAfter, receiptsBefore, dirtyAfter, dirtyBefore, failedReceipt)
	}
}

func TestArtifactPublishDeclaredContentMismatchAndReplayConflict(t *testing.T) {
	t.Run("mismatch leaves only a reconcilable orphan", func(t *testing.T) {
		f := openArtifactFixture(t)
		content := []byte("declared artifact body")
		wrongDigest := sha256.Sum256([]byte("different body"))
		size := int64(len(content))
		params := f.humanPublishParams(uuid.NewString(), string(content), f.at(1))
		params.ExpectedDigest = &wrongDigest
		params.ExpectedSize = &size
		if _, err := f.artifacts.Publish(context.Background(), params); !errors.Is(err, ErrArtifactIntegrity) {
			t.Fatalf("declared digest mismatch = %v", err)
		}
		assertArtifactFactCounts(t, f.database, 0, 0, 0)
		var committedAudits int
		if err := f.database.db.QueryRow(`
			SELECT count(*) FROM audit_events
			WHERE action = 'artifact.publish' AND outcome = 'committed'
		`).Scan(&committedAudits); err != nil {
			t.Fatal(err)
		}
		if committedAudits != 0 {
			t.Fatalf("declared mismatch committed audits = %d", committedAudits)
		}
		reconciled, err := f.artifacts.Reconcile(context.Background(), f.at(2))
		if err != nil {
			t.Fatal(err)
		}
		if reconciled.Quarantined != 1 {
			t.Fatalf("declared mismatch orphan reconcile = %+v", reconciled)
		}
	})

	t.Run("replay fingerprints declaration and computed content", func(t *testing.T) {
		f := openArtifactFixture(t)
		content := []byte("fingerprinted artifact")
		digest := sha256.Sum256(content)
		size := int64(len(content))
		params := f.humanPublishParams(uuid.NewString(), string(content), f.at(1))
		params.ExpectedDigest = &digest
		params.ExpectedSize = &size
		first, err := f.artifacts.Publish(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		params.Content = bytes.NewReader(content)
		replayed, err := f.artifacts.Publish(context.Background(), params)
		if err != nil || replayed.Artifact.ID != first.Artifact.ID || replayed.Version.Version != first.Version.Version {
			t.Fatalf("declared replay = %+v, %v", replayed, err)
		}
		changedDigest := sha256.Sum256([]byte("changed declaration"))
		params.ExpectedDigest = &changedDigest
		params.Content = bytes.NewReader(content)
		if _, err := f.artifacts.Publish(context.Background(), params); !errors.Is(err, ErrArtifactRequestConflict) {
			t.Fatalf("changed declaration replay = %v", err)
		}
		params.ExpectedDigest = &digest
		params.Content = strings.NewReader("changed artifact body")
		if _, err := f.artifacts.Publish(context.Background(), params); !errors.Is(err, ErrArtifactRequestConflict) {
			t.Fatalf("changed content replay = %v", err)
		}
		assertArtifactFactCounts(t, f.database, 1, 1, 1)
	})
}

func TestArtifactDomainGrantsAndRestrictedSources(t *testing.T) {
	f := openArtifactFixture(t)
	upstream, err := f.artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "upstream body", f.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	published, err := f.artifacts.Publish(context.Background(), func() PublishArtifactParams {
		params := f.humanPublishParams(uuid.NewString(), "acl body", f.at(2))
		params.Name = "downstream report"
		params.Sources = []ArtifactSourceInput{
			{Kind: ArtifactSourceMessage, MessageID: f.source.ID},
			{Kind: ArtifactSourceVersion, ArtifactID: upstream.Artifact.ID, ArtifactVersion: upstream.Version.Version},
		}
		return params
	}())
	if err != nil {
		t.Fatal(err)
	}
	agentAuthentication := ArtifactAuthentication{Agent: f.authentication}
	if _, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: agentAuthentication, ArtifactID: published.Artifact.ID, Now: f.at(3),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ungranted read = %v", err)
	}

	spaceGrantParams := GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: published.Artifact.ID, TargetKind: ArtifactGrantTargetSpace,
		TargetID: f.group.ID, Capability: ArtifactGrantRead, Now: f.at(4),
	}
	spaceGrant, err := f.artifacts.Grant(context.Background(), spaceGrantParams)
	if err != nil {
		t.Fatal(err)
	}
	spaceGrantParams.Now = f.at(5)
	replayed, err := f.artifacts.Grant(context.Background(), spaceGrantParams)
	if err != nil || replayed.ID != spaceGrant.ID {
		t.Fatalf("grant replay = %+v, %v", replayed, err)
	}
	spaceGrantParams.Capability = ArtifactGrantManage
	if _, err := f.artifacts.Grant(context.Background(), spaceGrantParams); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("space manage conflict = %v", err)
	}

	view, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: agentAuthentication, ArtifactID: published.Artifact.ID, Now: f.at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !view.OwningWorkRestricted || view.OwningWorkID != "" || len(view.Version.Sources) != 2 || view.Version.Sources[0].Restricted || !view.Version.Sources[1].Restricted {
		t.Fatalf("space-granted view = %+v", view)
	}
	assertArtifactReaderCannotManage(t, f, agentAuthentication, published.Artifact, f.at(6))
	exactGrant, err := f.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: published.Artifact.ID, TargetKind: ArtifactGrantTargetAgent,
		TargetID: f.agentID, Capability: ArtifactGrantRead, Now: f.at(7),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.artifacts.RevokeGrant(context.Background(), RevokeArtifactGrantParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		GrantID: spaceGrant.ID, Now: f.at(8),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, GrantID: f.readGrant.ID, Now: f.at(9),
	}); err != nil {
		t.Fatal(err)
	}
	view, err = f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: agentAuthentication, ArtifactID: published.Artifact.ID, Now: f.at(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Version.Sources) != 2 || !view.Version.Sources[0].Restricted || !view.Version.Sources[1].Restricted || view.Version.Sources[0].Kind != "" || view.Version.Sources[0].MessageID != "" || view.Version.Sources[1].ArtifactID != "" {
		t.Fatalf("restricted source leaked provenance = %+v", view.Version.Sources)
	}
	otherWork := f.createWork(t, "artifact work-target source", f.at(11))
	f.issueAgentGrant(t, CapabilityWorkRead, Scope{Kind: "work", ID: otherWork.ID}, f.at(12))
	workGrant, err := f.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: published.Artifact.ID, TargetKind: ArtifactGrantTargetWork,
		TargetID: otherWork.ID, Capability: ArtifactGrantRead, Now: f.at(13),
	})
	if err != nil {
		t.Fatal(err)
	}
	revokeRequest := RevokeArtifactGrantParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		GrantID: exactGrant.ID, Now: f.at(14),
	}
	revoked, err := f.artifacts.RevokeGrant(context.Background(), revokeRequest)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke = %+v, %v", revoked, err)
	}
	revokeRequest.Now = f.at(15)
	replayedRevoke, err := f.artifacts.RevokeGrant(context.Background(), revokeRequest)
	if err != nil || replayedRevoke.ID != exactGrant.ID || replayedRevoke.RevokedAt == nil {
		t.Fatalf("revoke replay = %+v, %v", replayedRevoke, err)
	}
	if _, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: agentAuthentication, ArtifactID: published.Artifact.ID, Now: f.at(16),
	}); err != nil {
		t.Fatalf("work-target read = %v", err)
	}
	assertArtifactReaderCannotManage(t, f, agentAuthentication, published.Artifact, f.at(16))
	if _, err := f.artifacts.RevokeGrant(context.Background(), RevokeArtifactGrantParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		GrantID: workGrant.ID, Now: f.at(17),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: agentAuthentication, ArtifactID: published.Artifact.ID, Now: f.at(18),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked read = %v", err)
	}
}

func TestArtifactAgentExecutionProvenanceAndProjection(t *testing.T) {
	f := openArtifactFixture(t)
	workGrant := f.issueAgentWorkGrant(t, CapabilityWorkManage, f.at(1))
	run := f.acceptTrigger(t, "artifact execution", 2)
	running := f.claimRun(t, run, 5)
	baseParams := PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Agent: f.authentication},
		OwningWorkID: f.work.ID, Name: "execution output", MediaType: "text/plain",
		Summary: "agent result", Content: strings.NewReader("agent execution body"), Now: f.at(6),
	}
	if _, err := f.artifacts.Publish(context.Background(), baseParams); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("agent publish without execution = %v", err)
	}
	baseParams.RequestID = uuid.NewString()
	baseParams.Content = strings.NewReader("agent execution body")
	baseParams.Execution = &ArtifactExecutionInput{RunID: run.ID, Attempt: running.Attempt, Fence: running.Fence + 1}
	if _, err := f.artifacts.Publish(context.Background(), baseParams); !errors.Is(err, ErrRunLeaseStale) {
		t.Fatalf("agent publish with wrong fence = %v", err)
	}
	result, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Agent: f.authentication},
		OwningWorkID: f.work.ID, Name: "execution output", MediaType: "text/plain",
		Summary: "agent result", Content: strings.NewReader("agent execution body"),
		Execution: &ArtifactExecutionInput{RunID: run.ID, Attempt: running.Attempt, Fence: running.Fence},
		Sources:   []ArtifactSourceInput{{Kind: ArtifactSourceMessage, MessageID: run.TriggerMessageID}}, Now: f.at(6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version.Execution == nil || result.Version.Execution.AgentID != f.agentID || result.Version.Execution.ComputerID != f.authentication.Proof.ComputerID() || result.Version.Execution.PlacementDesiredRevision != f.authentication.Proof.PlacementDesiredRevision() {
		t.Fatalf("execution provenance = %+v", result.Version.Execution)
	}
	if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, GrantID: workGrant.ID, Now: f.at(7),
	}); err != nil {
		t.Fatal(err)
	}
	manageGrant, err := f.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner}, ArtifactID: result.Artifact.ID,
		TargetKind: ArtifactGrantTargetAgent, TargetID: f.agentID, Capability: ArtifactGrantManage, Now: f.at(8),
	})
	if err != nil {
		t.Fatal(err)
	}
	managed, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Agent: f.authentication},
		ArtifactID: result.Artifact.ID, OwningWorkID: f.work.ID, Name: result.Artifact.Name,
		MediaType: result.Artifact.MediaType, Summary: "exact-agent managed append", Content: strings.NewReader("managed version"),
		Execution: &ArtifactExecutionInput{RunID: run.ID, Attempt: running.Attempt, Fence: running.Fence}, Now: f.at(9),
	})
	if err != nil || managed.Version.Version != 2 {
		t.Fatalf("exact-agent manage append = %+v, %v", managed, err)
	}
	agentView, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: ArtifactAuthentication{Agent: f.authentication}, ArtifactID: result.Artifact.ID, Now: f.at(9),
	})
	if err != nil || agentView.Version.Execution == nil || agentView.Version.Execution.Restricted ||
		agentView.Version.Execution.RunID != run.ID || agentView.Version.Execution.Attempt != running.Attempt {
		t.Fatalf("current agent execution view = %+v, %v", agentView.Version.Execution, err)
	}
	runGrant, err := scanGrant(f.database.db.QueryRow(grantSelect+`
		WHERE subject_kind = 'agent' AND subject_id = ? AND capability = ?
		  AND scope_kind = 'agent' AND scope_id = ? AND revoked_at IS NULL
	`, f.agentID, CapabilityRunExecute, f.agentID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, GrantID: runGrant.ID, Now: f.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	agentView, err = f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: ArtifactAuthentication{Agent: f.authentication}, ArtifactID: result.Artifact.ID, Now: f.at(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if execution := agentView.Version.Execution; execution == nil || !execution.Restricted ||
		execution.RunID != "" || execution.Attempt != 0 || execution.AgentID != "" || execution.ComputerID != "" ||
		execution.PlacementDesiredRevision != 0 || execution.Fence != 0 {
		t.Fatalf("revoked run.execute leaked execution = %+v", execution)
	}
	listed, err := f.artifacts.List(context.Background(), ListArtifactsParams{
		Authentication: ArtifactAuthentication{Agent: f.authentication}, Limit: 50, Now: f.at(10),
	})
	if err != nil || len(listed.Views) != 1 || listed.Views[0].Version.Execution == nil || !listed.Views[0].Version.Execution.Restricted ||
		listed.Views[0].Version.Execution.RunID != "" || listed.Views[0].Version.Execution.ComputerID != "" {
		t.Fatalf("revoked run.execute list projection = %+v, %v", listed, err)
	}
	if _, err := f.artifacts.RevokeGrant(context.Background(), RevokeArtifactGrantParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		GrantID: manageGrant.ID, Now: f.at(10),
	}); err != nil {
		t.Fatal(err)
	}
	beforeDeniedAppend := readArtifactMutationCounts(t, f.database, result.Artifact.ID)
	if _, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Agent: f.authentication},
		ArtifactID: result.Artifact.ID, OwningWorkID: f.work.ID, Name: result.Artifact.Name,
		MediaType: result.Artifact.MediaType, Summary: "revoked exact-agent manage",
		Content:   strings.NewReader("must not append"),
		Execution: &ArtifactExecutionInput{RunID: run.ID, Attempt: running.Attempt, Fence: running.Fence},
		Now:       f.at(10),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("append after exact-agent manage revoke = %v", err)
	}
	if after := readArtifactMutationCounts(t, f.database, result.Artifact.ID); after != beforeDeniedAppend {
		t.Fatalf("revoked manage changed artifact facts: before=%+v after=%+v", beforeDeniedAppend, after)
	}
	if _, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		OwningWorkID: f.work.ID, Name: "bad human execution", MediaType: "text/plain", Summary: "invalid",
		Content: strings.NewReader("invalid"), Execution: &ArtifactExecutionInput{RunID: run.ID, Attempt: running.Attempt, Fence: running.Fence}, Now: f.at(10),
	}); !errors.Is(err, ErrArtifactInvalid) {
		t.Fatalf("human execution = %v", err)
	}

	peer := createTestHuman(t, f.database, f.owner, "artifact-peer", "member", "artifact-peer-credential-abcdefghijklmnopqrstuvwxyz", f.at(11))
	peerPrincipal := Principal{Kind: "human", ID: peer.ID, OrganizationID: f.owner.OrganizationID}
	if _, err := f.database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID,
		Member: peerPrincipal, Now: f.at(12),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, Subject: peerPrincipal,
		Capability: CapabilitySpaceRead, Scope: Scope{Kind: "space", ID: f.group.ID},
		ParentGrantID: f.rootGrant.ID, Now: f.at(13),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner}, ArtifactID: result.Artifact.ID,
		TargetKind: ArtifactGrantTargetSpace, TargetID: f.group.ID, Capability: ArtifactGrantRead, Now: f.at(14),
	}); err != nil {
		t.Fatal(err)
	}
	peerView, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: ArtifactAuthentication{Human: peerPrincipal}, ArtifactID: result.Artifact.ID, Now: f.at(15),
	})
	if err != nil {
		t.Fatal(err)
	}
	if peerView.Version.Execution == nil || !peerView.Version.Execution.Restricted || peerView.Version.Execution.AgentID != "" || peerView.Version.Execution.RunID != "" || peerView.Version.Execution.Fence != 0 {
		t.Fatalf("restricted execution leaked provenance = %+v", peerView.Version.Execution)
	}
	ownerView, err := f.artifacts.Get(context.Background(), GetArtifactParams{
		Authentication: ArtifactAuthentication{Human: f.owner}, ArtifactID: result.Artifact.ID, Now: f.at(15),
	})
	if err != nil || ownerView.Version.Execution == nil || ownerView.Version.Execution.Restricted || ownerView.Version.Execution.RunID != run.ID {
		t.Fatalf("owner execution view = %+v, %v", ownerView.Version.Execution, err)
	}
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_version_executions SET fence = fence + 1 WHERE artifact_id = ?`, result.Artifact.ID)
	humanVersion, err := f.artifacts.Publish(context.Background(), func() PublishArtifactParams {
		params := f.humanPublishParams(uuid.NewString(), "human provenance", f.at(16))
		params.Name = "human provenance"
		return params
	}())
	if err != nil {
		t.Fatal(err)
	}
	expectArtifactSQLFailure(t, f.database, `
		INSERT INTO artifact_version_executions(
			artifact_id, version, organization_id, run_id, attempt,
			agent_id, computer_id, placement_desired_revision, fence
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, humanVersion.Artifact.ID, humanVersion.Version.Version, humanVersion.Artifact.OrganizationID,
		run.ID, running.Attempt, f.agentID, running.LeaseHolderComputerID, running.PlacementDesiredRevision, running.Fence)
}

func TestArtifactFactsAreImmutableAndGrantsAreRevokeOnly(t *testing.T) {
	f := openArtifactFixture(t)
	params := f.humanPublishParams(uuid.NewString(), "immutable body", f.at(1))
	params.Sources = []ArtifactSourceInput{{Kind: ArtifactSourceMessage, MessageID: f.source.ID}}
	published, err := f.artifacts.Publish(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner}, ArtifactID: published.Artifact.ID,
		TargetKind: ArtifactGrantTargetAgent, TargetID: f.agentID, Capability: ArtifactGrantRead, Now: f.at(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	expectArtifactSQLFailure(t, f.database, `UPDATE artifacts SET name = 'changed' WHERE id = ?`, published.Artifact.ID)
	expectArtifactSQLFailure(t, f.database, `DELETE FROM artifacts WHERE id = ?`, published.Artifact.ID)
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_versions SET summary = 'changed' WHERE artifact_id = ?`, published.Artifact.ID)
	expectArtifactSQLFailure(t, f.database, `DELETE FROM artifact_versions WHERE artifact_id = ?`, published.Artifact.ID)
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_version_sources SET ordinal = ordinal + 1 WHERE artifact_id = ?`, published.Artifact.ID)
	expectArtifactSQLFailure(t, f.database, `
		INSERT INTO artifact_version_sources(
			id, artifact_id, version, organization_id, ordinal, source_kind,
			source_message_id, source_artifact_id, source_artifact_version
		) VALUES(?, ?, ?, ?, 99, 'message', ?, NULL, NULL)
	`, uuid.NewString(), published.Artifact.ID, published.Version.Version, published.Artifact.OrganizationID, f.source.ID)
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_blobs SET size = size + 1 WHERE digest = ?`, published.Version.Digest[:])
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_grants SET target_id = ? WHERE id = ?`, uuid.NewString(), grant.ID)
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_requests SET committed_at = committed_at + 1 WHERE request_id = ?`, params.RequestID)

	if _, err := f.artifacts.RevokeGrant(context.Background(), RevokeArtifactGrantParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner}, GrantID: grant.ID, Now: f.at(3),
	}); err != nil {
		t.Fatal(err)
	}
	expectArtifactSQLFailure(t, f.database, `UPDATE artifact_grants SET revoked_at = revoked_at + 1 WHERE id = ?`, grant.ID)
	expectArtifactSQLFailure(t, f.database, `DELETE FROM artifact_grants WHERE id = ?`, grant.ID)
}

func TestArtifactPublishRevalidatesAuthorityAfterDurablePut(t *testing.T) {
	f := openArtifactFixture(t)
	member := createTestHuman(t, f.database, f.owner, "artifact-publisher", "member", "artifact-publisher-credential-abcdefghijklmnopqrstuvwxyz", f.at(1))
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: f.owner.OrganizationID}
	workGrant, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, Subject: memberPrincipal,
		Capability: CapabilityWorkManage, Scope: Scope{Kind: "work", ID: f.work.ID},
		ParentGrantID: f.rootGrant.ID, Now: f.at(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	callback := func() {
		if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{
			RequestID: uuid.NewString(), Actor: f.owner, GrantID: workGrant.ID, Now: f.at(4),
		}); err != nil {
			t.Error(err)
		}
	}
	storeWithHook := f.storeWithPutHook(t, callback)
	if _, err := storeWithHook.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: memberPrincipal},
		OwningWorkID: f.work.ID, Name: "revoked upload", MediaType: "text/plain", Summary: "must not commit",
		Content: strings.NewReader("durable orphan after revoke"), Now: f.at(3),
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("publish after revoke = %v", err)
	}
	assertArtifactFactCounts(t, f.database, 0, 0, 0)
	result, err := storeWithHook.Reconcile(context.Background(), f.at(5))
	if err != nil {
		t.Fatal(err)
	}
	if result.Quarantined != 1 {
		t.Fatalf("orphan reconcile = %+v", result)
	}
}

func TestArtifactPublishRevalidatesSourceAccessAfterDurablePut(t *testing.T) {
	f := openArtifactFixture(t)
	member := createTestHuman(t, f.database, f.owner, "artifact-source-publisher", "member", "artifact-source-publisher-credential-abcdefghijklmnopqrstuvwxyz", f.at(1))
	memberPrincipal := Principal{Kind: "human", ID: member.ID, OrganizationID: f.owner.OrganizationID}
	if _, err := f.database.AddMember(context.Background(), ChangeMemberParams{
		RequestID: uuid.NewString(), Actor: f.owner, SpaceID: f.group.ID,
		Member: memberPrincipal, Now: f.at(2),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, Subject: memberPrincipal,
		Capability: CapabilityWorkManage, Scope: Scope{Kind: "work", ID: f.work.ID},
		ParentGrantID: f.rootGrant.ID, Now: f.at(3),
	}); err != nil {
		t.Fatal(err)
	}
	sourceGrant, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner, Subject: memberPrincipal,
		Capability: CapabilitySpaceRead, Scope: Scope{Kind: "space", ID: f.group.ID},
		ParentGrantID: f.rootGrant.ID, Now: f.at(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	storeWithHook := f.storeWithPutHook(t, func() {
		if _, err := f.database.RevokeGrant(context.Background(), RevokeGrantParams{
			RequestID: uuid.NewString(), Actor: f.owner, GrantID: sourceGrant.ID, Now: f.at(5),
		}); err != nil {
			t.Error(err)
		}
	})
	params := PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: memberPrincipal},
		OwningWorkID: f.work.ID, Name: "source revoked", MediaType: "text/plain", Summary: "must not commit",
		Sources: []ArtifactSourceInput{{Kind: ArtifactSourceMessage, MessageID: f.source.ID}},
		Content: strings.NewReader("source access orphan"), Now: f.at(4),
	}
	if _, err := storeWithHook.Publish(context.Background(), params); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("publish after source revoke = %v", err)
	}
	assertArtifactFactCounts(t, f.database, 0, 0, 0)
}

func TestArtifactPublishRevalidatesAgentRuntimeAfterDurablePut(t *testing.T) {
	f := openArtifactFixture(t)
	f.issueAgentWorkGrant(t, CapabilityWorkManage, f.at(1))
	run := f.acceptTrigger(t, "runtime rotation", 2)
	running := f.claimRun(t, run, 5)
	storeWithHook := f.storeWithPutHook(t, func() {
		rotateRunRuntime(t, f.runFixture, 99, f.at(7))
	})
	if _, err := storeWithHook.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Agent: f.authentication},
		OwningWorkID: f.work.ID, Name: "stale runtime", MediaType: "text/plain", Summary: "must not commit",
		Content:   strings.NewReader("runtime orphan"),
		Execution: &ArtifactExecutionInput{RunID: run.ID, Attempt: running.Attempt, Fence: running.Fence},
		Now:       f.at(6),
	}); !errors.Is(err, ErrAgentRuntimeUnauthenticated) {
		t.Fatalf("publish after runtime rotation = %v", err)
	}
	assertArtifactFactCounts(t, f.database, 0, 0, 0)
}

func TestArtifactBlobMetadataRejectsDigestSizeConflict(t *testing.T) {
	f := openArtifactFixture(t)
	digest := sha256.Sum256([]byte("fixed digest"))
	backend := &scriptedArtifactBlobs{digest: digest, sizes: []int64{3, 4}}
	artifacts, err := NewArtifactStore(f.database, backend, ArtifactMaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	first, err := artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		OwningWorkID: f.work.ID, Name: "collision", MediaType: "application/octet-stream", Summary: "first",
		Content: strings.NewReader("one"), Now: f.at(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: ArtifactAuthentication{Human: f.owner},
		ArtifactID: first.Artifact.ID, OwningWorkID: f.work.ID,
		Name: first.Artifact.Name, MediaType: first.Artifact.MediaType, Summary: "second",
		Content: strings.NewReader("two"), Now: f.at(2),
	}); !errors.Is(err, ErrArtifactIntegrity) {
		t.Fatalf("digest/size conflict = %v", err)
	}
	assertArtifactFactCounts(t, f.database, 1, 1, 1)
}

func TestArtifactBlobFailuresDoNotLeakBackendDetails(t *testing.T) {
	f := openArtifactFixture(t)
	secret := f.blobRoot + "/object-key artifact-secret-body"
	failing := &failingArtifactBlobs{putErr: errors.New(secret), reconcileErr: errors.New(secret)}
	artifacts, err := NewArtifactStore(f.database, failing, ArtifactMaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "body", f.at(1))); !errors.Is(err, ErrArtifactBlobUnavailable) || strings.Contains(err.Error(), secret) {
		t.Fatalf("put failure = %v", err)
	}
	if _, err := artifacts.Reconcile(context.Background(), f.at(2)); !errors.Is(err, ErrArtifactBlobUnavailable) || strings.Contains(err.Error(), secret) {
		t.Fatalf("reconcile failure = %v", err)
	}
}

func TestArtifactReconcileMarksMissingAndCorruptWithoutStopping(t *testing.T) {
	f := openArtifactFixture(t)
	first, err := f.artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "first blob", f.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "second blob", f.at(2)))
	if err != nil {
		t.Fatal(err)
	}
	firstPath := artifactBlobPath(f.blobRoot, first.Version.Digest)
	secondPath := artifactBlobPath(f.blobRoot, second.Version.Digest)
	if err := os.Remove(firstPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := f.artifacts.Reconcile(context.Background(), f.at(3))
	if err != nil {
		t.Fatal(err)
	}
	if result.Missing != 1 || result.Corrupt != 1 || result.Quarantined != 1 {
		t.Fatalf("reconcile = %+v", result)
	}
	for _, artifactID := range []string{first.Artifact.ID, second.Artifact.ID} {
		if _, err := f.artifacts.Fetch(context.Background(), FetchArtifactParams{
			Authentication: ArtifactAuthentication{Human: f.owner}, ArtifactID: artifactID, Now: f.at(4),
		}); !errors.Is(err, ErrArtifactIntegrity) {
			t.Fatalf("fetch %s after reconcile = %v", artifactID, err)
		}
	}
}

func TestArtifactReconcileIterationErrorDoesNotTouchBlobOrInventory(t *testing.T) {
	f := openArtifactFixture(t)
	published, err := f.artifacts.Publish(context.Background(), f.humanPublishParams(uuid.NewString(), "reconcile iterator", f.at(1)))
	if err != nil {
		t.Fatal(err)
	}
	backend := &reconcileArtifactBlobHook{ArtifactBlobStore: f.local}
	artifacts, err := NewArtifactStore(f.database, backend, ArtifactMaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	var beforeState string
	var beforeChecked int64
	if err := f.database.db.QueryRow(`SELECT integrity_state, checked_at FROM artifact_blobs WHERE digest = ?`, published.Version.Digest[:]).Scan(&beforeState, &beforeChecked); err != nil {
		t.Fatal(err)
	}
	contextWithIterationError := context.WithValue(context.Background(), artifactReconcileRowsErrorContextKey{}, artifactReconcileRowsErrorFunc(func(rows *sql.Rows) error {
		return errors.New("forced artifact reconcile iteration error")
	}))
	if _, err := artifacts.Reconcile(contextWithIterationError, f.at(2)); err == nil || !strings.Contains(err.Error(), "forced artifact reconcile iteration error") {
		t.Fatalf("reconcile with iteration error = %v", err)
	}
	if backend.reconcileCalls != 0 {
		t.Fatalf("blob reconcile calls after iteration error = %d", backend.reconcileCalls)
	}
	var afterState string
	var afterChecked int64
	if err := f.database.db.QueryRow(`SELECT integrity_state, checked_at FROM artifact_blobs WHERE digest = ?`, published.Version.Digest[:]).Scan(&afterState, &afterChecked); err != nil {
		t.Fatal(err)
	}
	if afterState != beforeState || afterChecked != beforeChecked {
		t.Fatalf("iteration error changed artifact inventory: state %q/%q checked %d/%d", afterState, beforeState, afterChecked, beforeChecked)
	}
	if _, err := os.Stat(artifactBlobPath(f.blobRoot, published.Version.Digest)); err != nil {
		t.Fatalf("iteration error removed referenced blob: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(f.blobRoot, "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("iteration error quarantined blobs: %v", entries)
	}
}

type artifactFixture struct {
	*runFixture
	artifacts    *ArtifactStore
	local        *artifactblob.Local
	blobRoot     string
	databasePath string
	source       Message
	work         Work
}

func openArtifactFixture(t *testing.T) *artifactFixture {
	t.Helper()
	runs := openRunFixture(t)
	source, err := runs.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: runs.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: runs.group.ID}, Body: "artifact source", Now: runs.at(-3),
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := runs.database.CreateWork(context.Background(), WorkCreateParams{
		RequestID: uuid.NewString(), Actor: runs.owner, SourceMessageID: source.ID, SourceSpaceID: source.SpaceID,
		SourceTarget: source.Target, SourceTargetSequence: source.TargetSequence,
		Goal: "produce artifact", AcceptanceCriteria: []string{"artifact is durable"}, Now: runs.at(-2),
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "artifacts")
	local, err := artifactblob.OpenLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := NewArtifactStore(runs.database, local, ArtifactMaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	return &artifactFixture{
		runFixture: runs, artifacts: artifacts, local: local, blobRoot: root,
		databasePath: runs.path, source: source, work: work,
	}
}

func (f *artifactFixture) humanPublishParams(requestID, content string, now time.Time) PublishArtifactParams {
	return PublishArtifactParams{
		RequestID: requestID, Authentication: ArtifactAuthentication{Human: f.owner},
		OwningWorkID: f.work.ID, Name: "run report", MediaType: "text/plain",
		Summary: "artifact summary", Content: strings.NewReader(content), Now: now,
	}
}

func (f *artifactFixture) issueAgentWorkGrant(t *testing.T, capability Capability, now time.Time) Grant {
	t.Helper()
	return f.issueAgentGrant(t, capability, Scope{Kind: "work", ID: f.work.ID}, now)
}

func (f *artifactFixture) issueAgentGrant(t *testing.T, capability Capability, scope Scope, now time.Time) Grant {
	t.Helper()
	grant, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Subject:    Principal{Kind: "agent", ID: f.agentID, OrganizationID: f.owner.OrganizationID},
		Capability: capability, Scope: scope, ParentGrantID: f.rootGrant.ID, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *artifactFixture) createWork(t *testing.T, body string, now time.Time) Work {
	t.Helper()
	source, err := f.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: body, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.database.CreateWork(context.Background(), WorkCreateParams{
		RequestID: uuid.NewString(), Actor: f.owner, SourceMessageID: source.ID, SourceSpaceID: source.SpaceID,
		SourceTarget: source.Target, SourceTargetSequence: source.TargetSequence,
		Goal: body, AcceptanceCriteria: []string{"complete"}, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func (f *artifactFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	artifacts, err := NewArtifactStore(database, f.local, ArtifactMaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	f.database = database
	f.artifacts = artifacts
}

func (f *artifactFixture) storeWithPutHook(t *testing.T, callback func()) *ArtifactStore {
	t.Helper()
	backend := &artifactBlobHook{ArtifactBlobStore: f.local, callback: callback}
	artifacts, err := NewArtifactStore(f.database, backend, ArtifactMaxBlobSize)
	if err != nil {
		t.Fatal(err)
	}
	return artifacts
}

type artifactBlobHook struct {
	ArtifactBlobStore
	callback func()
	once     sync.Once
}

type reconcileArtifactBlobHook struct {
	ArtifactBlobStore
	reconcileCalls int
}

func (b *reconcileArtifactBlobHook) Reconcile(ctx context.Context, references map[[sha256.Size]byte]int64, now time.Time, retention time.Duration) (map[[sha256.Size]byte]string, int, int, error) {
	b.reconcileCalls++
	return b.ArtifactBlobStore.Reconcile(ctx, references, now, retention)
}

func (b *artifactBlobHook) Put(ctx context.Context, source io.Reader, limit int64) ([sha256.Size]byte, int64, error) {
	digest, size, err := b.ArtifactBlobStore.Put(ctx, source, limit)
	if err == nil {
		b.once.Do(b.callback)
	}
	return digest, size, err
}

type scriptedArtifactBlobs struct {
	digest [sha256.Size]byte
	sizes  []int64
	index  int
}

type failingArtifactBlobs struct {
	putErr       error
	openErr      error
	reconcileErr error
}

func (b *failingArtifactBlobs) Put(context.Context, io.Reader, int64) ([sha256.Size]byte, int64, error) {
	return [sha256.Size]byte{}, 0, b.putErr
}

func (b *failingArtifactBlobs) Open(context.Context, [sha256.Size]byte, int64) (io.ReadCloser, error) {
	return nil, b.openErr
}

func (b *failingArtifactBlobs) Reconcile(context.Context, map[[sha256.Size]byte]int64, time.Time, time.Duration) (map[[sha256.Size]byte]string, int, int, error) {
	return nil, 0, 0, b.reconcileErr
}

func (b *scriptedArtifactBlobs) Put(context.Context, io.Reader, int64) ([sha256.Size]byte, int64, error) {
	if b.index >= len(b.sizes) {
		return [sha256.Size]byte{}, 0, errors.New("no scripted blob result")
	}
	size := b.sizes[b.index]
	b.index++
	return b.digest, size, nil
}

func (b *scriptedArtifactBlobs) Open(context.Context, [sha256.Size]byte, int64) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (b *scriptedArtifactBlobs) Reconcile(context.Context, map[[sha256.Size]byte]int64, time.Time, time.Duration) (map[[sha256.Size]byte]string, int, int, error) {
	return nil, 0, 0, errors.New("not implemented")
}

func assertFetchedArtifact(t *testing.T, artifacts *ArtifactStore, actor Principal, artifactID string, version uint64, want string, now time.Time) {
	t.Helper()
	fetched, err := artifacts.Fetch(context.Background(), FetchArtifactParams{
		Authentication: ArtifactAuthentication{Human: actor}, ArtifactID: artifactID, Version: version, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fetched.Content.Close()
	content, err := io.ReadAll(fetched.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("fetched content = %q, want %q", content, want)
	}
}

func assertArtifactFactCounts(t *testing.T, database *Store, wantArtifacts, wantVersions, wantReceipts int) {
	t.Helper()
	for table, want := range map[string]int{"artifacts": wantArtifacts, "artifact_versions": wantVersions, "artifact_requests": wantReceipts} {
		var count int
		if err := database.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}

type artifactMutationCounts struct {
	Versions     int
	Receipts     int
	ActiveGrants int
}

func readArtifactMutationCounts(t *testing.T, database *Store, artifactID string) artifactMutationCounts {
	t.Helper()
	var counts artifactMutationCounts
	if err := database.db.QueryRow(`SELECT count(*) FROM artifact_versions WHERE artifact_id = ?`, artifactID).Scan(&counts.Versions); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM artifact_requests`).Scan(&counts.Receipts); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRow(`SELECT count(*) FROM artifact_grants WHERE artifact_id = ? AND revoked_at IS NULL`, artifactID).Scan(&counts.ActiveGrants); err != nil {
		t.Fatal(err)
	}
	return counts
}

func assertArtifactReaderCannotManage(t *testing.T, f *artifactFixture, authentication ArtifactAuthentication, artifact Artifact, now time.Time) {
	t.Helper()
	before := readArtifactMutationCounts(t, f.database, artifact.ID)
	if _, err := f.artifacts.Grant(context.Background(), GrantArtifactParams{
		RequestID: uuid.NewString(), Authentication: authentication, ArtifactID: artifact.ID,
		TargetKind: ArtifactGrantTargetAgent, TargetID: f.agentID, Capability: ArtifactGrantManage, Now: now,
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("reader artifact grant = %v", err)
	}
	if _, err := f.artifacts.Publish(context.Background(), PublishArtifactParams{
		RequestID: uuid.NewString(), Authentication: authentication,
		ArtifactID: artifact.ID, OwningWorkID: artifact.OwningWorkID, Name: artifact.Name, MediaType: artifact.MediaType,
		Summary: "reader must not append", Content: strings.NewReader("must not persist"), Now: now,
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("reader artifact append = %v", err)
	}
	if after := readArtifactMutationCounts(t, f.database, artifact.ID); after != before {
		t.Fatalf("reader changed artifact facts: before=%+v after=%+v", before, after)
	}
}

func expectArtifactSQLFailure(t *testing.T, database *Store, statement string, arguments ...any) {
	t.Helper()
	if _, err := database.db.Exec(statement, arguments...); err == nil {
		t.Fatalf("SQL unexpectedly succeeded: %s", statement)
	}
}

func artifactBlobPath(root string, digest [sha256.Size]byte) string {
	encoded := fmt.Sprintf("%x", digest[:])
	return filepath.Join(root, "objects", encoded[:2], encoded)
}
