package app

import (
	"context"
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/command"
)

func TestPrepareAndCommitTaskCreateProposal(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	ctx := command.WithRunContext(context.Background(), command.RunContext{
		Source: "desktop:channel:" + sp.ID,
		Input:  "请创建任务给 dev",
	})

	proposal, err := a.PrepareTaskCreateProposal(ctx, TaskCreateProposalPayload{
		SpaceID:            sp.ID,
		SourceMessageID:    msg.ID,
		SourceThreadID:     msg.ID,
		CreatedBy:          "planner",
		AssignedBy:         "planner",
		AssigneeID:         "dev",
		Title:              "ship proposal flow",
		ExpectedOutcome:    "proposal can be committed into a task",
		AcceptanceCriteria: "task exists only after explicit commit",
		AuthorizationText:  "请创建任务给 dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Kind != "task.create" || proposal.Status != "prepared" || proposal.ID == "" {
		t.Fatalf("proposal = %+v", proposal)
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks before commit = %d, want 0", len(tasks))
	}

	tk, err := a.CommitTaskCreateProposal(command.WithSource(context.Background(), "desktop"), proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Title != "ship proposal flow" || tk.WorkerID != "dev" {
		t.Fatalf("task = %#v", tk)
	}
	tasks, err = a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks after commit = %d, want 1", len(tasks))
	}
}

func TestRejectTaskCreateProposalRemovesItFromPendingList(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	ctx := command.WithRunContext(context.Background(), command.RunContext{
		Source: "desktop:channel:" + sp.ID,
		Input:  "请创建任务给 dev",
	})
	proposal, err := a.PrepareTaskCreateProposal(ctx, TaskCreateProposalPayload{
		SpaceID:            sp.ID,
		SourceMessageID:    msg.ID,
		CreatedBy:          "planner",
		AssignedBy:         "planner",
		AssigneeID:         "dev",
		Title:              "reject proposal",
		ExpectedOutcome:    "proposal can be rejected cleanly",
		AcceptanceCriteria: "pending list is empty after reject",
		AuthorizationText:  "请创建任务给 dev",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RejectTaskCreateProposal(command.WithSource(context.Background(), "desktop"), proposal.ID); err != nil {
		t.Fatal(err)
	}
	if pending := a.PendingTaskCreateProposals(10); len(pending) != 0 {
		t.Fatalf("pending proposals = %#v", pending)
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks after reject = %d, want 0", len(tasks))
	}
}

func TestPrepareTaskCreateProposalRequiresExplicitIntent(t *testing.T) {
	a := newTaskToolTestApp(t)
	sp, msg := newTaskToolSpace(t, a)
	ctx := command.WithRunContext(context.Background(), command.RunContext{
		Source: "desktop:channel:" + sp.ID,
		Input:  "修复这个 bug",
	})
	_, err := a.PrepareTaskCreateProposal(ctx, TaskCreateProposalPayload{
		SpaceID:            sp.ID,
		SourceMessageID:    msg.ID,
		CreatedBy:          "planner",
		AssignedBy:         "planner",
		AssigneeID:         "dev",
		Title:              "bad proposal",
		ExpectedOutcome:    "this should fail explicit intent",
		AcceptanceCriteria: "tool rejects missing task authorization",
		AuthorizationText:  "修复这个 bug",
	})
	if err == nil || !strings.Contains(err.Error(), "explicitly ask") {
		t.Fatalf("expected explicit intent error, got %v", err)
	}
}
