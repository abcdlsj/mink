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
	SpaceID         string         `json:"space_id"`
	ParentID        string         `json:"parent_id"`
	Parent          *MessageView   `json:"parent,omitempty"`
	Replies         []MessageView  `json:"replies"`
	Participants    []AgentItem    `json:"participants,omitempty"`
	RecentRuns      []AgentRun     `json:"recent_runs,omitempty"`
	ActiveWorkerID  string         `json:"active_worker_id,omitempty"`
	LastReplyTime   time.Time      `json:"last_reply_time,omitempty"`
	NotFound        bool           `json:"not_found,omitempty"`
	Unsupported     bool           `json:"unsupported,omitempty"`
	UnsupportedHint string         `json:"unsupported_hint,omitempty"`
}

const threadParentPreviewLen = 120

func (b *Backend) ListThreadsForSpace(spaceID string) []ThreadSummary {
	if b.app == nil || b.app.Spaces() == nil {
		return nil
	}
	sp, err := b.app.Spaces().LoadSpace(strings.TrimSpace(spaceID))
	if err != nil || sp == nil {
		return nil
	}
	if !threadKindSupported(sp.Kind) {
		return nil
	}
	groups := groupRepliesByParent(sp)
	if len(groups) == 0 {
		return nil
	}
	parentIndex := indexMessages(sp.Messages)
	taskIndex := b.indexRunningTasksBySpace(sp.ID)
	out := make([]ThreadSummary, 0, len(groups))
	for parentID, replies := range groups {
		root, ok := parentIndex[parentID]
		if !ok {
			continue
		}
		s := b.summarizeThreadWithBackend(sp, root, replies, taskIndex)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastReplyTime.After(out[j].LastReplyTime) })
	return out
}

func (b *Backend) GetThreadDetail(spaceID, parentID string) ThreadDetail {
	parentID = strings.TrimSpace(parentID)
	spaceID = strings.TrimSpace(spaceID)
	if b.app == nil || b.app.Spaces() == nil {
		return ThreadDetail{SpaceID: spaceID, ParentID: parentID, NotFound: true}
	}
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ThreadDetail{SpaceID: spaceID, ParentID: parentID, NotFound: true}
	}
	if !threadKindSupported(sp.Kind) {
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
	allInThread := append([]space.Message{*parent}, replies...)
	parentView := singleMessageToView(sp, *parent, b.app)
	replyViews := make([]MessageView, 0, len(replies))
	for _, r := range replies {
		replyViews = append(replyViews, singleMessageToView(sp, r, b.app))
	}
	detail := ThreadDetail{
		SpaceID:      sp.ID,
		ParentID:     parent.ID,
		Parent:       &parentView,
		Replies:      replyViews,
		Participants: threadParticipants(sp, allInThread, b.app),
		RecentRuns:   b.threadRuns(sp.ID, allInThread),
	}
	for _, run := range detail.RecentRuns {
		if run.Status == "running" || run.Status == "queued" {
			detail.ActiveWorkerID = run.AgentID
			break
		}
	}
	if len(replies) > 0 {
		detail.LastReplyTime = replies[len(replies)-1].CreatedAt
	}
	return detail
}

func singleMessageToView(sp *space.Space, m space.Message, a appAccessor) MessageView {
	resolver := personaResolver(a)
	return MessageView{
		ID:         m.ID,
		Role:       roleForKind(m.AuthorKind),
		AuthorID:   m.AuthorID,
		AuthorName: space.MessageAuthorDisplay(sp, m, resolver),
		Content:    m.Content,
		Reasoning:  m.Reasoning,
		Time:       m.CreatedAt,
		ThreadID:   m.ParentMessageID,
	}
}

func threadKindSupported(k space.Kind) bool {
	return k == space.KindChannel || k == space.KindDirectChat
}

