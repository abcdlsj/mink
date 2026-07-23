package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	knowledgev1 "github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/knowledge/v1/knowledgev1connect"
	systemv1 "github.com/abcdlsj/sumi/gen/go/sumi/system/v1"
	"github.com/abcdlsj/sumi/gen/go/sumi/system/v1/systemv1connect"
	artifactblob "github.com/abcdlsj/sumi/internal/artifact/blob"
	"github.com/abcdlsj/sumi/internal/authority"
	"github.com/abcdlsj/sumi/internal/store"
	"github.com/google/uuid"
)

func TestBootstrapUsesPersistentIdentity(t *testing.T) {
	dataRoot := t.TempDir()
	first := requestBootstrap(t, dataRoot)
	second := requestBootstrap(t, dataRoot)
	if first.GetServerId() != second.GetServerId() {
		t.Fatalf("server id changed from %q to %q", first.GetServerId(), second.GetServerId())
	}
	if first.GetVersion() == "" {
		t.Fatal("version is empty")
	}
	if len(first.GetPlatforms()) != 2 {
		t.Fatalf("platforms = %v", first.GetPlatforms())
	}
}

func requestBootstrap(t *testing.T, dataRoot string) *systemv1.GetBootstrapResponse {
	t.Helper()
	server, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	client := systemv1connect.NewSystemServiceClient(httpServer.Client(), httpServer.URL)
	response, err := client.GetBootstrap(context.Background(), connect.NewRequest(&systemv1.GetBootstrapRequest{}))
	httpServer.Close()
	if closeErr := server.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	return response.Msg
}

func TestServesProductionWeb(t *testing.T) {
	webRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<main>Sumi production shell</main>"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := New(context.Background(), Config{DataRoot: t.TempDir(), WebRoot: webRoot})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	response, err := httpServer.Client().Get(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	httpServer.Close()
	if closeErr := server.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Sumi production shell") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestKnowledgeSearchProjectionSurvivesServerReopenAndCloseFailsControlled(t *testing.T) {
	dataRoot := t.TempDir()
	app, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	app.knowledge.Close()
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	bootstrap, err := app.store.EnsureAuthority(context.Background(), credential, time.Now())
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	peerCredential := strings.Repeat("p", 43)
	if _, err := app.store.CreateHuman(context.Background(), store.CreateHumanParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "Knowledge Search Peer", Role: "member", Credential: peerCredential, Now: time.Now(),
	}); err != nil {
		app.Close()
		t.Fatal(err)
	}
	space, err := app.store.CreateGroup(context.Background(), store.CreateGroupParams{
		RequestID: uuid.NewString(), Actor: owner, Name: "knowledge transport", Now: time.Now(),
	})
	if err != nil {
		app.Close()
		t.Fatal(err)
	}
	messageIDs := make(map[string]struct{}, 2)
	for _, body := range []string{"transport search alpha", "transport search beta"} {
		message, err := app.store.SendMessage(context.Background(), store.SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID}, Body: body, Now: time.Now(),
		})
		if err != nil {
			app.Close()
			t.Fatal(err)
		}
		messageIDs[message.ID] = struct{}{}
	}
	if err := app.store.RebuildKnowledgeIndex(context.Background()); err != nil {
		app.Close()
		t.Fatal(err)
	}

	httpServer := httptest.NewServer(app.Handler())
	client := knowledgev1connect.NewKnowledgeServiceClient(httpServer.Client(), httpServer.URL, clientAuthorization(credential))
	first, err := client.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "transport search", Limit: 1}))
	if err != nil || len(first.Msg.GetResults()) != 1 {
		httpServer.Close()
		app.Close()
		t.Fatalf("first knowledge search = %+v, %v", first, err)
	}
	firstID := first.Msg.GetResults()[0].GetCitation().GetMessage().GetMessageId()
	if _, ok := messageIDs[firstID]; !ok {
		httpServer.Close()
		app.Close()
		t.Fatalf("first citation = %q", firstID)
	}
	peerClient := knowledgev1connect.NewKnowledgeServiceClient(httpServer.Client(), httpServer.URL, clientAuthorization(peerCredential))
	peerOutput, err := peerClient.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "transport search"}))
	if err != nil || len(peerOutput.Msg.GetResults()) != 0 {
		httpServer.Close()
		app.Close()
		t.Fatalf("non-member search = %+v, %v", peerOutput, err)
	}
	httpServer.Close()
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	reopenedHTTP := httptest.NewServer(reopened.Handler())
	reopenedClient := knowledgev1connect.NewKnowledgeServiceClient(reopenedHTTP.Client(), reopenedHTTP.URL, clientAuthorization(credential))
	second, err := reopenedClient.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "transport search", Limit: 1}))
	if err != nil || len(second.Msg.GetResults()) != 1 {
		reopenedHTTP.Close()
		reopened.Close()
		t.Fatalf("reopened knowledge search = %+v, %v", second, err)
	}
	secondID := second.Msg.GetResults()[0].GetCitation().GetMessage().GetMessageId()
	if _, ok := messageIDs[secondID]; !ok {
		reopenedHTTP.Close()
		reopened.Close()
		t.Fatalf("reopened citations = first:%q second:%q", firstID, secondID)
	}

	if err := reopened.Close(); err != nil {
		reopenedHTTP.Close()
		t.Fatal(err)
	}
	_, err = reopenedClient.SearchKnowledge(context.Background(), connect.NewRequest(&knowledgev1.SearchKnowledgeRequest{Query: "transport search"}))
	assertKnowledgeConnectError(t, err, connect.CodeInternal, "knowledge service unavailable")
	reopenedHTTP.Close()
}

