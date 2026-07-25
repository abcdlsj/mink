package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	artifactv1 "github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/artifact/v1/artifactv1connect"
	grantv1 "github.com/abcdlsj/sumi/gen/go/sumi/grant/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/grant/v1/grantv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/inbox/v1/inboxv1connect"
	runv1 "github.com/abcdlsj/sumi/gen/go/sumi/run/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/run/v1/runv1connect"
	"github.com/abcdlsj/sumi/gen/go/sumi/runtime/v1/runtimev1connect"
	spacev1 "github.com/abcdlsj/sumi/gen/go/sumi/space/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/space/v1/spacev1connect"
	artifactblob "github.com/abcdlsj/sumi/internal/artifact/blob"
	authorityapp "github.com/abcdlsj/sumi/internal/authority/application"
	"github.com/abcdlsj/sumi/internal/authority/localauth"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestArtifactHTTPHumanStreamingACLReplayPaginationRestartAndMissing(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	seed := seedArtifactWork(t, api.app, dataRoot)
	client := artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL)
	content := bytes.Repeat([]byte("artifact-stream-"), 9000)
	metadata := artifactPublishMetadata(seed.work.ID, "streamed-report", content)
	metadata.Sources = []*artifactv1.ArtifactSourceInput{{
		Source: &artifactv1.ArtifactSourceInput_MessageId{MessageId: seed.message.ID},
	}}
	first, err := publishArtifactHTTP(context.Background(), client, seed.credential, "", metadata, artifactChunks(content)...)
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if first.Msg.GetArtifact().GetOwningWorkId() != seed.work.ID || first.Msg.GetVersion().GetVersion() != 1 ||
		first.Msg.GetVersion().GetSize() != int64(len(content)) || first.Msg.GetVersion().GetExecution() != nil {
		api.close(t)
		t.Fatalf("human publish = %+v", first.Msg)
	}
	replayed, err := publishArtifactHTTP(context.Background(), client, seed.credential, "", metadata, artifactChunks(content)...)
	if err != nil || !proto.Equal(first.Msg, replayed.Msg) {
		api.close(t)
		t.Fatalf("publish replay = %+v, %v", replayed, err)
	}
	changed := append([]byte(nil), content...)
	changed[len(changed)-1] ^= 1
	changedMetadata := proto.Clone(metadata).(*artifactv1.PublishArtifactMetadata)
	changedDigest := sha256.Sum256(changed)
	changedMetadata.DeclaredDigest = changedDigest[:]
	_, err = publishArtifactHTTP(context.Background(), client, seed.credential, "", changedMetadata, artifactChunks(changed)...)
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		api.close(t)
		t.Fatalf("changed replay error = %v", err)
	}
	assertQuietArtifactError(t, err, string(changed), dataRoot, seed.credential)

	secondContent := []byte("second artifact")
	secondMetadata := artifactPublishMetadata(seed.work.ID, "second-report", secondContent)
	second, err := publishArtifactHTTP(context.Background(), client, seed.credential, "", secondMetadata, secondContent)
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	ownerClient := artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL, browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", ""))
	if _, err := ownerClient.ListArtifacts(context.Background(), connect.NewRequest(&artifactv1.ListArtifactsRequest{Limit: 201})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		api.close(t)
		t.Fatalf("artifact list oversized limit = %v", err)
	}
	if _, err := ownerClient.ListArtifacts(context.Background(), connect.NewRequest(&artifactv1.ListArtifactsRequest{AfterArtifactId: "not-a-uuid"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		api.close(t)
		t.Fatalf("artifact list invalid cursor = %v", err)
	}
	pageOne, err := ownerClient.ListArtifacts(context.Background(), connect.NewRequest(&artifactv1.ListArtifactsRequest{Limit: 1}))
	if err != nil || len(pageOne.Msg.GetViews()) != 1 || pageOne.Msg.GetNextArtifactId() == "" {
		api.close(t)
		t.Fatalf("artifact page one = %+v, %v", pageOne, err)
	}
	pageTwo, err := ownerClient.ListArtifacts(context.Background(), connect.NewRequest(&artifactv1.ListArtifactsRequest{
		AfterArtifactId: pageOne.Msg.GetNextArtifactId(), Limit: 1,
	}))
	if err != nil || len(pageTwo.Msg.GetViews()) != 1 || pageTwo.Msg.GetNextArtifactId() != "" ||
		pageTwo.Msg.GetViews()[0].GetArtifact().GetId() == pageOne.Msg.GetViews()[0].GetArtifact().GetId() {
		api.close(t)
		t.Fatalf("artifact page two = %+v, %v", pageTwo, err)
	}
	if pageOne.Msg.GetViews()[0].GetArtifact().GetId() != first.Msg.GetArtifact().GetId() ||
		pageTwo.Msg.GetViews()[0].GetArtifact().GetId() != second.Msg.GetArtifact().GetId() {
		api.close(t)
		t.Fatalf("artifact pagination order = %+v / %+v", pageOne.Msg, pageTwo.Msg)
	}

	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewRuntimeServiceClient(api.http.Client(), api.http.URL)
	oldSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	grantResponse, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(&artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: first.Msg.GetArtifact().GetId(),
		Target: artifactAgentTarget(agent.GetId()), Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
	}))
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := client.GetArtifact(context.Background(), artifactRequest(oldSession.GetToken(), &artifactv1.GetArtifactRequest{
		ArtifactId: first.Msg.GetArtifact().GetId(),
	})); err != nil {
		api.close(t)
		t.Fatalf("agent artifact read = %v", err)
	}
	currentSession := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	if _, err := client.GetArtifact(context.Background(), artifactRequest(oldSession.GetToken(), &artifactv1.GetArtifactRequest{
		ArtifactId: first.Msg.GetArtifact().GetId(),
	})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		api.close(t)
		t.Fatalf("replaced runtime artifact read = %v", err)
	}
	if _, err := client.GetArtifact(context.Background(), artifactRequest(currentSession.GetToken(), &artifactv1.GetArtifactRequest{
		ArtifactId: first.Msg.GetArtifact().GetId(),
	})); err != nil {
		api.close(t)
		t.Fatalf("current runtime artifact read = %v", err)
	}
	if _, err := ownerClient.RevokeArtifactGrant(context.Background(), connect.NewRequest(&artifactv1.RevokeArtifactGrantRequest{
		RequestId: uuid.NewString(), GrantId: grantResponse.Msg.GetGrant().GetId(),
	})); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if _, err := client.GetArtifact(context.Background(), artifactRequest(currentSession.GetToken(), &artifactv1.GetArtifactRequest{
		ArtifactId: first.Msg.GetArtifact().GetId(),
	})); connect.CodeOf(err) != connect.CodePermissionDenied {
		api.close(t)
		t.Fatalf("revoked artifact read = %v", err)
	}

	fetched := fetchArtifactHTTP(t, client, seed.credential, first.Msg.GetArtifact().GetId(), 1)
	if !bytes.Equal(fetched.content, content) || fetched.metadata.GetView().GetVersion().GetVersion() != 1 {
		api.close(t)
		t.Fatalf("fetched artifact = %d bytes / %+v", len(fetched.content), fetched.metadata)
	}
	api.close(t)
	api = openFactsAPI(t, dataRoot)
	client = artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL)
	fetched = fetchArtifactHTTP(t, client, seed.credential, first.Msg.GetArtifact().GetId(), 1)
	if !bytes.Equal(fetched.content, content) {
		api.close(t)
		t.Fatal("restarted artifact content differs")
	}
	api.close(t)

	objectPath := artifactObjectPath(dataRoot, first.Msg.GetVersion().GetDigest())
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	api = openFactsAPI(t, dataRoot)
	defer api.close(t)
	client = artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL, browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", ""))
	missing, err := client.GetArtifact(context.Background(), connect.NewRequest(&artifactv1.GetArtifactRequest{ArtifactId: first.Msg.GetArtifact().GetId()}))
	if err != nil || missing.Msg.GetView().GetVersion().GetIntegrityState() != artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_MISSING {
		t.Fatalf("missing artifact projection = %+v, %v", missing, err)
	}
	stream, err := client.FetchArtifact(context.Background(), artifactRequest(seed.credential, &artifactv1.FetchArtifactRequest{ArtifactId: first.Msg.GetArtifact().GetId()}))
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeDataLoss {
		t.Fatalf("missing artifact fetch error = %v", err)
	}
	assertQuietArtifactError(t, err, string(content), objectPath, seed.credential)
}

