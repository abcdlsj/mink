package desktop

import (
	"sort"
	"strings"
	"time"

	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

type ThreadSummary struct {
	ParentID         string    `json:"parent_id"`
	ParentPreview    string    `json:"parent_preview"`
	ReplyCount       int       `json:"reply_count"`
	LastReplyTime    time.Time `json:"last_reply_time"`
	LastReplyAuthor  string    `json:"last_reply_author,omitempty"`
	HasRunningWorker bool      `json:"has_running_worker,omitempty"`
}

type ThreadDetail struct {
	SpaceID           string            `json:"space_id"`
	ParentID          string            `json:"parent_id"`
	Parent            *MessageView      `json:"parent,omitempty"`
	Replies           []MessageView     `json:"replies"`
	Participants      []AgentItem       `json:"participants,omitempty"`
	ChannelAgents     []string          `json:"channel_agents,omitempty"`
	AgentModes        map[string]string `json:"agent_modes,omitempty"`
	RecentRuns        []AgentRun        `json:"recent_runs,omitempty"`
	ArchivedRunsCount int               `json:"archived_runs_count,omitempty"`
	ActiveWorkerID    string            `json:"active_worker_id,omitempty"`
	LastReplyTime     time.Time         `json:"last_reply_time,omitempty"`
	NotFound          bool              `json:"not_found,omitempty"`
	Unsupported       bool              `json:"unsupported,omitempty"`
	UnsupportedHint   string            `json:"unsupported_hint,omitempty"`
}

const previewLen = 120

func (b *Backend) ListThreadsForSpace(id string) []ThreadSummary {
	sp, err := b.app.Spaces().LoadSpace(strings.TrimSpace(id))
	if err != nil || sp == nil || !threadKind(sp.Kind) {
		return []ThreadSummary{}
	}
	groups := groupReplies(sp)
	if len(groups) == 0 {
		return []ThreadSummary{}
	}
	parents := indexMessages(sp.Messages)
	tasks := b.runningTasks(sp.ID)
	out := make([]ThreadSummary, 0, len(groups))
	for pid, replies := range groups {
		root, ok := parents[pid]
		if !ok {
			continue
		}
		out = append(out, b.summarize(sp, root, replies, tasks))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastReplyTime.After(out[j].LastReplyTime) })
	return out
}

func (b *Backend) GetThreadDetail(spaceID, parentID string) ThreadDetail {
	parentID = strings.TrimSpace(parentID)
	spaceID = strings.TrimSpace(spaceID)
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ThreadDetail{SpaceID: spaceID, ParentID: parentID, NotFound: true}
	}
	if !threadKind(sp.Kind) {
		return ThreadDetail{
			SpaceID:         spaceID,
			ParentID:        parentID,
			Unsupported:     true,
			UnsupportedHint: "Threads are not supported in agent DMs",
		}
	}
	parent, ok := findMessage(sp.Messages, parentID)
	if !ok {
		return ThreadDetail{SpaceID: spaceID, ParentID: parentID, NotFound: true}
	}
	replies := sp.Replies(parentID)
	all := append([]space.Message{*parent}, replies...)
	accessory := computeTaskAccessoryIndex(sp, b.app)
	parentView := singleMessageToView(sp, *parent, b.app)
	if tk, ok := accessory[parent.ID]; ok && tk != nil {
		parentView.TaskAccessory = projectTaskAccessory(tk, b.app)
	}
	views := make([]MessageView, 0, len(replies))
	for _, r := range replies {
		v := singleMessageToView(sp, r, b.app)
		if tk, ok := accessory[r.ID]; ok && tk != nil {
			v.TaskAccessory = projectTaskAccessory(tk, b.app)
		}
		views = append(views, v)
	}
	recentRuns, archivedRuns := b.threadRuns(sp, all)
	d := ThreadDetail{
		SpaceID:           sp.ID,
		ParentID:          parent.ID,
		Parent:            &parentView,
		Replies:           views,
		Participants:      threadParticipants(sp, all, b.app),
		ChannelAgents:     spaceAgentIDs(sp),
		AgentModes:        effectiveThreadModes(sp, parent.ID),
		RecentRuns:        recentRuns,
		ArchivedRunsCount: archivedRuns,
	}
	for _, r := range d.RecentRuns {
		if r.Status == "running" || r.Status == "queued" {
			d.ActiveWorkerID = r.AgentID
			break
		}
	}
	if len(replies) > 0 {
		d.LastReplyTime = replies[len(replies)-1].CreatedAt
	}
	return d
}

func singleMessageToView(sp *space.Space, m space.Message, a appAccessor) MessageView {
	return baseMessageView(sp, m, personaResolver(a))
}