func groupRepliesByParent(sp *space.Space) map[string][]space.Message {
	groups := map[string][]space.Message{}
	for _, m := range sp.Messages {
		if strings.TrimSpace(m.ParentMessageID) == "" {
			continue
		}
		groups[m.ParentMessageID] = append(groups[m.ParentMessageID], m)
	}
	return groups
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

func summarizeThread(sp *space.Space, root space.Message, replies []space.Message, taskIndex map[string]*taskpkg.Task) ThreadSummary {
	last := replies[len(replies)-1]
	hasRunning := false
	if _, ok := taskIndex[root.ID]; ok {
		hasRunning = true
	} else {
		for _, r := range replies {
			if _, ok := taskIndex[r.ID]; ok {
				hasRunning = true
				break
			}
		}
	}
	return ThreadSummary{
		ParentID:         root.ID,
		ParentPreview:    previewText(root.Content, threadParentPreviewLen),
		ReplyCount:       len(replies),
		LastReplyTime:    last.CreatedAt,
		LastReplyAuthor:  authorDisplayForMessage(sp, last, nil),
		HasRunningWorker: hasRunning,
	}
}

func (b *Backend) summarizeThreadWithBackend(sp *space.Space, root space.Message, replies []space.Message, taskIndex map[string]*taskpkg.Task) ThreadSummary {
	s := summarizeThread(sp, root, replies, taskIndex)
	if last := replies[len(replies)-1]; last.AuthorID != "" {
		s.LastReplyAuthor = authorDisplayForMessage(sp, last, b.app)
	}
	return s
}

func previewText(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	rl := []rune(s)
	if len(rl) <= n {
		return s
	}
	return string(rl[:n]) + "…"
}

func authorDisplayForMessage(sp *space.Space, m space.Message, accessor appAccessor) string {
	if accessor != nil && m.AuthorKind == space.ParticipantAgent {
		if p := accessor.Personas().Get(m.AuthorID); p != nil && strings.TrimSpace(p.Display) != "" {
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

func (b *Backend) indexRunningTasksBySpace(spaceID string) map[string]*taskpkg.Task {
	out := map[string]*taskpkg.Task{}
	if b.app == nil || b.app.Tasks() == nil {
		return out
	}
	tasks, err := b.app.Tasks().ListBySpace(spaceID)
	if err != nil {
		return out
	}
	for _, tk := range tasks {
		if tk.Status == taskpkg.StatusRunning || tk.Status == taskpkg.StatusQueued {
			out[tk.TriggerMessageID] = tk
		}
	}
	return out
}

func threadParticipants(sp *space.Space, msgs []space.Message, accessor appAccessor) []AgentItem {
	seen := map[string]bool{}
	out := make([]AgentItem, 0)
	for _, m := range msgs {
		id := strings.TrimSpace(m.AuthorID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, agentItemForAuthor(sp, m, accessor))
	}
	return out
}

func agentItemForAuthor(sp *space.Space, m space.Message, accessor appAccessor) AgentItem {
	display := authorDisplayForMessage(sp, m, accessor)
	role := ""
	for _, p := range sp.Participants {
		if p.ID == m.AuthorID {
			role = p.Role
			break
		}
	}
	if accessor != nil && m.AuthorKind == space.ParticipantAgent {
		if p := accessor.Personas().Get(m.AuthorID); p != nil && strings.TrimSpace(p.Description) != "" {
			role = p.Description
		}
	}
	return AgentItem{
		ID:      m.AuthorID,
		Display: display,
		Role:    role,
		Status:  "idle",
	}
}

func (b *Backend) threadRuns(spaceID string, msgs []space.Message) []AgentRun {
	if b.app == nil || b.app.Tasks() == nil {
		return nil
	}
	allTasks, err := b.app.Tasks().ListBySpace(spaceID)
	if err != nil {
		return nil
	}
	allowed := map[string]bool{}
	for _, m := range msgs {
		allowed[m.ID] = true
	}
	out := make([]AgentRun, 0)
	for _, tk := range allTasks {
		if !allowed[tk.TriggerMessageID] {
			continue
		}
		out = append(out, agentRunFromTask(tk))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}
