package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	taskpkg "github.com/abcdlsj/sumi/task"
	"github.com/abcdlsj/sumi/tool"
)

func (a *App) PrepareTaskCreateProposal(ctx context.Context, in TaskCreateProposalPayload) (tool.ActionProposal, error) {
	if a == nil {
		return tool.ActionProposal{}, fmt.Errorf("app not available")
	}
	title := strings.TrimSpace(in.Title)
	expected := strings.TrimSpace(in.ExpectedOutcome)
	criteria := strings.TrimSpace(in.AcceptanceCriteria)
	if err := validateTaskCommitment(title, expected, criteria); err != nil {
		return tool.ActionProposal{}, err
	}
	if err := validateExplicitTaskAuthorization(ctx, in.AuthorizationText); err != nil {
		return tool.ActionProposal{}, err
	}
	if strings.TrimSpace(in.SpaceID) == "" {
		return tool.ActionProposal{}, fmt.Errorf("task_create requires space_id")
	}
	if _, err := a.taskAssignee(in.AssigneeID); err != nil {
		return tool.ActionProposal{}, err
	}
	in.Title = title
	in.ExpectedOutcome = expected
	in.AcceptanceCriteria = criteria
	in.CreatedBy = strings.TrimSpace(in.CreatedBy)
	in.AssignedBy = strings.TrimSpace(firstNonEmpty(in.AssignedBy, in.CreatedBy))
	in.AssigneeID = strings.TrimSpace(in.AssigneeID)
	in.SourceMessageID = strings.TrimSpace(in.SourceMessageID)
	in.SourceThreadID = strings.TrimSpace(in.SourceThreadID)
	raw, _ := json.Marshal(in)
	proposal := tool.ActionProposal{
		ID:        "taskprop-" + uuid.NewString()[:8],
		Kind:      "task.create",
		Status:    "prepared",
		Intent:    "Create task",
		Target:    in.SpaceID,
		Risk:      string(tool.RiskSafe),
		Preview:   fmt.Sprintf("Create task %q for %s", in.Title, in.AssigneeID),
		Rollback:  "Archive or close the created task",
		ExpiresAt: time.Now().Add(15 * time.Minute),
		Payload:   raw,
		Reason:    "explicit task intent confirmed; waiting human commit",
	}
	a.publishActionProposal(ctx, "task_create", proposal, proposal.Status)
	return proposal, nil
}

func (a *App) CommitTaskCreateProposal(ctx context.Context, proposalID string) (*taskpkg.Task, error) {
	proposal, ok := a.findTaskCreateProposal(proposalID)
	if !ok {
		return nil, fmt.Errorf("task proposal not found: %s", strings.TrimSpace(proposalID))
	}
	if proposal.Status != "prepared" {
		return nil, fmt.Errorf("task proposal %s is %s", proposal.ID, proposal.Status)
	}
	var in TaskCreateProposalPayload
	if err := json.Unmarshal(proposal.Payload, &in); err != nil {
		return nil, fmt.Errorf("task proposal payload invalid: %w", err)
	}
	if err := validateTaskCommitment(in.Title, in.ExpectedOutcome, in.AcceptanceCriteria); err != nil {
		return nil, err
	}
	createdBy := strings.TrimSpace(firstNonEmpty(in.CreatedBy, "user"))
	assignedBy := strings.TrimSpace(firstNonEmpty(in.AssignedBy, createdBy))
	tk, err := a.tasks.Create(taskpkg.CreateTaskInput{
		SpaceID:            strings.TrimSpace(in.SpaceID),
		TriggerMessageID:   strings.TrimSpace(in.SourceMessageID),
		SourceThreadID:     strings.TrimSpace(in.SourceThreadID),
		InitiatorID:        createdBy,
		CreatedBy:          createdBy,
		WorkerID:           strings.TrimSpace(in.AssigneeID),
		AssignedBy:         assignedBy,
		Title:              strings.TrimSpace(in.Title),
		ExpectedOutcome:    strings.TrimSpace(in.ExpectedOutcome),
		AcceptanceCriteria: strings.TrimSpace(in.AcceptanceCriteria),
		Source:             command.SourceFrom(ctx),
	})
	if err != nil {
		a.publishActionProposal(ctx, "task_create", withProposalStatus(proposal, "failed", err.Error()), "failed")
		return nil, err
	}
	a.publishActionProposal(ctx, "task_create", withProposalStatus(proposal, "committed", tk.ID), "committed")
	return tk, nil
}

func (a *App) RejectTaskCreateProposal(ctx context.Context, proposalID string) error {
	proposal, ok := a.findTaskCreateProposal(proposalID)
	if !ok {
		return fmt.Errorf("task proposal not found: %s", strings.TrimSpace(proposalID))
	}
	if proposal.Status != "prepared" {
		return fmt.Errorf("task proposal %s is %s", proposal.ID, proposal.Status)
	}
	a.publishActionProposal(ctx, "task_create", withProposalStatus(proposal, "rejected", "rejected by human"), "rejected")
	return nil
}

func (a *App) findTaskCreateProposal(proposalID string) (tool.ActionProposal, bool) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" || a == nil || a.store == nil {
		return tool.ActionProposal{}, false
	}
	events, err := a.store.ReplayGlobal(1000)
	if err != nil {
		return tool.ActionProposal{}, false
	}
	var latest tool.ActionProposal
	var found bool
	for _, ev := range events {
		if ev.Type != bus.ActionProposal {
			continue
		}
		var p tool.ActionProposal
		if json.Unmarshal([]byte(ev.Input), &p) != nil {
			continue
		}
		if p.ID != proposalID || p.Kind != "task.create" {
			continue
		}
		latest = p
		found = true
	}
	return latest, found
}

func (a *App) FindTaskCreateProposalForUI(proposalID string) (ActionProposalSummary, bool) {
	proposal, ok := a.findTaskCreateProposal(proposalID)
	if !ok {
		return ActionProposalSummary{}, false
	}
	return ActionProposalSummary{
		Time:     time.Now(),
		Source:   "",
		Tool:     "task_create",
		Result:   proposal.Status,
		Proposal: proposal,
	}, true
}

func (a *App) publishActionProposal(ctx context.Context, toolName string, proposal tool.ActionProposal, result string) {
	if a == nil || a.bus == nil {
		return
	}
	data, _ := json.Marshal(proposal)
	a.bus.Publish(bus.Event{
		Type:   bus.ActionProposal,
		Source: command.SourceFrom(ctx),
		Tool:   toolName,
		Input:  string(data),
		Output: strings.TrimSpace(result),
	})
}

func withProposalStatus(p tool.ActionProposal, status, reason string) tool.ActionProposal {
	p.Status = strings.TrimSpace(status)
	if strings.TrimSpace(reason) != "" {
		p.Reason = strings.TrimSpace(reason)
	}
	return p
}