func TestKnowledgeRunnerDrainsConcurrentFactsAndServerReopens(t *testing.T) {
	dataRoot := t.TempDir()
	server, err := New(context.Background(), Config{DataRoot: dataRoot})
	if err != nil {
		t.Fatal(err)
	}
	waitForKnowledgeRunnerReady(t, dataRoot)
	credential, err := authority.ReadCredentialFile(filepath.Join(dataRoot, "owner.key"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	bootstrap, err := server.store.EnsureAuthority(context.Background(), credential, time.Now())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	owner := store.Principal{Kind: "human", ID: bootstrap.Human.ID, OrganizationID: bootstrap.Organization.ID}
	space, err := server.store.CreateGroup(context.Background(), store.CreateGroupParams{RequestID: uuid.NewString(), Actor: owner, Name: "knowledge lifecycle", Now: time.Now()})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	seed, err := server.store.SendMessage(context.Background(), store.SendMessageParams{
		RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID}, Body: "knowledge artifact source", Now: time.Now(),
	})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	work, err := server.store.CreateWork(context.Background(), store.WorkCreateParams{
		RequestID: uuid.NewString(), Actor: owner, SourceMessageID: seed.ID, SourceSpaceID: seed.SpaceID, SourceTarget: seed.Target,
		SourceTargetSequence: seed.TargetSequence, Goal: "publish artifact", AcceptanceCriteria: []string{"artifact durable"}, Now: time.Now(),
	})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	blobs, err := artifactblob.OpenLocal(filepath.Join(dataRoot, "data", "artifacts"))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	artifacts, err := store.NewArtifactStore(server.store, blobs, store.ArtifactMaxBlobSize)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	server.knowledge.Start(context.Background())

	start := make(chan struct{})
	errs := make(chan error, 3)
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		<-start
		_, err := server.store.SendMessage(context.Background(), store.SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID}, Body: "knowledge concurrent message", Now: time.Now(),
		})
		errs <- err
	}()
	go func() {
		defer wait.Done()
		<-start
		source, err := server.store.SendMessage(context.Background(), store.SendMessageParams{
			RequestID: uuid.NewString(), Actor: owner, Target: store.MessageTarget{Kind: store.MessageTargetSpace, ID: space.ID}, Body: "knowledge concurrent work source", Now: time.Now(),
		})
		if err == nil {
			_, err = server.store.CreateWork(context.Background(), store.WorkCreateParams{
				RequestID: uuid.NewString(), Actor: owner, SourceMessageID: source.ID, SourceSpaceID: source.SpaceID, SourceTarget: source.Target,
				SourceTargetSequence: source.TargetSequence, Goal: "concurrent work", AcceptanceCriteria: []string{"complete"}, Now: time.Now(),
			})
		}
		errs <- err
	}()
	go func() {
		defer wait.Done()
		<-start
		_, err := artifacts.Publish(context.Background(), store.PublishArtifactParams{
			RequestID: uuid.NewString(), Authentication: store.ArtifactAuthentication{Human: owner}, OwningWorkID: work.ID,
			Name: "knowledge concurrent artifact", MediaType: "text/plain", Summary: "concurrent artifact", Content: strings.NewReader("knowledge artifact body"), Now: time.Now(),
		})
		errs <- err
	}()
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
	}
	waitForKnowledgeRunnerDrain(t, dataRoot)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		reopened, err := New(context.Background(), Config{DataRoot: dataRoot})
		if err != nil {
			t.Fatal(err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForKnowledgeRunnerDrain(t *testing.T, dataRoot string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		var applied, dirty, messageRows, workRows, artifactRows uint64
		var status string
		err := database.QueryRow(`SELECT applied_sequence, status FROM knowledge_index_state WHERE singleton = 1`).Scan(&applied, &status)
		if err == nil && status == store.KnowledgeIndexReady {
			err = database.QueryRow(`SELECT count(*) FROM knowledge_dirty_sources`).Scan(&dirty)
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE source_kind = 'message'`).Scan(&messageRows)
			}
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE source_kind = 'work'`).Scan(&workRows)
			}
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE source_kind = 'artifact_version'`).Scan(&artifactRows)
			}
			if err == nil && dirty == 0 && messageRows != 0 && workRows != 0 && artifactRows != 0 {
				return
			}
			last = fmt.Sprintf("status=%s applied=%d dirty=%d message=%d work=%d artifact=%d err=%v", status, applied, dirty, messageRows, workRows, artifactRows, err)
		} else {
			last = fmt.Sprintf("state applied=%d status=%s err=%v", applied, status, err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("knowledge runner did not drain concurrent Message, Work, and Artifact writes: %s", last)
}

func waitForKnowledgeRunnerReady(t *testing.T, dataRoot string) {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(dataRoot, "data", "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var applied uint64
		var status string
		err := database.QueryRow(`SELECT applied_sequence, status FROM knowledge_index_state WHERE singleton = 1`).Scan(&applied, &status)
		if err == nil && status == store.KnowledgeIndexReady {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("knowledge runner did not become ready")
}
