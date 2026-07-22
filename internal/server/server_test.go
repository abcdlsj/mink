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
		var activeGeneration, nextGeneration, maximum, applied, messageRows, workRows, artifactRows, generations uint64
		var status string
		err := database.QueryRow(`SELECT active_generation, next_generation, status FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&activeGeneration, &nextGeneration, &status)
		if err == nil && activeGeneration != 0 && nextGeneration == 0 && status == store.KnowledgeIndexReady {
			err = database.QueryRow(`SELECT COALESCE(MAX(sequence), 0) FROM knowledge_dirty_sources`).Scan(&maximum)
			if err == nil {
				err = database.QueryRow(`SELECT applied_sequence FROM knowledge_generation_progress WHERE generation = ?`, activeGeneration).Scan(&applied)
			}
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ? AND source_kind = 'message'`, activeGeneration).Scan(&messageRows)
			}
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ? AND source_kind = 'work'`, activeGeneration).Scan(&workRows)
			}
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_fts WHERE generation = ? AND source_kind = 'artifact_version'`, activeGeneration).Scan(&artifactRows)
			}
			if err == nil {
				err = database.QueryRow(`SELECT count(*) FROM knowledge_index_generations`).Scan(&generations)
			}
			if err == nil && applied == maximum && messageRows != 0 && workRows != 0 && artifactRows != 0 && generations == 1 {
				return
			}
			last = fmt.Sprintf("active=%d next=%d status=%s maximum=%d applied=%d message=%d work=%d artifact=%d generations=%d err=%v", activeGeneration, nextGeneration, status, maximum, applied, messageRows, workRows, artifactRows, generations, err)
		} else {
			last = fmt.Sprintf("metadata active=%d next=%d status=%s err=%v", activeGeneration, nextGeneration, status, err)
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
		var activeGeneration uint64
		var status string
		err := database.QueryRow(`SELECT active_generation, status FROM knowledge_index_metadata WHERE singleton = 1`).Scan(&activeGeneration, &status)
		if err == nil && activeGeneration != 0 && status == store.KnowledgeIndexReady {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("knowledge runner did not become ready")
}