func TestArtifactHTTPStreamingValidationBrowserAmbiguityAndCleanup(t *testing.T) {
	dataRoot := t.TempDir()
	api := openBrowserServer(t, dataRoot)
	defer api.close(t)
	seed := seedArtifactWork(t, api.app, dataRoot)
	browser := browserClient(t, api.origin, seed.credential)
	client := artifactv1connect.NewArtifactServiceClient(browser, api.origin)
	content := []byte("browser artifact")
	metadata := artifactPublishMetadata(seed.work.ID, "browser-report", content)

	if _, err := publishArtifactHTTP(context.Background(), client, "", "", metadata, content); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("browser mutation without origin = %v", err)
	}
	published, err := publishArtifactHTTP(context.Background(), client, "", api.origin, metadata, content)
	if err != nil {
		t.Fatal(err)
	}
	ambiguous := connect.NewRequest(&artifactv1.GetArtifactRequest{ArtifactId: published.Msg.GetArtifact().GetId()})
	ambiguous.Header().Set("Authorization", "Bearer "+seed.credential)
	if _, err := client.GetArtifact(context.Background(), ambiguous); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("browser plus bearer identity = %v", err)
	}
	if _, err := client.GetArtifact(context.Background(), connect.NewRequest(&artifactv1.GetArtifactRequest{ArtifactId: published.Msg.GetArtifact().GetId()})); err != nil {
		t.Fatalf("browser artifact read = %v", err)
	}

	before := listArtifactsHTTP(t, client)
	stream := client.PublishArtifact(context.Background())
	stream.RequestHeader().Set("Origin", api.origin)
	if err := stream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Chunk{Chunk: []byte("before metadata")}}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.CloseAndReceive()
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("content before metadata = %v", err)
	}
	assertArtifactListCount(t, client, before)

	missingDigest := proto.Clone(metadata).(*artifactv1.PublishArtifactMetadata)
	missingDigest.RequestId = uuid.NewString()
	missingDigest.DeclaredDigest = nil
	if _, err := publishArtifactHTTP(context.Background(), client, "", api.origin, missingDigest, content); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("missing declared digest = %v", err)
	}
	assertArtifactListCount(t, client, before)

	repeated := client.PublishArtifact(context.Background())
	repeated.RequestHeader().Set("Origin", api.origin)
	duplicateMetadata := proto.Clone(metadata).(*artifactv1.PublishArtifactMetadata)
	duplicateMetadata.RequestId = uuid.NewString()
	for range 2 {
		if err := repeated.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Metadata{Metadata: duplicateMetadata}}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = repeated.CloseAndReceive()
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("repeated metadata = %v", err)
	}
	assertArtifactListCount(t, client, before)

	oversized := proto.Clone(metadata).(*artifactv1.PublishArtifactMetadata)
	oversized.RequestId = uuid.NewString()
	oversized.DeclaredSize = artifactblob.MaxChunkSize + 1
	oversized.DeclaredDigest = make([]byte, sha256.Size)
	if _, err := publishArtifactHTTP(context.Background(), client, "", api.origin, oversized, make([]byte, artifactblob.MaxChunkSize+1)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("oversized chunk = %v", err)
	}
	assertArtifactListCount(t, client, before)

	emptyChunk := proto.Clone(metadata).(*artifactv1.PublishArtifactMetadata)
	emptyChunk.RequestId = uuid.NewString()
	if _, err := publishArtifactHTTP(context.Background(), client, "", api.origin, emptyChunk, nil); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("empty chunk = %v", err)
	}
	assertArtifactListCount(t, client, before)
	assertArtifactStagingEmpty(t, dataRoot)
}

