package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	executionapp "github.com/abcdlsj/sumi/internal/execution/application"
	executiondomain "github.com/abcdlsj/sumi/internal/execution/domain"
	"github.com/google/uuid"
)

type runFixture struct {
	*inboxFixture
	path string
}

func openRunFixture(t *testing.T) *runFixture {
	t.Helper()
	f := openInboxFixture(t)
	issueRunGrant(t, f, f.at(0))
	var sequence int
	var name, path string
	if err := f.database.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	return &runFixture{inboxFixture: f, path: path}
}

func issueRunGrant(t *testing.T, f *inboxFixture, now time.Time) Grant {
	t.Helper()
	grant, err := f.database.IssueGrant(context.Background(), IssueGrantParams{
		RequestID: uuid.NewString(),
		Actor:     f.owner,
		Subject: Principal{
			Kind: PrincipalAgent, ID: f.agentID, OrganizationID: f.owner.OrganizationID,
		},
		Capability:    CapabilityRunExecute,
		Scope:         Scope{Kind: "agent", ID: f.agentID},
		ParentGrantID: f.rootGrant.ID,
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func (f *runFixture) queue(t *testing.T, body string, second int) Run {
	t.Helper()
	message, err := f.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID},
		Body:   body, MentionedPrincipals: agentPrincipals(f.owner.OrganizationID, f.agentID),
		Now: f.at(second),
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := runByAgentMessage(context.Background(), f.database.db, f.agentID, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunStateQueued || run.Attempt != 0 || run.Fence != 0 || run.ID == "" {
		t.Fatalf("queued run = %+v", run)
	}
	return run
}

func (f *runFixture) observe(t *testing.T, second int) uint64 {
	t.Helper()
	result, err := f.database.ObserveTarget(context.Background(), ObserveTargetParams{
		Authentication: executionapp.AgentInboxAuthentication(f.authentication),
		Target:         MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID},
		Limit:          200,
		Now:            f.at(second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.Head
}

func (f *runFixture) claim(t *testing.T, runID string, second int) Run {
	t.Helper()
	run, err := f.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: f.authentication,
		RunID: runID, Now: f.at(second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func (f *runFixture) acceptTrigger(t *testing.T, body string, second int) Run {
	t.Helper()
	run := f.queue(t, body, second)
	f.observe(t, second+1)
	return run
}

func (f *runFixture) claimRun(t *testing.T, run Run, second int) Run {
	t.Helper()
	return f.claim(t, run.ID, second)
}

func rotateRunRuntime(t *testing.T, f *runFixture, tokenValue byte, now time.Time) AgentRuntimeAuthentication {
	t.Helper()
	token := rtToken(tokenValue)
	if _, err := f.database.CreateSession(context.Background(), CreateAgentRuntimeSessionParams{
		ComputerID: f.authentication.Proof.ComputerID(), RegistrationKey: "computer-registration-key",
		AgentID: f.agentID, PlacementDesiredRevision: f.authentication.Proof.PlacementDesiredRevision(),
		Token: token, Now: now, ExpiresAt: now.Add(agentRuntimeSessionTTL),
	}); err != nil {
		t.Fatal(err)
	}
	authentication, err := f.database.AuthenticateAgentRuntimeSession(context.Background(), token, now.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	f.authentication = authentication
	return authentication
}

func TestInboxTriggerCreatesStableQueuedRunAndClaimFreezesBasis(t *testing.T) {
	f := openRunFixture(t)
	queued := f.queue(t, "run me", 1)

	listed, err := f.database.ListRuns(context.Background(), ListRunsParams{
		Authentication: f.authentication, Limit: 20, Now: f.at(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Runs) != 1 || listed.Runs[0].ID != queued.ID || listed.NextSequence != queued.Sequence {
		t.Fatalf("listed runs = %+v", listed)
	}

	head := f.observe(t, 3)
	claimed := f.claim(t, queued.ID, 4)
	if claimed.ID != queued.ID || claimed.State != RunStateRunning || claimed.Attempt != 1 ||
		claimed.Fence == 0 || claimed.InputBasisTargetSequence != head ||
		claimed.LeaseHolderComputerID != f.authentication.Proof.ComputerID() ||
		claimed.PlacementDesiredRevision != f.authentication.Proof.PlacementDesiredRevision() {
		t.Fatalf("claimed run = %+v, observed head = %d", claimed, head)
	}

	requestID := uuid.NewString()
	params := RenewRunParams{
		RequestID: requestID, Authentication: f.authentication, RunID: claimed.ID,
		Attempt: claimed.Attempt, Fence: claimed.Fence, Now: f.at(5),
	}
	first, err := f.database.RenewRun(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.database.RenewRun(context.Background(), params)
	if err != nil || replayed.LeaseExpiresAt == nil || !replayed.LeaseExpiresAt.Equal(*first.LeaseExpiresAt) {
		t.Fatalf("renew replay = %+v, %v", replayed, err)
	}
	params.Fence++
	if _, err := f.database.RenewRun(context.Background(), params); !errors.Is(err, ErrRunRequestConflict) {
		t.Fatalf("changed renew replay error = %v", err)
	}
}

func TestRunRetryKeepsIDAndRejectsStaleAttempt(t *testing.T) {
	f := openRunFixture(t)
	queued := f.queue(t, "retry me", 1)
	f.observe(t, 2)
	first := f.claim(t, queued.ID, 3)

	retried, err := f.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: f.authentication,
		RunID: first.ID, Now: first.LeaseExpiresAt.Add(time.Nanosecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	if retried.ID != first.ID || retried.Attempt != first.Attempt+1 || retried.Fence <= first.Fence {
		t.Fatalf("retried run = %+v, first = %+v", retried, first)
	}
	_, err = f.database.CompleteRun(context.Background(), CompleteRunParams{
		RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(), Authentication: f.authentication,
		RunID: first.ID, Attempt: first.Attempt, Fence: first.Fence,
		Outcome: executiondomain.OutcomeSucceeded, Body: "stale", Now: retried.LeaseExpiresAt.Add(-time.Second),
	})
	if !errors.Is(err, ErrRunLeaseStale) {
		t.Fatalf("stale completion error = %v", err)
	}
}

func TestOnlyOneRunningRunPerAgent(t *testing.T) {
	f := openRunFixture(t)
	first := f.queue(t, "first", 1)
	second := f.queue(t, "second", 2)
	f.observe(t, 3)
	f.claim(t, first.ID, 4)
	if _, err := f.database.ClaimRun(context.Background(), ClaimRunParams{
		RequestID: uuid.NewString(), Authentication: f.authentication,
		RunID: second.ID, Now: f.at(5),
	}); !errors.Is(err, ErrRunAlreadyActive) {
		t.Fatalf("second claim error = %v", err)
	}
}

func TestRunCompletionIsAtomicAndOutboxReplayIsImmutable(t *testing.T) {
	f := openRunFixture(t)
	queued := f.queue(t, "complete me", 1)
	f.observe(t, 2)
	running := f.claim(t, queued.ID, 3)
	params := CompleteRunParams{
		RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(), Authentication: f.authentication,
		RunID: running.ID, Attempt: running.Attempt, Fence: running.Fence,
		Outcome: executiondomain.OutcomeSucceeded, Body: "done",
		Usage: RunUsage{InputUnits: 11, OutputUnits: 7}, Now: f.at(4),
	}
	first, err := f.database.CompleteRun(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.State != RunStateSucceeded || first.Kind != InboxResultMessage || first.Message == nil ||
		first.Run.Usage != params.Usage {
		t.Fatalf("completion = %+v", first)
	}
	replayed, err := f.database.CompleteRun(context.Background(), params)
	if err != nil || replayed.Run.ID != first.Run.ID || replayed.Message == nil || replayed.Message.ID != first.Message.ID {
		t.Fatalf("completion replay = %+v, %v", replayed, err)
	}
	params.Body = "changed"
	if _, err := f.database.CompleteRun(context.Background(), params); !errors.Is(err, ErrRunRequestConflict) {
		t.Fatalf("changed request replay error = %v", err)
	}
	params.RequestID = uuid.NewString()
	if _, err := f.database.CompleteRun(context.Background(), params); !errors.Is(err, ErrRunCompletionConflict) {
		t.Fatalf("reused outbox event error = %v", err)
	}
}

func TestRunCompletionHoldsDraftWhenTargetAdvanced(t *testing.T) {
	f := openRunFixture(t)
	queued := f.queue(t, "basis", 1)
	f.observe(t, 2)
	running := f.claim(t, queued.ID, 3)
	if _, err := f.database.SendMessage(context.Background(), SendMessageParams{
		RequestID: uuid.NewString(), Actor: f.owner,
		Target: MessageTarget{Kind: MessageTargetSpace, ID: f.group.ID}, Body: "advanced", Now: f.at(4),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := f.database.CompleteRun(context.Background(), CompleteRunParams{
		RequestID: uuid.NewString(), OutboxEventID: uuid.NewString(), Authentication: f.authentication,
		RunID: running.ID, Attempt: running.Attempt, Fence: running.Fence,
		Outcome: executiondomain.OutcomeSucceeded, Body: "based on old head", Now: f.at(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != InboxResultHeldDraft || result.HeldDraft == nil || result.Run.ResultID != result.HeldDraft.ID {
		t.Fatalf("held completion = %+v", result)
	}
}

func TestRunClaimConcurrentRequestHasOneWinner(t *testing.T) {
	f := openRunFixture(t)
	queued := f.queue(t, "race", 1)
	f.observe(t, 2)
	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := f.database.ClaimRun(context.Background(), ClaimRunParams{
				RequestID: uuid.NewString(), Authentication: f.authentication,
				RunID: queued.ID, Now: f.at(3),
			})
			errorsByCall <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByCall)
	var succeeded, rejected int
	for err := range errorsByCall {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, executiondomain.ErrRunLeaseActive):
			rejected++
		default:
			t.Fatalf("claim error = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("claim results: success=%d rejected=%d", succeeded, rejected)
	}
}

func TestRunFactsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	f := openAgentRuntimeFixtureAt(t, path)
	if err := f.database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}
