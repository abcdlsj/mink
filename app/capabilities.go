package app

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/task"
	"github.com/abcdlsj/sumi/tool"
)

type SkillSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	When        string   `json:"when,omitempty"`
	Risk        string   `json:"risk,omitempty"`
	Env         []string `json:"env,omitempty"`
	Entrypoints []string `json:"entrypoints,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Path        string   `json:"path,omitempty"`
}

type TaskStateSummary struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Status     string         `json:"status"`
	WorkerID   string         `json:"worker_id,omitempty"`
	SpaceID    string         `json:"space_id,omitempty"`
	Source     string         `json:"source,omitempty"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Outcome    string         `json:"outcome,omitempty"`
	State      task.TaskState `json:"state,omitempty"`
	LatestRun  string         `json:"latest_run,omitempty"`
	RunStatus  string         `json:"run_status,omitempty"`
	RunStarted time.Time      `json:"run_started,omitempty"`
}

type ActionProposalSummary struct {
	Time     time.Time           `json:"time"`
	Source   string              `json:"source,omitempty"`
	Tool     string              `json:"tool,omitempty"`
	Result   string              `json:"result,omitempty"`
	Proposal tool.ActionProposal `json:"proposal,omitempty"`
}

func (a *App) SkillSummaries() []SkillSummary {
	if a == nil || a.skills == nil {
		return nil
	}
	skills := a.skills.Discover()
	out := make([]SkillSummary, 0, len(skills))
	for _, s := range skills {
		if s == nil {
			continue
		}
		out = append(out, SkillSummary{
			Name:        s.Name,
			Description: s.Desc,
			When:        s.When,
			Risk:        s.Risk,
			Env:         append([]string(nil), s.Env...),
			Entrypoints: append([]string(nil), s.Entrypoints...),
			Examples:    append([]string(nil), s.Examples...),
			Path:        s.Path,
		})
	}
	return out
}

func (a *App) RecentTaskStates(limit int) []TaskStateSummary {
	if a == nil || a.tasks == nil {
		return nil
	}
	tasks, err := a.tasks.ListAll()
	if err != nil {
		return nil
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt) })
	out := make([]TaskStateSummary, 0, len(tasks))
	for _, tk := range tasks {
		if tk == nil {
			continue
		}
		var latest *task.Run
		if runs, err := a.tasks.ListRuns(tk.ID); err == nil && len(runs) > 0 {
			latest = runs[0]
			for _, r := range runs[1:] {
				if r.StartedAt.After(latest.StartedAt) {
					latest = r
				}
			}
		}
		state := tk.State
		if emptyTaskState(state) && latest != nil {
			state = latest.State
		}
		if emptyTaskState(state) {
			continue
		}
		item := TaskStateSummary{
			ID:        tk.ID,
			Title:     tk.Title,
			Status:    string(tk.Status),
			WorkerID:  tk.WorkerID,
			SpaceID:   tk.SpaceID,
			Source:    tk.Source,
			UpdatedAt: tk.UpdatedAt,
			Outcome:   tk.Outcome,
			State:     state,
		}
		if latest != nil {
			item.LatestRun = latest.ID
			item.RunStatus = string(latest.Status)
			item.RunStarted = latest.StartedAt
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (a *App) RecentActionProposals(limit int) []ActionProposalSummary {
	if a == nil || a.store == nil {
		return nil
	}
	events, err := a.store.ReplayGlobal(500)
	if err != nil {
		return nil
	}
	out := make([]ActionProposalSummary, 0)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type != bus.ActionProposal {
			continue
		}
		var p tool.ActionProposal
		_ = json.Unmarshal([]byte(ev.Input), &p)
		out = append(out, ActionProposalSummary{
			Time:     ev.Time,
			Source:   ev.Source,
			Tool:     ev.Tool,
			Result:   ev.Output,
			Proposal: p,
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func emptyTaskState(s task.TaskState) bool {
	return strings.TrimSpace(s.Goal) == "" &&
		strings.TrimSpace(s.Checkpoint) == "" &&
		len(s.Todo) == 0 &&
		len(s.Artifacts) == 0 &&
		len(s.Blockers) == 0 &&
		len(s.RelatedIDs) == 0
}
