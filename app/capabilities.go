package app

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/space"
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

type SkillEnvNeed struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint,omitempty"`
}

type SkillDirectoryItem struct {
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	When          string         `json:"when,omitempty"`
	Risk          string         `json:"risk,omitempty"`
	Env           []string       `json:"env,omitempty"`
	EnvNeeds      []SkillEnvNeed `json:"env_needs,omitempty"`
	Entrypoints   []string       `json:"entrypoints,omitempty"`
	Examples      []string       `json:"examples,omitempty"`
	Path          string         `json:"path,omitempty"`
	Configured    bool           `json:"configured"`
	MissingEnv    []string       `json:"missing_env,omitempty"`
	LastAction    string         `json:"last_action,omitempty"`
	LastListed    *time.Time     `json:"last_listed,omitempty"`
	LastDescribed *time.Time     `json:"last_described,omitempty"`
	LastUsed      *time.Time     `json:"last_used,omitempty"`
	Body          string         `json:"body,omitempty"`
}

type TaskStateSummary struct {
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Status             string         `json:"status"`
	Lifecycle          string         `json:"lifecycle"`
	CreatedBy          string         `json:"created_by,omitempty"`
	WorkerID           string         `json:"worker_id,omitempty"`
	AssigneeID         string         `json:"assignee_id,omitempty"`
	Assignee           string         `json:"assignee,omitempty"`
	AssignedBy         string         `json:"assigned_by,omitempty"`
	SpaceID            string         `json:"space_id,omitempty"`
	Source             string         `json:"source,omitempty"`
	SourceMessageID    string         `json:"source_message,omitempty"`
	SourceThreadID     string         `json:"source_thread_id,omitempty"`
	SourceThread       string         `json:"source_thread,omitempty"`
	TriggerMessageID   string         `json:"trigger_message_id,omitempty"`
	ParentMessageID    string         `json:"parent_message_id,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at"`
	ExpectedOutcome    string         `json:"expected_outcome,omitempty"`
	AcceptanceCriteria string         `json:"acceptance_criteria,omitempty"`
	Outcome            string         `json:"outcome,omitempty"`
	State              task.TaskState `json:"state,omitempty"`
	LatestRun          string         `json:"latest_run,omitempty"`
	RunStatus          string         `json:"run_status,omitempty"`
	RunStarted         time.Time      `json:"run_started,omitempty"`
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

func (a *App) SkillDirectory() []SkillDirectoryItem {
	if a == nil || a.skills == nil {
		return nil
	}
	status := a.skillAuditStates()
	env := childEnvMap(a.cfg.ChildEnv())
	skills := a.skills.Discover()
	out := make([]SkillDirectoryItem, 0, len(skills))
	for _, s := range skills {
		if s == nil {
			continue
		}
		item := SkillDirectoryItem{
			Name:        s.Name,
			Description: s.Desc,
			When:        s.When,
			Risk:        s.Risk,
			Env:         append([]string(nil), s.Env...),
			Entrypoints: append([]string(nil), s.Entrypoints...),
			Examples:    append([]string(nil), s.Examples...),
			Path:        s.Path,
			Configured:  true,
		}
		item.EnvNeeds, item.MissingEnv = skillEnvNeeds(s.Env, env)
		if len(item.MissingEnv) > 0 {
			item.Configured = false
		}
		if st, ok := status[strings.ToLower(s.Name)]; ok {
			item.LastListed = timePtr(st.listed)
			item.LastDescribed = timePtr(st.described)
			item.LastUsed = timePtr(st.used)
			item.LastAction = st.lastAction
		}
		out = append(out, item)
	}
	return out
}

func (a *App) SkillDetail(name string) (SkillDirectoryItem, bool) {
	name = strings.TrimSpace(name)
	if a == nil || a.skills == nil || name == "" {
		return SkillDirectoryItem{}, false
	}
	for _, item := range a.SkillDirectory() {
		if strings.EqualFold(item.Name, name) {
			if s := a.skills.Load(item.Name); s != nil {
				item.Body = s.Body
			}
			return item, true
		}
	}
	return SkillDirectoryItem{}, false
}

func (a *App) RecentTaskStates(limit int) []TaskStateSummary {
	return a.RecentTaskStatesByLifecycle(limit, task.LifecycleActive)
}

func (a *App) RecentArchivedTaskStates(limit int) []TaskStateSummary {
	return a.RecentTaskStatesByLifecycle(limit, task.LifecycleArchived)
}

func (a *App) ArchivedTaskStateCount() int {
	return len(a.RecentArchivedTaskStates(0))
}

func (a *App) taskParentMessageID(tk *task.Task) string {
	if a == nil || a.spaces == nil || tk == nil || strings.TrimSpace(tk.TriggerMessageID) == "" {
		return ""
	}
	sp, err := a.spaces.LoadSpace(tk.SpaceID)
	if err != nil || sp == nil {
		return ""
	}
	for _, m := range sp.Messages {
		if m.ID != tk.TriggerMessageID {
			continue
		}
		if strings.TrimSpace(m.ParentMessageID) != "" {
			return m.ParentMessageID
		}
		if sp.Kind == space.KindChannel || sp.Kind == space.KindDirectChat {
			return m.ID
		}
		return ""
	}
	return ""
}

func (a *App) RecentTaskStatesByLifecycle(limit int, lifecycle task.Lifecycle) []TaskStateSummary {
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
		if tk.Status.Lifecycle() != lifecycle {
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
		item := TaskStateSummary{
			ID:                 tk.ID,
			Title:              tk.Title,
			Status:             string(tk.Status),
			Lifecycle:          string(tk.Status.Lifecycle()),
			CreatedBy:          taskCreatedBy(tk),
			WorkerID:           tk.WorkerID,
			AssigneeID:         tk.WorkerID,
			Assignee:           tk.WorkerID,
			AssignedBy:         tk.AssignedBy,
			SpaceID:            tk.SpaceID,
			Source:             tk.Source,
			SourceMessageID:    tk.TriggerMessageID,
			SourceThreadID:     tk.SourceThreadID,
			SourceThread:       tk.SourceThreadID,
			TriggerMessageID:   tk.TriggerMessageID,
			ParentMessageID:    a.taskParentMessageID(tk),
			UpdatedAt:          tk.UpdatedAt,
			ExpectedOutcome:    tk.ExpectedOutcome,
			AcceptanceCriteria: tk.AcceptanceCriteria,
			Outcome:            tk.Outcome,
			State:              state,
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

func taskCreatedBy(tk *task.Task) string {
	if tk == nil {
		return ""
	}
	if strings.TrimSpace(tk.CreatedBy) != "" {
		return strings.TrimSpace(tk.CreatedBy)
	}
	return strings.TrimSpace(tk.InitiatorID)
}

type skillAuditState struct {
	listed     time.Time
	described  time.Time
	used       time.Time
	lastAt     time.Time
	lastAction string
}

func (a *App) skillAuditStates() map[string]skillAuditState {
	out := map[string]skillAuditState{}
	if a == nil || a.store == nil {
		return out
	}
	events, err := a.store.ReplayGlobal(500)
	if err != nil {
		return out
	}
	for _, ev := range events {
		if ev.Text == "" {
			continue
		}
		var action string
		key := strings.ToLower(ev.Text)
		st := out[key]
		switch ev.Type {
		case bus.SkillListed:
			action = "listed"
			if ev.Time.After(st.listed) {
				st.listed = ev.Time
			}
		case bus.SkillDescribed:
			action = "described"
			if ev.Time.After(st.described) {
				st.described = ev.Time
			}
		case bus.SkillUsed:
			action = "used"
			if ev.Time.After(st.used) {
				st.used = ev.Time
			}
		default:
			continue
		}
		if ev.Time.After(st.lastAt) {
			st.lastAt = ev.Time
			st.lastAction = action
		}
		out[key] = st
	}
	return out
}

func childEnvMap(env []string) map[string]bool {
	out := make(map[string]bool, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && strings.TrimSpace(value) != "" {
			out[key] = true
		}
	}
	return out
}

func skillEnvNeeds(env []string, configured map[string]bool) ([]SkillEnvNeed, []string) {
	needs := make([]SkillEnvNeed, 0, len(env))
	var missing []string
	for _, raw := range env {
		name := skillEnvName(raw)
		if name == "" {
			continue
		}
		need := SkillEnvNeed{
			Name:       name,
			Configured: configured[name],
			Hint:       skillEnvHint(name),
		}
		if !need.Configured {
			missing = append(missing, name)
		}
		needs = append(needs, need)
	}
	return needs, missing
}

func skillEnvName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "SUMI_") {
		return raw
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
			lastUnderscore = false
		case r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		return ""
	}
	return "SUMI_" + name
}

func skillEnvHint(name string) string {
	rest := strings.TrimPrefix(name, "SUMI_")
	parts := strings.Split(rest, "_")
	if len(parts) < 2 {
		return "Set " + name + " in the environment"
	}
	section := strings.ToLower(parts[0])
	key := strings.ToLower(strings.Join(parts[1:], "_"))
	return "Set [" + section + "]." + key + " in config.toml or export " + name
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
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