func threadKind(k space.Kind) bool {
	return k == space.KindChannel || k == space.KindDirectChat
}

func effectiveThreadModes(sp *space.Space, parentID string) map[string]string {
	out := map[string]string{}
	for id, mode := range sp.AgentModes {
		out[id] = mode
	}
	for id, mode := range sp.ThreadAgentModes[parentID] {
		out[id] = mode
	}
	return out
}

func groupReplies(sp *space.Space) map[string][]space.Message {
	g := map[string][]space.Message{}
	for _, m := range sp.Messages {
		if pid := strings.TrimSpace(m.ParentMessageID); pid != "" {
			g[pid] = append(g[pid], m)
		}
	}
	return g
}

func indexMessages(msgs []space.Message) map[string]space.Message {
	out := make(map[string]space.Message, len(msgs))
	for _, m := range msgs {
		out[m.ID] = m
	}
	return out
}

func findMessage(msgs []space.Message, id string) (*space.Message, bool) {
	for i := range msgs {
		if msgs[i].ID == id {
			return &msgs[i], true
		}
	}
	return nil, false
}

func (b *Backend) summarize(sp *space.Space, root space.Message, replies []space.Message, tasks map[string]*taskpkg.Task) ThreadSummary {
	last := replies[len(replies)-1]
	running := false
	if _, ok := tasks[root.ID]; ok {
		running = true
	} else {
		for _, r := range replies {
			if _, ok := tasks[r.ID]; ok {
				running = true
				break
			}
		}
	}
	return ThreadSummary{
		ParentID:         root.ID,
		ParentPreview:    preview(root.Content, previewLen),
		ReplyCount:       len(replies),
		LastReplyTime:    last.CreatedAt,
		LastReplyAuthor:  authorDisplay(sp, last, b.app),
		HasRunningWorker: running,
	}
}

func preview(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	rl := []rune(s)
	if len(rl) <= n {
		return s
	}
	return string(rl[:n]) + "…"
}

func authorDisplay(sp *space.Space, m space.Message, a appAccessor) string {
	if a != nil && m.AuthorKind == space.ParticipantAgent {
		if p := a.Personas().Get(m.AuthorID); p != nil && strings.TrimSpace(p.Display) != "" {
			return p.Display
		}
	}
	for _, p := range sp.Participants {
		if p.ID == m.AuthorID && strings.TrimSpace(p.Display) != "" {
			return p.Display
		}
	}
	return m.AuthorID
}

func (b *Backend) runningTasks(spaceID string) map[string]*taskpkg.Task {
	out := map[string]*taskpkg.Task{}
	if b.app.Tasks() == nil {
		return out
	}
	tasks, err := b.app.Tasks().ListBySpace(spaceID)
	if err != nil {
		return out
	}
	for _, tk := range tasks {
		if tk.Status.Active() {
			out[tk.TriggerMessageID] = tk
		}
	}
	return out
}

func threadParticipants(sp *space.Space, msgs []space.Message, a appAccessor) []AgentItem {
	seen := map[string]bool{}
	out := make([]AgentItem, 0)
	for _, m := range msgs {
		id := strings.TrimSpace(m.AuthorID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, threadAgentItem(sp, m, a))
	}
	return out
}

func threadAgentItem(sp *space.Space, m space.Message, a appAccessor) AgentItem {
	role := ""
	for _, p := range sp.Participants {
		if p.ID == m.AuthorID {
			role = p.Role
			break
		}
	}
	if a != nil && m.AuthorKind == space.ParticipantAgent {
		if p := a.Personas().Get(m.AuthorID); p != nil && strings.TrimSpace(p.Description) != "" {
			role = p.Description
		}
	}
	return AgentItem{
		ID:      m.AuthorID,
		Display: authorDisplay(sp, m, a),
		Role:    role,
		Runtime: personaRuntime(m.AuthorID, a),
		Model:   personaModel(m.AuthorID, a),
		Status:  "idle",
	}
}

func (b *Backend) threadRuns(sp *space.Space, msgs []space.Message) ([]AgentRun, int) {
	if b.app.Tasks() == nil {
		return []AgentRun{}, 0
	}
	if sp == nil {
		return []AgentRun{}, 0
	}
	all, err := b.app.Tasks().ListBySpace(sp.ID)
	if err != nil {
		return []AgentRun{}, 0
	}
	allowed := map[string]bool{}
	for _, m := range msgs {
		allowed[m.ID] = true
	}
	out := make([]AgentRun, 0)
	archived := 0
	for _, tk := range all {
		if allowed[tk.TriggerMessageID] {
			if tk.Status.Active() {
				out = append(out, agentRunFromTask(tk, sp))
			} else {
				archived++
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, archived
}