func TestArtifactHTTPDeclaredMismatchLimitCancelAndOrphanReconcile(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	seed := seedArtifactWork(t, api.app, dataRoot)
	client := artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL, browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", ""))
	before := readArtifactHTTPMutationCounts(t, dataRoot)

	actual := []byte("mismatched artifact body")
	declared := []byte("different declared body")
	mismatch := artifactPublishMetadata(seed.work.ID, "mismatch-report", declared)
	_, err := publishArtifactHTTP(context.Background(), client, seed.credential, "", mismatch, actual)
	if connect.CodeOf(err) != connect.CodeDataLoss {
		api.close(t)
		t.Fatalf("declared content mismatch = %v", err)
	}
	assertQuietArtifactError(t, err, string(actual), dataRoot, seed.credential)
	assertArtifactHTTPMutationCounts(t, dataRoot, before)
	actualDigest := sha256.Sum256(actual)
	orphanPath := artifactObjectPath(dataRoot, actualDigest[:])
	if _, err := os.Stat(orphanPath); err != nil {
		api.close(t)
		t.Fatalf("mismatch orphan = %v", err)
	}

	limitMetadata := artifactPublishMetadata(seed.work.ID, "limit-report", nil)
	limitMetadata.DeclaredSize = artifactblob.MaxBlobSize
	limitMetadata.DeclaredDigest = make([]byte, sha256.Size)
	chunks := make([][]byte, 0, artifactblob.MaxBlobSize/artifactblob.MaxChunkSize+1)
	chunk := make([]byte, artifactblob.MaxChunkSize)
	for range artifactblob.MaxBlobSize / artifactblob.MaxChunkSize {
		chunks = append(chunks, chunk)
	}
	chunks = append(chunks, []byte{1})
	_, err = publishArtifactHTTP(context.Background(), client, seed.credential, "", limitMetadata, chunks...)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		api.close(t)
		t.Fatalf("artifact total limit = %v", err)
	}
	assertQuietArtifactError(t, err, dataRoot, seed.credential)
	assertArtifactHTTPMutationCounts(t, dataRoot, before)
	assertArtifactStagingEmptyEventually(t, dataRoot)

	cancelContext, cancel := context.WithCancel(context.Background())
	cancelStream := client.PublishArtifact(cancelContext)
	cancelStream.RequestHeader().Set("Authorization", "Bearer "+seed.credential)
	cancelMetadata := artifactPublishMetadata(seed.work.ID, "cancel-report", nil)
	cancelMetadata.DeclaredSize = artifactblob.MaxBlobSize
	cancelMetadata.DeclaredDigest = make([]byte, sha256.Size)
	if err := cancelStream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Metadata{Metadata: cancelMetadata}}); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	if err := cancelStream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Chunk{Chunk: chunk}}); err != nil {
		api.close(t)
		t.Fatal(err)
	}
	waitForArtifactStaging(t, dataRoot, false)
	cancel()
	_, err = cancelStream.CloseAndReceive()
	if connect.CodeOf(err) != connect.CodeCanceled {
		api.close(t)
		t.Fatalf("canceled artifact upload = %v", err)
	}
	assertQuietArtifactError(t, err, dataRoot, seed.credential)
	assertArtifactStagingEmptyEventually(t, dataRoot)
	assertArtifactHTTPMutationCounts(t, dataRoot, before)

	api.close(t)
	api = openFactsAPI(t, dataRoot)
	defer api.close(t)
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("orphan after reconcile = %v", err)
	}
	quarantine, err := os.ReadDir(filepath.Join(dataRoot, "data", "artifacts", "quarantine"))
	if err != nil || len(quarantine) == 0 {
		t.Fatalf("artifact quarantine after mismatch = %v, %v", quarantine, err)
	}
	assertArtifactHTTPMutationCounts(t, dataRoot, before)
}

func TestArtifactHTTPAgentExecutionCurrentACLGrantReplayAndPagination(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	defer api.close(t)
	seed := seedArtifactWork(t, api.app, dataRoot)
	computer, agent, placement, registrationKey := createActiveRuntimeBinding(t, api)
	runtimeClient := runtimev1connect.NewRuntimeServiceClient(api.http.Client(), api.http.URL)
	session := createRuntimeOverHTTP(t, runtimeClient, computer.GetId(), registrationKey, agent.GetId(), placement.GetDesiredRevision())
	ownerOption := browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", "")
	grantClient := grantv1connect.NewGrantServiceClient(api.http.Client(), api.http.URL, ownerOption)
	collaborationClient := spacev1connect.NewCollaborationServiceClient(api.http.Client(), api.http.URL, ownerOption)
	if _, err := collaborationClient.AddMember(context.Background(), connect.NewRequest(&spacev1.AddMemberRequest{
		RequestId: uuid.NewString(), SpaceId: seed.space.ID,
		Member: &spacev1.Principal{Kind: spacev1.PrincipalKind_PRINCIPAL_KIND_AGENT, Id: agent.GetId()},
	})); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		capability grantv1.Capability
		scope      *grantv1.Scope
	}{
		{grantv1.Capability_CAPABILITY_SPACE_READ, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: seed.space.ID}},
		{grantv1.Capability_CAPABILITY_MESSAGE_SEND, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_SPACE, Id: seed.space.ID}},
		{grantv1.Capability_CAPABILITY_RUN_EXECUTE, &grantv1.Scope{Kind: grantv1.ScopeKind_SCOPE_KIND_AGENT, Id: agent.GetId()}},
	} {
		issueRunGrantHTTP(t, grantClient, seed.bootstrap.RootGrant.ID, agent.GetId(), value.capability, value.scope)
	}
	workGrant, err := api.app.store.IssueGrant(context.Background(), store.IssueGrantParams{
		RequestID: uuid.NewString(), Actor: seed.owner,
		Subject:    store.Principal{Kind: "agent", ID: agent.GetId(), OrganizationID: seed.owner.OrganizationID},
		Capability: store.CapabilityWorkManage, Scope: store.Scope{Kind: "work", ID: seed.work.ID},
		ParentGrantID: seed.bootstrap.RootGrant.ID, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	trigger := sendMention(t, collaborationClient, seed.space.ID, agent.GetId(), "artifact execution trigger")
	inboxClient := inboxv1connect.NewInboxServiceClient(api.http.Client(), api.http.URL)
	item := findInboxItem(t, inboxClient, session.GetToken(), trigger.GetId())
	observeInbox(t, inboxClient, session.GetToken(), item.GetTarget())
	runClient := runv1connect.NewRunServiceClient(api.http.Client(), api.http.URL)
	runs, err := runClient.ListRuns(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ListRunsRequest{Limit: 200}))
	if err != nil || len(runs.Msg.GetRuns()) != 1 {
		t.Fatalf("artifact execution runs = %+v, %v", runs, err)
	}
	claimed, err := runClient.ClaimRun(context.Background(), runtimeRequestHTTP(session.GetToken(), &runv1.ClaimRunRequest{
		RequestId: uuid.NewString(), RunId: runs.Msg.GetRuns()[0].GetId(),
	}))
	if err != nil {
		t.Fatal(err)
	}

	client := artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL)
	content := []byte("agent execution artifact")
	metadata := artifactPublishMetadata(seed.work.ID, "agent-execution", content)
	metadata.Execution = &artifactv1.ArtifactExecutionInput{
		RunId: claimed.Msg.GetRun().GetId(), Attempt: claimed.Msg.GetRun().GetAttempt(), Fence: claimed.Msg.GetRun().GetFence(),
	}
	metadata.Sources = []*artifactv1.ArtifactSourceInput{{Source: &artifactv1.ArtifactSourceInput_MessageId{MessageId: trigger.GetId()}}}
	published, err := publishArtifactHTTP(context.Background(), client, session.GetToken(), "", metadata, content)
	if err != nil || published.Msg.GetVersion().GetExecution().GetAgentId() != agent.GetId() ||
		published.Msg.GetVersion().GetExecution().GetComputerId() != computer.GetId() {
		t.Fatalf("agent execution publish = %+v, %v", published, err)
	}
	humanExecution := proto.Clone(metadata).(*artifactv1.PublishArtifactMetadata)
	humanExecution.RequestId = uuid.NewString()
	humanExecution.ArtifactId = ""
	humanExecution.Name = "forged-human-execution"
	if _, err := publishArtifactHTTP(context.Background(), client, seed.credential, "", humanExecution, content); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("human execution forgery = %v", err)
	}

	ownerClient := artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL, ownerOption)
	secondContent := []byte("pagination peer artifact")
	second, err := publishArtifactHTTP(context.Background(), ownerClient, seed.credential, "", artifactPublishMetadata(seed.work.ID, "pagination-peer", secondContent), secondContent)
	if err != nil {
		t.Fatal(err)
	}
	unknownTarget := &artifactv1.ArtifactGrantTarget{}
	unknownTarget.ProtoReflect().SetUnknown(protowire.AppendString(
		protowire.AppendTag(nil, 99, protowire.BytesType), "unknown-target",
	))
	beforeBadTargets := readArtifactHTTPMutationCounts(t, dataRoot)
	for name, target := range map[string]*artifactv1.ArtifactGrantTarget{
		"missing":    nil,
		"empty":      artifactAgentTarget(""),
		"blank":      artifactAgentTarget("   "),
		"invalid id": artifactAgentTarget("not-a-uuid"),
		"unknown":    unknownTarget,
	} {
		_, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(&artifactv1.GrantArtifactRequest{
			RequestId: uuid.NewString(), ArtifactId: published.Msg.GetArtifact().GetId(), Target: target,
			Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("artifact grant %s target = %v", name, err)
		}
	}
	assertArtifactHTTPMutationCounts(t, dataRoot, beforeBadTargets)
	manageResponse, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(&artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: published.Msg.GetArtifact().GetId(),
		Target:     artifactAgentTarget(agent.GetId()),
		Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_MANAGE,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.app.store.RevokeGrant(context.Background(), store.RevokeGrantParams{
		RequestID: uuid.NewString(), Actor: seed.owner, GrantID: workGrant.ID, Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	agentGrantRequest := &artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: published.Msg.GetArtifact().GetId(),
		Target:     artifactAgentTarget(agent.GetId()),
		Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
	}
	agentGrant, err := client.GrantArtifact(context.Background(), artifactRequest(session.GetToken(), agentGrantRequest))
	if err != nil {
		t.Fatal(err)
	}
	agentGrantReplay, err := client.GrantArtifact(context.Background(), artifactRequest(session.GetToken(), agentGrantRequest))
	if err != nil || !proto.Equal(agentGrant.Msg, agentGrantReplay.Msg) {
		t.Fatalf("agent grant replay = %+v, %v", agentGrantReplay, err)
	}
	agentRevokeRequest := &artifactv1.RevokeArtifactGrantRequest{RequestId: uuid.NewString(), GrantId: agentGrant.Msg.GetGrant().GetId()}
	agentRevoke, err := client.RevokeArtifactGrant(context.Background(), artifactRequest(session.GetToken(), agentRevokeRequest))
	if err != nil {
		t.Fatal(err)
	}
	agentRevokeReplay, err := client.RevokeArtifactGrant(context.Background(), artifactRequest(session.GetToken(), agentRevokeRequest))
	if err != nil || !proto.Equal(agentRevoke.Msg, agentRevokeReplay.Msg) {
		t.Fatalf("agent revoke replay = %+v, %v", agentRevokeReplay, err)
	}
	if _, err := ownerClient.RevokeArtifactGrant(context.Background(), connect.NewRequest(&artifactv1.RevokeArtifactGrantRequest{
		RequestId: uuid.NewString(), GrantId: manageResponse.Msg.GetGrant().GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GrantArtifact(context.Background(), artifactRequest(session.GetToken(), agentGrantRequest)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("grant replay after current manage revoke = %v", err)
	}
	if _, err := client.RevokeArtifactGrant(context.Background(), artifactRequest(session.GetToken(), agentRevokeRequest)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("revoke replay after current manage revoke = %v", err)
	}

	runGrant := findGrantByCapability(t, grantClient, agent.GetId(), grantv1.Capability_CAPABILITY_RUN_EXECUTE)
	if _, err := grantClient.RevokeGrant(context.Background(), connect.NewRequest(&grantv1.RevokeGrantRequest{
		RequestId: uuid.NewString(), GrantId: runGrant.GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	readOne := grantArtifactHTTP(t, ownerClient, published.Msg.GetArtifact().GetId(), agent.GetId())
	grantArtifactHTTP(t, ownerClient, second.Msg.GetArtifact().GetId(), agent.GetId())
	view, err := client.GetArtifact(context.Background(), artifactRequest(session.GetToken(), &artifactv1.GetArtifactRequest{ArtifactId: published.Msg.GetArtifact().GetId()}))
	if err != nil || !view.Msg.GetView().GetVersion().GetExecution().GetRestricted() ||
		view.Msg.GetView().GetVersion().GetExecution().GetRunId() != "" || view.Msg.GetView().GetArtifact().GetOwningWorkId() != "" {
		t.Fatalf("restricted execution after revoke = %+v, %v", view, err)
	}
	listed, err := client.ListArtifacts(context.Background(), artifactRequest(session.GetToken(), &artifactv1.ListArtifactsRequest{Limit: 1}))
	if err != nil || len(listed.Msg.GetViews()) != 1 || listed.Msg.GetNextArtifactId() == "" ||
		!listed.Msg.GetViews()[0].GetVersion().GetExecution().GetRestricted() {
		t.Fatalf("restricted artifact list = %+v, %v", listed, err)
	}

	peerResponse, err := api.agents.CreateAgent(context.Background(), connect.NewRequest(agentRequest("artifact-grant-peer")))
	if err != nil {
		t.Fatal(err)
	}
	grantReplayRequest := &artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: second.Msg.GetArtifact().GetId(),
		Target:     artifactAgentTarget(peerResponse.Msg.GetAgent().GetId()),
		Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
	}
	grantReplay, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(grantReplayRequest))
	if err != nil {
		t.Fatal(err)
	}
	grantReplayAgain, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(grantReplayRequest))
	if err != nil || !proto.Equal(grantReplay.Msg, grantReplayAgain.Msg) {
		t.Fatalf("owner grant replay = %+v, %v", grantReplayAgain, err)
	}
	grantConflict := proto.Clone(grantReplayRequest).(*artifactv1.GrantArtifactRequest)
	grantConflict.Target = artifactAgentTarget(uuid.NewString())
	if _, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(grantConflict)); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("owner grant conflict = %v", err)
	}
	variantRequest := &artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: second.Msg.GetArtifact().GetId(),
		Target: artifactWorkTarget(seed.work.ID), Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
	}
	variantGrant, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(variantRequest))
	if err != nil || variantGrant.Msg.GetGrant().GetTarget().GetWorkId() != seed.work.ID {
		t.Fatalf("work variant grant = %+v, %v", variantGrant, err)
	}
	spaceVariant, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(&artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: second.Msg.GetArtifact().GetId(),
		Target: artifactSpaceTarget(seed.space.ID), Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
	}))
	if err != nil || spaceVariant.Msg.GetGrant().GetTarget().GetSpaceId() != seed.space.ID {
		t.Fatalf("space variant grant = %+v, %v", spaceVariant, err)
	}
	normalizedReplay := proto.Clone(variantRequest).(*artifactv1.GrantArtifactRequest)
	normalizedReplay.Target = artifactWorkTarget(strings.ToUpper(seed.work.ID))
	variantReplayed, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(normalizedReplay))
	if err != nil || !proto.Equal(variantGrant.Msg, variantReplayed.Msg) {
		t.Fatalf("normalized work variant replay = %+v, %v", variantReplayed, err)
	}
	for name, target := range map[string]*artifactv1.ArtifactGrantTarget{
		"agent": artifactAgentTarget(seed.work.ID),
		"space": artifactSpaceTarget(seed.work.ID),
	} {
		crossVariant := proto.Clone(variantRequest).(*artifactv1.GrantArtifactRequest)
		crossVariant.Target = target
		if _, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(crossVariant)); connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("cross-variant %s grant replay = %v", name, err)
		}
	}
	beforeInvalid := readArtifactHTTPMutationCounts(t, dataRoot)
	for name, target := range map[string]*artifactv1.ArtifactGrantTarget{
		"space": artifactSpaceTarget(seed.space.ID),
		"work":  artifactWorkTarget(seed.work.ID),
	} {
		_, err := ownerClient.GrantArtifact(context.Background(), connect.NewRequest(&artifactv1.GrantArtifactRequest{
			RequestId: uuid.NewString(), ArtifactId: published.Msg.GetArtifact().GetId(), Target: target,
			Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_MANAGE,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("non-agent manage grant %s = %v", name, err)
		}
	}
	assertArtifactHTTPMutationCounts(t, dataRoot, beforeInvalid)

	revokeRequest := &artifactv1.RevokeArtifactGrantRequest{RequestId: uuid.NewString(), GrantId: readOne.Msg.GetGrant().GetId()}
	revoked, err := ownerClient.RevokeArtifactGrant(context.Background(), connect.NewRequest(revokeRequest))
	if err != nil {
		t.Fatal(err)
	}
	revokedAgain, err := ownerClient.RevokeArtifactGrant(context.Background(), connect.NewRequest(revokeRequest))
	if err != nil || !proto.Equal(revoked.Msg, revokedAgain.Msg) {
		t.Fatalf("owner revoke replay = %+v, %v", revokedAgain, err)
	}
	replacement := grantArtifactHTTP(t, ownerClient, published.Msg.GetArtifact().GetId(), agent.GetId())
	revokeConflict := proto.Clone(revokeRequest).(*artifactv1.RevokeArtifactGrantRequest)
	revokeConflict.GrantId = replacement.Msg.GetGrant().GetId()
	if _, err := ownerClient.RevokeArtifactGrant(context.Background(), connect.NewRequest(revokeConflict)); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("owner revoke conflict = %v", err)
	}
	if _, err := ownerClient.RevokeArtifactGrant(context.Background(), connect.NewRequest(&artifactv1.RevokeArtifactGrantRequest{
		RequestId: uuid.NewString(), GrantId: replacement.Msg.GetGrant().GetId(),
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetArtifact(context.Background(), artifactRequest(session.GetToken(), &artifactv1.GetArtifactRequest{ArtifactId: published.Msg.GetArtifact().GetId()})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("artifact get after current read revoke = %v", err)
	}
	if _, err := client.ListArtifacts(context.Background(), artifactRequest(session.GetToken(), &artifactv1.ListArtifactsRequest{
		AfterArtifactId: listed.Msg.GetNextArtifactId(), Limit: 1,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("artifact cursor after ACL revoke = %v", err)
	}
	if _, err := client.GetArtifact(context.Background(), artifactRequest(session.GetToken(), &artifactv1.GetArtifactRequest{ArtifactId: second.Msg.GetArtifact().GetId()})); err != nil {
		t.Fatalf("remaining exact artifact read = %v", err)
	}
}

func TestArtifactHTTPCorruptObjectDoesNotBlockRestart(t *testing.T) {
	dataRoot := t.TempDir()
	api := openFactsAPI(t, dataRoot)
	seed := seedArtifactWork(t, api.app, dataRoot)
	client := artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL)
	content := []byte("artifact content corrupted after commit")
	published, err := publishArtifactHTTP(context.Background(), client, seed.credential, "", artifactPublishMetadata(seed.work.ID, "corrupt-report", content), content)
	if err != nil {
		api.close(t)
		t.Fatal(err)
	}
	api.close(t)
	objectPath := artifactObjectPath(dataRoot, published.Msg.GetVersion().GetDigest())
	corrupt := bytes.Repeat([]byte{'x'}, len(content))
	if err := os.WriteFile(objectPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	api = openFactsAPI(t, dataRoot)
	defer api.close(t)
	client = artifactv1connect.NewArtifactServiceClient(api.http.Client(), api.http.URL, browserSessionAuth("abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP", ""))
	response, err := client.GetArtifact(context.Background(), connect.NewRequest(&artifactv1.GetArtifactRequest{ArtifactId: published.Msg.GetArtifact().GetId()}))
	if err != nil || response.Msg.GetView().GetVersion().GetIntegrityState() != artifactv1.ArtifactIntegrityState_ARTIFACT_INTEGRITY_STATE_CORRUPT {
		t.Fatalf("corrupt artifact projection = %+v, %v", response, err)
	}
	stream, err := client.FetchArtifact(context.Background(), artifactRequest(seed.credential, &artifactv1.FetchArtifactRequest{ArtifactId: published.Msg.GetArtifact().GetId()}))
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeDataLoss {
		t.Fatalf("corrupt artifact fetch = %v", err)
	}
	assertQuietArtifactError(t, err, string(content), string(corrupt), objectPath, seed.credential)
}

type artifactHTTPMutationCounts struct {
	artifacts       int
	versions        int
	grants          int
	receipts        int
	committedAudits int
}

func readArtifactHTTPMutationCounts(t *testing.T, dataRoot string) artifactHTTPMutationCounts {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var counts artifactHTTPMutationCounts
	queries := []struct {
		query string
		value *int
	}{
		{`SELECT count(*) FROM artifacts`, &counts.artifacts},
		{`SELECT count(*) FROM artifact_versions`, &counts.versions},
		{`SELECT count(*) FROM artifact_grants`, &counts.grants},
		{`SELECT count(*) FROM artifact_requests`, &counts.receipts},
		{`SELECT count(*) FROM audit_events WHERE action LIKE 'artifact.%' AND outcome = 'committed'`, &counts.committedAudits},
	}
	for _, query := range queries {
		if err := database.QueryRow(query.query).Scan(query.value); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func assertArtifactHTTPMutationCounts(t *testing.T, dataRoot string, want artifactHTTPMutationCounts) {
	t.Helper()
	if got := readArtifactHTTPMutationCounts(t, dataRoot); got != want {
		t.Fatalf("artifact mutation counts = %+v, want %+v", got, want)
	}
}

func grantArtifactHTTP(t *testing.T, client artifactv1connect.ArtifactServiceClient, artifactID, agentID string) *connect.Response[artifactv1.GrantArtifactResponse] {
	t.Helper()
	response, err := client.GrantArtifact(context.Background(), connect.NewRequest(&artifactv1.GrantArtifactRequest{
		RequestId: uuid.NewString(), ArtifactId: artifactID,
		Target:     artifactAgentTarget(agentID),
		Capability: artifactv1.ArtifactCapability_ARTIFACT_CAPABILITY_READ,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Msg.GetGrant().GetTarget().GetAgentId() != agentID {
		t.Fatalf("artifact agent target = %+v", response.Msg.GetGrant().GetTarget())
	}
	return response
}

func artifactAgentTarget(agentID string) *artifactv1.ArtifactGrantTarget {
	return &artifactv1.ArtifactGrantTarget{Target: &artifactv1.ArtifactGrantTarget_AgentId{AgentId: agentID}}
}

func artifactSpaceTarget(spaceID string) *artifactv1.ArtifactGrantTarget {
	return &artifactv1.ArtifactGrantTarget{Target: &artifactv1.ArtifactGrantTarget_SpaceId{SpaceId: spaceID}}
}

func artifactWorkTarget(workID string) *artifactv1.ArtifactGrantTarget {
	return &artifactv1.ArtifactGrantTarget{Target: &artifactv1.ArtifactGrantTarget_WorkId{WorkId: workID}}
}

func findGrantByCapability(t *testing.T, client grantv1connect.GrantServiceClient, agentID string, capability grantv1.Capability) *grantv1.Grant {
	t.Helper()
	response, err := client.ListGrants(context.Background(), connect.NewRequest(&grantv1.ListGrantsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range response.Msg.GetGrants() {
		if grant.GetSubject().GetKind() == grantv1.PrincipalKind_PRINCIPAL_KIND_AGENT &&
			grant.GetSubject().GetId() == agentID && grant.GetCapability() == capability && grant.GetRevokedAt() == nil {
			return grant
		}
	}
	t.Fatalf("active agent grant %s not found", capability)
	return nil
}

func waitForArtifactStaging(t *testing.T, dataRoot string, wantEmpty bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(filepath.Join(dataRoot, "data", "artifacts", "staging"))
		if err != nil {
			t.Fatal(err)
		}
		if (len(entries) == 0) == wantEmpty {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("artifact staging empty = %t, want %t", len(entries) == 0, wantEmpty)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertArtifactStagingEmptyEventually(t *testing.T, dataRoot string) {
	t.Helper()
	waitForArtifactStaging(t, dataRoot, true)
}

type artifactSeed struct {
	credential string
	bootstrap  store.AuthorityBootstrap
	owner      store.Principal
	space      store.Space
	message    store.Message
	work       store.Work
}

func seedArtifactWork(t *testing.T, app *Server, dataRoot string) artifactSeed {
	t.Helper()
	now := time.Now()
	password := "artifact-test-password-1234567890"
	digest, hashErr := localauth.HashPassword(rand.Reader, password, localauth.DefaultPasswordParameters())
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	sessionToken := "abcdefghijklmnopqrstuvwxyz-ABCDEFGHIJKLMNOP"
	bootstrap, err := app.store.RegisterFirstOwner(context.Background(), authorityapp.RegisterFirstOwnerCommand{
		RequestID: uuid.NewString(), Name: "Owner",
		Identity:         authorityapp.AuthenticationIdentity{Provider: "local", Subject: "owner"},
		Password:         digest,
		SessionToken:     sessionToken,
		Now:              now,
		SessionExpiresAt: now.Add(12 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := app.store.CreateGroup(context.Background(), store.CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Artifact HTTP", Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := app.store.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID},
		Body: "artifact HTTP source", Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	work, err := app.store.CreateWork(context.Background(), store.WorkCreateParams{
		RequestID: uuid.NewString(), Actor: owner, SourceMessageID: message.ID, SourceSpaceID: message.SpaceID,
		SourceTarget: message.Target, SourceTargetSequence: message.TargetSequence,
		Goal: "publish durable artifact", AcceptanceCriteria: []string{"artifact is fetchable"}, Now: now.Add(3 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return artifactSeed{credential: credential, bootstrap: bootstrap, owner: owner, space: space, message: message, work: work}
}

func artifactPublishMetadata(workID, name string, content []byte) *artifactv1.PublishArtifactMetadata {
	digest := sha256.Sum256(content)
	return &artifactv1.PublishArtifactMetadata{
		RequestId: uuid.NewString(), OwningWorkId: workID, Name: name, MediaType: "application/octet-stream",
		Summary: "artifact HTTP summary", DeclaredSize: int64(len(content)), DeclaredDigest: digest[:],
	}
}

func artifactChunks(content []byte) [][]byte {
	chunks := make([][]byte, 0, (len(content)+artifactblob.MaxChunkSize-1)/artifactblob.MaxChunkSize)
	for len(content) > 0 {
		size := min(len(content), artifactblob.MaxChunkSize)
		chunks = append(chunks, content[:size])
		content = content[size:]
	}
	return chunks
}

func publishArtifactHTTP(ctx context.Context, client artifactv1connect.ArtifactServiceClient, token, origin string, metadata *artifactv1.PublishArtifactMetadata, chunks ...[]byte) (*connect.Response[artifactv1.PublishArtifactResponse], error) {
	stream := client.PublishArtifact(ctx)
	if token != "" {
		stream.RequestHeader().Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		stream.RequestHeader().Set("Origin", origin)
	}
	if metadata != nil {
		if err := stream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Metadata{Metadata: metadata}}); err != nil {
			return nil, err
		}
	}
	for _, chunk := range chunks {
		if err := stream.Send(&artifactv1.PublishArtifactRequest{Payload: &artifactv1.PublishArtifactRequest_Chunk{Chunk: chunk}}); err != nil {
			break
		}
	}
	return stream.CloseAndReceive()
}

type fetchedArtifact struct {
	metadata *artifactv1.FetchArtifactMetadata
	content  []byte
}

func fetchArtifactHTTP(t *testing.T, client artifactv1connect.ArtifactServiceClient, token, artifactID string, version uint64) fetchedArtifact {
	t.Helper()
	stream, err := client.FetchArtifact(context.Background(), artifactRequest(token, &artifactv1.FetchArtifactRequest{ArtifactId: artifactID, Version: version}))
	if err != nil {
		t.Fatal(err)
	}
	var result fetchedArtifact
	for stream.Receive() {
		message := stream.Msg()
		switch payload := message.GetPayload().(type) {
		case *artifactv1.FetchArtifactResponse_Metadata:
			if result.metadata != nil || len(result.content) != 0 {
				t.Fatal("artifact fetch metadata was not the unique first frame")
			}
			result.metadata = payload.Metadata
		case *artifactv1.FetchArtifactResponse_Chunk:
			if result.metadata == nil || len(payload.Chunk) == 0 || len(payload.Chunk) > artifactblob.MaxChunkSize {
				t.Fatalf("invalid artifact fetch chunk size = %d", len(payload.Chunk))
			}
			result.content = append(result.content, payload.Chunk...)
		default:
			t.Fatal("artifact fetch frame is empty")
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if result.metadata == nil {
		t.Fatal("artifact fetch metadata is missing")
	}
	return result
}

func artifactRequest[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func listArtifactsHTTP(t *testing.T, client artifactv1connect.ArtifactServiceClient) int {
	t.Helper()
	response, err := client.ListArtifacts(context.Background(), connect.NewRequest(&artifactv1.ListArtifactsRequest{Limit: 200}))
	if err != nil {
		t.Fatal(err)
	}
	return len(response.Msg.GetViews())
}

func assertArtifactListCount(t *testing.T, client artifactv1connect.ArtifactServiceClient, want int) {
	t.Helper()
	if got := listArtifactsHTTP(t, client); got != want {
		t.Fatalf("artifact list count = %d, want %d", got, want)
	}
}

func assertArtifactStagingEmpty(t *testing.T, dataRoot string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataRoot, "data", "artifacts", "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("artifact staging entries = %v", entries)
	}
}

func artifactObjectPath(dataRoot string, digest []byte) string {
	encoded := hex.EncodeToString(digest)
	return filepath.Join(dataRoot, "data", "artifacts", "objects", encoded[:2], encoded)
}

func assertQuietArtifactError(t *testing.T, err error, privateValues ...string) {
	t.Helper()
	for _, value := range privateValues {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("artifact error leaked private value: %v", err)
		}
	}
}
