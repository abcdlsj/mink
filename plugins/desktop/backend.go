package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/command"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

const desktopSource = "desktop"

const defaultChannelID = "desktop:default"

type Backend struct {
	app    *app.App
	subs   *fanout
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

func newBackend(a *app.App) *Backend {
	return &Backend{app: a, subs: newFanout(), cancel: map[string]context.CancelFunc{}}
}

func (b *Backend) WorkspaceInfo() WorkspaceState {
	if b.app == nil {
		return mockState()
	}
	cfg := b.app.Config()
	current := b.app.CurrentModel()
	provider, model := splitModel(current)
	return WorkspaceState{
		Workspace: cfg.Workspace,
		Provider:  provider,
		Model:     model,
		Runtime:   cfg.Runtime,
		Ready:     provider != "" && provider != "(unconfigured)",
		DataDir:   cfg.DataRoot(),
	}
}

func (b *Backend) ListSessions() ([]SessionItem, error) {
	if b.app == nil {
		out := []SessionItem{}
		for _, c := range mockChannels() {
			out = append(out, SessionItem{
				ID:        c.ID,
				Title:     "#" + c.Name,
				Runtime:   "local",
				Model:     "claude-sonnet-4",
				UpdatedAt: c.UpdatedAt,
				Running:   c.HasRunning,
			})
		}
		return out, nil
	}
	idx, err := b.app.SessionIndex()
	if err != nil {
		return nil, err
	}
	out := make([]SessionItem, 0, len(idx))
	for _, m := range idx {
		out = append(out, SessionItem{
			ID:           m.ID,
			Title:        fallback(m.Title, "(untitled)"),
			UpdatedAt:    m.UpdatedAt,
			MessageCount: m.Messages,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (b *Backend) GetSession(id string) (SessionDetail, error) {
	if b.app == nil {
		return mockChannelDetail(id), nil
	}
	return b.GetThread(id), nil
}

func (b *Backend) SendMessage(req SendRequest) (string, error) {
	if b.app == nil {
		return "", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	b.cancel[req.SessionID] = cancel
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.cancel, req.SessionID)
		b.mu.Unlock()
		cancel()
	}()
	source := desktopSource
	var sp *space.Space
	if loaded, err := b.app.Spaces().LoadSpace(req.SessionID); err == nil && loaded != nil {
		sp = loaded
		switch sp.Kind {
		case space.KindChannel:
			source = desktopSource
		case space.KindDirectChat:
			source = "desktop:direct:" + sp.Title
		case space.KindAgentDM:
			// Multi-instance: address by Space id directly so the
			// resolver picks the right conversation regardless of
			// title state. Singleton AgentDM Spaces also resolve here
			// since their ids share the same prefix shape.
			source = "desktop:agent:" + sp.ID
		}
	} else if strings.HasPrefix(req.SessionID, "desktop:agent:") {
		source = req.SessionID
	} else if isThreadID(req.SessionID) {
		if _, err := b.app.SwitchSession(desktopSource, req.SessionID); err != nil {
			return "", err
		}
	}
	parentID := strings.TrimSpace(req.ParentMessageID)
	if parentID != "" {
		if sp == nil || sp.Kind == space.KindAgentDM {
			return "", fmt.Errorf("threads are not supported in this Space kind")
		}
		normalized, ok := b.normalizeThreadParentID(sp, parentID)
		if !ok {
			return "", fmt.Errorf("thread parent message %q not found in this Space", parentID)
		}
		ctx = command.WithParentMessage(ctx, normalized)
	}
	if req.PersonaID != "" {
		return b.app.HandleInputAs(ctx, source, req.PersonaID, req.Input)
	}
	return b.app.HandleInput(ctx, source, req.Input)
}

func (b *Backend) normalizeThreadParentID(sp *space.Space, parentID string) (string, bool) {
	target, ok := findMessage(sp.Messages, parentID)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(target.ParentMessageID) != "" {
		return target.ParentMessageID, true
	}
	return target.ID, true
}

func (b *Backend) StopTurn(sessionID string) error {
	b.mu.Lock()
	cancel := b.cancel[sessionID]
	delete(b.cancel, sessionID)
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// NewDirectChat creates a fresh KindDirectChat Space. Each call
// produces a new Space with its own id; participants seed with the
// user only (no agent binding). Agents join later when the user
// @-mentions them via the routing layer (P3.5). The Space title
// starts as "New chat" and may be polished from the first user
// message in a future commit.
func (b *Backend) NewDirectChat() (SessionDetail, error) {
	if b.app == nil {
		return SessionDetail{}, nil
	}
	seed := newDirectChatSeed()
	sp, err := b.app.Spaces().EnsureSpace(space.KindDirectChat, seed, space.PersonaInfo{})
	if err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        directChatTitle(sp),
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		Messages: spaceMessagesToView(sp, b.app),
	}, nil
}

// ListDirectChats returns every persisted KindDirectChat Space.
// The frontend renders this as the left rail's "Direct Chats"
// group.
//
// Per Iris's polish ruling: at most one empty (zero-message) chat
// is kept in the result so the rail can act as a draft/start row
// without piling up. Older empties are dropped silently (the user
// can always create a new one). Non-empty direct chats are always
// included.
func (b *Backend) ListDirectChats() []DirectChatItem {
	if b.app == nil {
		return nil
	}
	spaces, err := b.app.Spaces().ListSpaces()
	if err != nil {
		return nil
	}
	type entry struct {
		sp    *space.Space
		empty bool
	}
	all := make([]entry, 0)
	for _, sp := range spaces {
		if sp.Kind != space.KindDirectChat {
			continue
		}
		all = append(all, entry{sp: sp, empty: len(sp.Messages) == 0})
	}
	// Sort newest-first so the kept empty (if any) is the most
	// recently created one — matches the user's intent after they
	// just clicked New.
	sort.Slice(all, func(i, j int) bool { return all[i].sp.UpdatedAt.After(all[j].sp.UpdatedAt) })

	out := make([]DirectChatItem, 0, len(all))
	keptEmpty := false
	for _, e := range all {
		if e.empty {
			if keptEmpty {
				continue
			}
			keptEmpty = true
		}
		out = append(out, DirectChatItem{
			ID:        e.sp.ID,
			Title:     directChatTitle(e.sp),
			Agents:    spaceAgentIDs(e.sp),
			UpdatedAt: e.sp.UpdatedAt,
		})
	}
	return out
}

// GetDirectChat loads one direct-chat Space by id and projects it
// for the center pane.
func (b *Backend) GetDirectChat(id string) SessionDetail {
	if b.app == nil {
		return SessionDetail{}
	}
	sp, err := b.app.Spaces().LoadSpace(id)
	if err != nil || sp == nil || sp.Kind != space.KindDirectChat {
		return SessionDetail{}
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        directChatTitle(sp),
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		Messages: spaceMessagesToView(sp, b.app),
	}
}

// directChatTitle picks a display title for a KindDirectChat Space.
// We prefer the first user message preview when the seed has not
// been promoted to a real title yet.
func directChatTitle(sp *space.Space) string {
	if sp == nil {
		return "New chat"
	}
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			if t := strings.TrimSpace(m.Content); t != "" {
				return previewTitle(t)
			}
		}
	}
	if t := strings.TrimSpace(sp.Title); t != "" && !strings.HasPrefix(t, "dchat-") {
		return t
	}
	return "New chat"
}

func previewTitle(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) <= 48 {
		return s
	}
	r := []rune(s)
	return string(r[:48]) + "…"
}

// newDirectChatSeed produces a unique seed for a fresh direct chat.
// Seeds are namespaced with "dchat-" so they don't visually look
// like a user-supplied title.
func newDirectChatSeed() string {
	return "dchat-" + time.Now().Format("20060102-150405") + "-" + uuid.NewString()[:4]
}

func (b *Backend) ListChannels() []ChannelItem {
	if b.app == nil {
		return mockChannels()
	}
	// P3.1: list real Channel-kind Spaces. The default workspace
	// channel is auto-created by the dual-write/router on first
	// channel input; if it doesn't exist yet we still surface a
	// placeholder row so the rail isn't empty on a fresh install.
	cfg := b.app.Config()
	spaces, err := b.app.Spaces().Store().ListSpaces()
	if err == nil {
		out := make([]ChannelItem, 0, 1)
		for _, sp := range spaces {
			if sp.Kind != space.KindChannel {
				continue
			}
			out = append(out, ChannelItem{
				ID:        sp.ID,
				Name:      channelDisplayName(sp, cfg.Workspace),
				Topic:     "",
				Agents:    spaceAgentIDs(sp),
				UpdatedAt: sp.UpdatedAt,
			})
		}
		if len(out) > 0 {
			sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
			return out
		}
	}
	// Empty state: synthesize a placeholder so the rail still shows
	// a #workspace entry, click-through ensures the Space is created.
	return []ChannelItem{
		{
			ID:        defaultChannelID,
			Name:      workspaceName(cfg.Workspace),
			Topic:     "",
			Agents:    personaIDs(b.app),
			UpdatedAt: time.Now(),
		},
	}
}

// ListRecent returns the recent-activity aggregator for the left
// rail. It walks every persisted Space, surfaces a kind-tagged row
// per entry, and sorts by updated_at descending. Recent is a
// derived view: clicking an item should dispatch by kind back to
// the kind-specific detail endpoint, not to /api/thread.
func (b *Backend) ListRecent() []RecentItem {
	if b.app == nil {
		return nil
	}
	spaces, err := b.app.Spaces().ListSpaces()
	if err != nil {
		return nil
	}
	cfg := b.app.Config()
	out := make([]RecentItem, 0, len(spaces))
	for _, sp := range spaces {
		var item RecentItem
		switch sp.Kind {
		case space.KindChannel:
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "channel",
				Title:     "#" + channelDisplayName(sp, cfg.Workspace),
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		case space.KindDirectChat:
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "direct_chat",
				Title:     directChatTitle(sp),
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		case space.KindAgentDM:
			display := sp.Title
			if p := b.app.Personas().Get(sp.Title); p != nil {
				display = p.Display
			}
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "agent_dm",
				Title:     "@" + display,
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		default:
			continue
		}
		// Skip spaces with no activity (channel default, brand-new
		// agent DMs that have never been used). Recent should only
		// surface things the user has actually touched.
		if len(sp.Messages) == 0 {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// recentSubtitle picks a one-line preview from the last message in
// a Space. Empty when the space has no messages.
func recentSubtitle(sp *space.Space) string {
	if sp == nil || len(sp.Messages) == 0 {
		return ""
	}
	last := sp.Messages[len(sp.Messages)-1]
	c := strings.TrimSpace(last.Content)
	if c == "" {
		return ""
	}
	c = strings.ReplaceAll(c, "\n", " ")
	if len([]rune(c)) > 60 {
		r := []rune(c)
		c = string(r[:60]) + "…"
	}
	prefix := ""
	switch last.AuthorKind {
	case space.ParticipantUser:
		prefix = "You: "
	case space.ParticipantAgent:
		display := last.AuthorID
		prefix = display + ": "
	}
	return prefix + c
}
// Channel Spaces are auto-created with title "default" today; we
// surface the workspace folder name instead so users see "#sumi"
// rather than "#default".
func channelDisplayName(sp *space.Space, workspace string) string {
	if sp == nil {
		return ""
	}
	if strings.TrimSpace(sp.Title) == "" || sp.Title == "default" {
		return workspaceName(workspace)
	}
	return sp.Title
}

// spaceAgentIDs returns the persona ids of every agent participant
// currently in the space, in stable order.
func spaceAgentIDs(sp *space.Space) []string {
	if sp == nil {
		return nil
	}
	out := make([]string, 0, len(sp.Participants))
	for _, p := range sp.Participants {
		if p.Kind == space.ParticipantAgent {
			out = append(out, p.ID)
		}
	}
	sort.Strings(out)
	return out
}

// ListThreads previously walked desktop sessions to fake threads,
// then briefly returned KindDirectChat spaces under the old shape.
// Per Iris's P3.4 review, endpoint semantics must not blur Spaces
// into the Thread concept — threads are parent_message replies,
// which v1 doesn't have yet. Until /api/threads is removed in P3.6
// it returns an empty list so the frontend cannot accidentally
// depend on it carrying Direct Chats.
func (b *Backend) ListThreads() []ThreadItem {
	if b.app == nil {
		return mockThreads()
	}
	return []ThreadItem{}
}

func (b *Backend) ListAgents() []AgentItem {
	if b.app == nil {
		return mockAgents()
	}
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, AgentItem{ID: p.ID, Display: p.Display, Role: p.Description, Status: "idle"})
	}
	return out
}

func (b *Backend) CreateChannel(name string) (ChannelItem, error) {
	if b.app == nil {
		return ChannelItem{}, fmt.Errorf("app not initialized")
	}
	seed := normalizeChannelSeed(name)
	if seed == "" {
		return ChannelItem{}, fmt.Errorf("channel name required")
	}
	if existing, err := b.app.Spaces().Store().FindSpaceByKindAndSeed(space.KindChannel, seed); err == nil && existing != nil {
		return ChannelItem{}, fmt.Errorf("channel %q already exists", seed)
	}
	sp, err := b.app.Spaces().EnsureSpace(space.KindChannel, seed, space.PersonaInfo{})
	if err != nil {
		return ChannelItem{}, err
	}
	return ChannelItem{
		ID:        sp.ID,
		Name:      channelDisplayName(sp, b.app.Config().Workspace),
		Topic:     "",
		Agents:    spaceAgentIDs(sp),
		UpdatedAt: sp.UpdatedAt,
	}, nil
}

func normalizeChannelSeed(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
			prevDash = false
		case r == '-' || r == '_':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		case r == ' ':
			if !prevDash && b.Len() > 0 {
				b.WriteRune('-')
				prevDash = true
			}
		}
		if b.Len() >= 32 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func (b *Backend) GetChannel(id string) SessionDetail {
	if b.app == nil {
		return mockChannelDetail(id)
	}
	cfg := b.app.Config()
	// P3.1: prefer the real Channel-kind Space. If id refers to a
	// concrete Space we load it directly; otherwise (legacy
	// defaultChannelID, fresh install) we fall back to the singleton
	// channel default and create it if missing.
	var sp *space.Space
	if strings.HasPrefix(id, "20") { // Space ids start with the date
		if loaded, err := b.app.Spaces().LoadSpace(id); err == nil && loaded != nil {
			sp = loaded
		}
	}
	if sp == nil {
		ensured, err := b.app.Spaces().EnsureSpace(space.KindChannel, "default", space.PersonaInfo{})
		if err != nil {
			return SessionDetail{}
		}
		sp = ensured
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        "#" + channelDisplayName(sp, cfg.Workspace),
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		Summary:  "",
		Messages: spaceMessagesToView(sp, b.app),
	}
}

func spaceMessagesToView(sp *space.Space, a appAccessor) []MessageView {
	if sp == nil {
		return nil
	}
	resolver := personaResolver(a)
	var threadInfo map[string]ThreadSummary
	var taskIndex map[string]*taskpkg.Task
	var accessoryIndex map[string]*taskpkg.Task
	if threadKind(sp.Kind) {
		threadInfo, taskIndex = computeThreadInfo(sp, a)
		accessoryIndex = computeTaskAccessoryIndex(sp, a)
	}
	out := make([]MessageView, 0, len(sp.Messages))
	for _, m := range sp.Messages {
		view := MessageView{
			ID:         m.ID,
			Role:       roleForKind(m.AuthorKind),
			AuthorID:   m.AuthorID,
			AuthorName: space.MessageAuthorDisplay(sp, m, resolver),
			Content:    m.Content,
			Reasoning:  m.Reasoning,
			Time:       m.CreatedAt,
			ThreadID:   m.ParentMessageID,
		}
		if m.ParentMessageID != "" {
			view.IsThreadReply = true
		}
		if info, ok := threadInfo[m.ID]; ok {
			summary := info
			view.ThreadInfo = &summary
		}
		if tk, ok := accessoryIndex[m.ID]; ok && tk != nil {
			view.TaskAccessory = projectTaskAccessory(tk, a)
		}
		_ = taskIndex
		out = append(out, view)
	}
	return out
}

func computeTaskAccessoryIndex(sp *space.Space, a appAccessor) map[string]*taskpkg.Task {
	out := map[string]*taskpkg.Task{}
	if a == nil || a.Tasks() == nil {
		return out
	}
	tasks, err := a.Tasks().ListBySpace(sp.ID)
	if err != nil {
		return out
	}
	for _, tk := range tasks {
		if tk == nil || strings.TrimSpace(tk.TriggerMessageID) == "" {
			continue
		}
		prev, ok := out[tk.TriggerMessageID]
		if !ok || tk.CreatedAt.After(prev.CreatedAt) {
			out[tk.TriggerMessageID] = tk
		}
	}
	return out
}

func projectTaskAccessory(tk *taskpkg.Task, a appAccessor) *TaskAccessoryInfo {
	if tk == nil {
		return nil
	}
	info := &TaskAccessoryInfo{
		TaskID:        tk.ID,
		WorkerID:      tk.WorkerID,
		WorkerDisplay: resolveWorkerDisplay(tk.WorkerID, a),
		Status:        taskStatusForUI(tk.Status),
	}
	switch tk.Status {
	case taskpkg.StatusFinished, taskpkg.StatusFailed, taskpkg.StatusCanceled, taskpkg.StatusEmptyOutput:
		info.Terminal = true
	}
	if tk.Status == taskpkg.StatusFailed || tk.Status == taskpkg.StatusCanceled {
		info.ShortOutcome = shortOutcome(tk.Outcome)
	}
	return info
}

func shortOutcome(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	rl := []rune(s)
	if len(rl) <= 80 {
		return s
	}
	return string(rl[:80]) + "…"
}

func computeThreadInfo(sp *space.Space, a appAccessor) (map[string]ThreadSummary, map[string]*taskpkg.Task) {
	groups := groupReplies(sp)
	if len(groups) == 0 {
		return nil, nil
	}
	parentIndex := indexMessages(sp.Messages)
	taskIndex := map[string]*taskpkg.Task{}
	if a != nil && a.Tasks() != nil {
		if tasks, err := a.Tasks().ListBySpace(sp.ID); err == nil {
			for _, tk := range tasks {
				if tk.Status == taskpkg.StatusRunning || tk.Status == taskpkg.StatusQueued {
					taskIndex[tk.TriggerMessageID] = tk
				}
			}
		}
	}
	out := make(map[string]ThreadSummary, len(groups))
	for parentID, replies := range groups {
		root, ok := parentIndex[parentID]
		if !ok {
			continue
		}
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
		out[parentID] = ThreadSummary{
			ParentID:         root.ID,
			ParentPreview:    preview(root.Content, previewLen),
			ReplyCount:       len(replies),
			LastReplyTime:    last.CreatedAt,
			LastReplyAuthor:  authorDisplay(sp, last, a),
			HasRunningWorker: hasRunning,
		}
	}
	return out, taskIndex
}

// roleForKind maps a Space participant kind to the UI's role string.
func roleForKind(k space.ParticipantKind) string {
	switch k {
	case space.ParticipantUser:
		return "user"
	case space.ParticipantAgent:
		return "agent"
	}
	return ""
}

func personaResolver(a appAccessor) space.DisplayResolver {
	if a == nil {
		return nil
	}
	return space.DisplayResolverFunc(func(id string) string {
		if p := a.Personas().Get(id); p != nil {
			return p.Display
		}
		return ""
	})
}

// appAccessor is the small slice of *app.App the Space-projection
// helpers need. Pulling it out behind an interface keeps these
// helpers testable without spinning up a full App.
type appAccessor interface {
	Personas() *persona.Registry
	Tasks() *taskpkg.Manager
}

// GetThread is the legacy thread-detail endpoint. v1 has no real
// parent_message threads yet, so the only reason a frontend would
// hit this endpoint is to load a Space detail through the wrong
// route. Per Iris's P3.5 review we don't want a "/api/thread can
// load any Space" loophole — the proper endpoints are
// /api/channel, /api/direct-chat, /api/agent-dm. This handler
// returns an empty SessionDetail to make stale links 404 cleanly.
//
// The endpoint stays as a stub for one release so existing app
// builds don't break on missing routes; P4 removes the route.
func (b *Backend) GetThread(id string) SessionDetail {
	if b.app == nil {
		return mockThreadDetail(id)
	}
	return SessionDetail{}
}

// attachDelegateOutcomes / replayDelegateTask / subtaskSteps /
// taskAsRun were P2.6 helpers that synthesized delegate task
// playback by walking the legacy session store. P3.6 removed every
// caller; the helpers themselves go away with the upcoming Task
// store migration in P5. The git history for this file holds the
// reference if needed.

func timeInWindow(t, lo, hi time.Time) bool {
	if lo.IsZero() && hi.IsZero() {
		return true
	}
	if !lo.IsZero() && t.Before(lo.Add(-2*time.Second)) {
		return false
	}
	if !hi.IsZero() && t.After(hi.Add(2*time.Second)) {
		return false
	}
	return true
}

func dropExploratoryErrors(steps []DelegateStep) []DelegateStep {
	out := steps[:0]
	for _, s := range steps {
		if s.Status == "error" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// humanizeStep turns a tool call into a one-line action summary the
// reader can scan without reading raw output.
func humanizeStep(tool, rawArgs, workspace string) string {
	verb := stepVerb(tool)
	target := stepTarget(tool, rawArgs, workspace)
	if target == "" {
		return verb
	}
	return verb + " " + target
}

func stepVerb(tool string) string {
	switch strings.ToLower(tool) {
	case "read":
		return "read"
	case "list_files", "ls":
		return "listed"
	case "bash", "shell", "exec":
		return "ran"
	case "write":
		return "wrote"
	case "edit", "patch":
		return "edited"
	case "grep", "search":
		return "searched"
	default:
		return tool
	}
}

func stepTarget(tool, rawArgs, workspace string) string {
	if rawArgs == "" {
		return ""
	}
	var args struct {
		Path string `json:"path"`
		Cmd  string `json:"cmd"`
		File string `json:"file"`
		Q    string `json:"query"`
		Dir  string `json:"dir"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	switch strings.ToLower(tool) {
	case "read", "write", "edit", "patch":
		if p := strings.TrimSpace(args.Path); p != "" {
			return projectRel(p, workspace)
		}
		if p := strings.TrimSpace(args.File); p != "" {
			return projectRel(p, workspace)
		}
	case "list_files", "ls":
		if p := strings.TrimSpace(args.Path); p != "" {
			return strings.TrimSuffix(projectRel(p, workspace), "/") + "/"
		}
		if p := strings.TrimSpace(args.Dir); p != "" {
			return strings.TrimSuffix(projectRel(p, workspace), "/") + "/"
		}
	case "bash", "shell", "exec":
		if c := strings.TrimSpace(args.Cmd); c != "" {
			return shortCmd(c, workspace)
		}
	case "grep", "search":
		if q := strings.TrimSpace(args.Q); q != "" {
			return "for " + clip(q, 40)
		}
	}
	return ""
}

// projectRel rewrites an absolute path under the workspace into a
// relative-from-workspace form. Paths outside the workspace fall back
// to the basename so the rail never shows a full machine-local path.
func projectRel(p, workspace string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if workspace != "" && strings.HasPrefix(p, workspace+"/") {
		rel := strings.TrimPrefix(p, workspace+"/")
		return clip(rel, 60)
	}
	if workspace != "" && p == workspace {
		return "./"
	}
	if !strings.HasPrefix(p, "/") {
		return clip(p, 60)
	}
	return basenameOf(p)
}

func basenameOf(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// shortCmd reduces a shell command to something readable: strips a
// leading absolute workspace path and clips to ~60 chars while
// preserving the command head so the verb is visible.
func shortCmd(c, workspace string) string {
	c = strings.TrimSpace(c)
	if workspace != "" {
		c = strings.ReplaceAll(c, workspace+"/", "")
		c = strings.ReplaceAll(c, workspace, ".")
	}
	if i := strings.IndexAny(c, " \t"); i > 0 {
		head := c[:i]
		rest := strings.TrimSpace(c[i:])
		if rest == "" {
			return head
		}
		return clip(head+" "+rest, 60)
	}
	return clip(c, 60)
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 120 {
		r := []rune(s)
		s = string(r[:120]) + "…"
	}
	return s
}

func parseTaskID(s string) string {
	const tag = "task_id="
	i := strings.Index(s, tag)
	if i < 0 {
		return ""
	}
	rest := s[i+len(tag):]
	end := strings.IndexAny(rest, " \n\t,)")
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func questionFromArgs(rawArgs string) string {
	if rawArgs == "" {
		return ""
	}
	var args struct {
		Question string `json:"question"`
		Task     string `json:"task"`
		Prompt   string `json:"prompt"`
		Input    string `json:"input"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return ""
	}
	for _, v := range []string{args.Question, args.Task, args.Prompt, args.Input} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (b *Backend) GetParticipants(channelID, threadID string) ParticipantsView {
	if b.app == nil {
		return mockParticipants(channelID, threadID)
	}
	// P3.3: read participants directly off the Space. The
	// `threadID` query parameter is the active Space id (the
	// frontend just kept the legacy name); we resolve it through
	// space.Manager rather than synthesizing from session history.
	spaceID := strings.TrimSpace(threadID)
	if spaceID == "" {
		spaceID = strings.TrimSpace(channelID)
	}
	if spaceID == "" {
		return ParticipantsView{Agents: b.allAgents()}
	}
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ParticipantsView{}
	}
	return ParticipantsView{
		Agents:     spaceParticipantsAsAgents(sp, b.app),
		RecentRuns: b.spaceRecentRuns(sp),
	}
}

// spaceParticipantsAsAgents projects every Participant in a Space
// onto the rail's AgentItem shape. The user participant is dropped
// (the rail is for collaborators); persona display + role are
// resolved through the registry when present.
func spaceParticipantsAsAgents(sp *space.Space, a appAccessor) []AgentItem {
	if sp == nil {
		return nil
	}
	out := make([]AgentItem, 0, len(sp.Participants))
	for _, p := range sp.Participants {
		if p.Kind != space.ParticipantAgent {
			continue
		}
		display := p.Display
		role := p.Role
		if a != nil {
			if pp := a.Personas().Get(p.ID); pp != nil {
				if display == "" {
					display = pp.Display
				}
				if role == "" {
					role = pp.Description
				}
			}
		}
		if display == "" {
			display = p.ID
		}
		out = append(out, AgentItem{
			ID:      p.ID,
			Display: display,
			Role:    role,
			Status:  "idle",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Backend) spaceRecentRuns(sp *space.Space) []AgentRun {
	if sp == nil || b.app == nil || b.app.Tasks() == nil {
		return nil
	}
	tasks, err := b.app.Tasks().ListBySpace(sp.ID)
	if err != nil {
		return nil
	}
	out := make([]AgentRun, 0, len(tasks))
	for _, tk := range tasks {
		out = append(out, agentRunFromTask(tk))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

func agentRunFromTask(tk *taskpkg.Task) AgentRun {
	return AgentRun{
		ID:      tk.ID,
		AgentID: tk.WorkerID,
		Title:   tk.Title,
		Status:  taskStatusForUI(tk.Status),
		Time:    tk.UpdatedAt,
	}
}

func taskStatusForUI(s taskpkg.Status) string {
	switch s {
	case taskpkg.StatusEmptyOutput:
		return "no_output"
	default:
		return string(s)
	}
}

type RunStep struct {
	Kind  string    `json:"kind"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
}

type RunDetail struct {
	TaskID           string    `json:"task_id"`
	SpaceID          string    `json:"space_id"`
	WorkerID         string    `json:"worker_id"`
	WorkerName       string    `json:"worker_name,omitempty"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	Outcome          string    `json:"outcome,omitempty"`
	ResultMessageID  string    `json:"result_message_id,omitempty"`
	TriggerMessageID string    `json:"trigger_message_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	KeySteps         []RunStep `json:"key_steps,omitempty"`
}

func (b *Backend) GetRunDetail(taskID string) RunDetail {
	if b.app == nil || b.app.Tasks() == nil {
		return RunDetail{}
	}
	tk, err := b.app.Tasks().Get(strings.TrimSpace(taskID))
	if err != nil || tk == nil {
		return RunDetail{}
	}
	detail := RunDetail{
		TaskID:           tk.ID,
		SpaceID:          tk.SpaceID,
		WorkerID:         tk.WorkerID,
		WorkerName:       resolveWorkerDisplay(tk.WorkerID, b.app),
		Title:            tk.Title,
		Status:           taskStatusForUI(tk.Status),
		Outcome:          tk.Outcome,
		ResultMessageID:  tk.ResultMessageID,
		TriggerMessageID: tk.TriggerMessageID,
		CreatedAt:        tk.CreatedAt,
		UpdatedAt:        tk.UpdatedAt,
	}
	runs, err := b.app.Tasks().ListRuns(tk.ID)
	if err != nil {
		return detail
	}
	if len(runs) == 0 {
		return detail
	}
	latest := runs[0]
	for _, r := range runs[1:] {
		if r.StartedAt.After(latest.StartedAt) {
			latest = r
		}
	}
	steps := make([]RunStep, 0, len(latest.KeySteps))
	for _, s := range latest.KeySteps {
		steps = append(steps, RunStep{
			Kind:  string(s.Kind),
			Title: s.Title,
			At:    s.At,
			OK:    s.OK,
		})
	}
	detail.KeySteps = steps
	return detail
}

func resolveWorkerDisplay(workerID string, a appAccessor) string {
	if a == nil || strings.TrimSpace(workerID) == "" {
		return ""
	}
	if p := a.Personas().Get(workerID); p != nil && strings.TrimSpace(p.Display) != "" {
		return p.Display
	}
	return workerID
}

func (b *Backend) allAgents() []AgentItem {
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, AgentItem{ID: p.ID, Display: p.Display, Role: p.Description, Status: "idle"})
	}
	return out
}

func (b *Backend) taskAsRun(taskID, agentID string) *AgentRun {
	// Removed in P3.6 — see comment near subtaskSteps. Returns nil
	// so any leftover transitive caller behaves as "no replay".
	_ = taskID
	_ = agentID
	return nil
}

func (b *Backend) GetAgentDM(agentID string) SessionDetail {
	if b.app == nil {
		return SessionDetail{}
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return SessionDetail{}
	}
	var sp *space.Space
	if isAgentDMSpaceID(agentID) {
		if loaded, err := b.app.Spaces().LoadSpace(agentID); err == nil && loaded != nil && loaded.Kind == space.KindAgentDM {
			sp = loaded
		}
	}
	display := agentID
	role := ""
	if sp != nil {
		if pid := agentParticipantIDForBackend(sp); pid != "" {
			if p := b.app.Personas().Get(pid); p != nil {
				display = p.Display
				role = p.Description
			} else {
				display = pid
			}
		}
	} else {
		if p := b.app.Personas().Get(agentID); p != nil {
			display = p.Display
			role = p.Description
		}
		ensured, err := b.app.Spaces().EnsureSpace(space.KindAgentDM, agentID, space.PersonaInfo{
			ID:      agentID,
			Display: display,
			Role:    role,
		})
		if err != nil {
			return SessionDetail{}
		}
		sp = ensured
	}
	pid := agentParticipantIDForBackend(sp)
	if pid == "" {
		pid = strings.TrimSpace(agentID)
	}
	title := visibleAgentDMTitle(sp, pid)
	if title == "New chat" {
		title = "@" + display
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           sp.ID,
			Title:        title,
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		Messages: spaceMessagesToView(sp, b.app),
	}
}

// CreateAgentDM provisions a fresh AgentDM Space instance for the
// given persona. Each call returns a brand-new conversation; the
// "New → Message agent" flow uses this so left-rail history rows are
// addressable conversations, not the singleton history per persona.
func (b *Backend) CreateAgentDM(personaID string) (AgentDMItem, error) {
	if b.app == nil {
		return AgentDMItem{}, fmt.Errorf("app not initialized")
	}
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return AgentDMItem{}, fmt.Errorf("persona id required")
	}
	p := b.app.Personas().Get(personaID)
	if p == nil {
		return AgentDMItem{}, fmt.Errorf("persona not registered: %s", personaID)
	}
	seed := p.ID + "-" + uuid.NewString()[:8]
	info := space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
	sp, err := b.app.Spaces().EnsureSpace(space.KindAgentDM, seed, info)
	if err != nil {
		return AgentDMItem{}, err
	}
	return agentDMItemFromSpace(sp, b.app), nil
}

func (b *Backend) ListAgentDMs() []AgentDMItem {
	if b.app == nil {
		return nil
	}
	all, err := b.app.Spaces().Store().ListSpaces()
	if err != nil {
		return nil
	}
	out := make([]AgentDMItem, 0, len(all))
	for _, sp := range all {
		if sp.Kind != space.KindAgentDM {
			continue
		}
		out = append(out, agentDMItemFromSpace(sp, b.app))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func agentParticipantIDForBackend(sp *space.Space) string {
	if sp == nil {
		return ""
	}
	for _, p := range sp.Participants {
		if p.Kind == space.ParticipantAgent {
			return p.ID
		}
	}
	return ""
}

func agentDMItemFromSpace(sp *space.Space, a appAccessor) AgentDMItem {
	pid := agentParticipantIDForBackend(sp)
	display := pid
	if a != nil {
		if p := a.Personas().Get(pid); p != nil && strings.TrimSpace(p.Display) != "" {
			display = p.Display
		}
	}
	title := visibleAgentDMTitle(sp, pid)
	return AgentDMItem{
		ID:           sp.ID,
		PersonaID:    pid,
		PersonaName:  display,
		Title:        title,
		UpdatedAt:    sp.UpdatedAt,
		MessageCount: len(sp.Messages),
	}
}

func visibleAgentDMTitle(sp *space.Space, personaID string) string {
	if sp == nil {
		return "New chat"
	}
	t := strings.TrimSpace(sp.Title)
	if t == "" || isAgentDMMachineSeed(t, personaID) {
		return "New chat"
	}
	return t
}

func isAgentDMMachineSeed(t, personaID string) bool {
	if personaID == "" || !strings.HasPrefix(t, personaID+"-") {
		return false
	}
	tail := t[len(personaID)+1:]
	if len(tail) != 8 {
		return false
	}
	for _, r := range tail {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func isAgentDMSpaceID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 9 {
		return false
	}
	for i := 0; i < 8; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return s[8] == '-'
}

func (b *Backend) ListPersonas() []PersonaItem {
	if b.app == nil {
		return mockPersonas()
	}
	out := make([]PersonaItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, PersonaItem{
			ID:          p.ID,
			Display:     p.Display,
			Runtime:     p.Runtime,
			Description: p.Description,
			Tools:       p.Tools,
		})
	}
	return out
}

func (b *Backend) ListModels() []ModelItem {
	if b.app == nil {
		return mockModels()
	}
	cfg := b.app.Config()
	out := make([]ModelItem, 0, len(cfg.Models))
	for name, m := range cfg.Models {
		out = append(out, ModelItem{
			Name:          name,
			Provider:      m.Provider,
			Model:         m.Model,
			MaxTokens:     m.MaxTokens,
			ContextWindow: m.ContextWindow,
			Ready:         m.APIKey != "",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (b *Backend) ListTools() []ToolItem {
	return mockTools()
}

func (b *Backend) ListCommands() []CommandItem {
	if b.app == nil {
		return mockCommands()
	}
	cmds := b.app.Commands()
	out := make([]CommandItem, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, CommandItem{Name: "/" + c.Name(), Summary: c.Desc()})
	}
	return out
}

func (b *Backend) Subscribe() (<-chan BusEvent, func()) {
	return b.subs.subscribe(256)
}

func (b *Backend) MockStream(req SendRequest) {
	go runMockStream(b.subs, req)
}

func (b *Backend) APIHandler(mock bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", jsonHandler(func() any { return b.WorkspaceInfo() }))
	mux.HandleFunc("/api/sessions", jsonHandler(func() any {
		out, _ := b.ListSessions()
		return out
	}))
	mux.HandleFunc("/api/session", func(rw http.ResponseWriter, req *http.Request) {
		out, _ := b.GetSession(req.URL.Query().Get("id"))
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/channels", jsonHandler(func() any { return b.ListChannels() }))
	mux.HandleFunc("/api/channel/create", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.CreateChannel(in.Name)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/threads", jsonHandler(func() any { return b.ListThreads() }))
	mux.HandleFunc("/api/agents", jsonHandler(func() any { return b.ListAgents() }))
	mux.HandleFunc("/api/channel", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetChannel(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/thread", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetThread(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/participants", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetParticipants(req.URL.Query().Get("channel"), req.URL.Query().Get("thread")))
	})
	mux.HandleFunc("/api/agent-dm", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetAgentDM(req.URL.Query().Get("agent")))
	})
	mux.HandleFunc("/api/agent-dms", jsonHandler(func() any { return b.ListAgentDMs() }))
	mux.HandleFunc("/api/agent-dm/create", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			PersonaID string `json:"persona_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.CreateAgentDM(in.PersonaID)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/direct-chats", jsonHandler(func() any { return b.ListDirectChats() }))
	mux.HandleFunc("/api/direct-chat", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetDirectChat(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/recent", jsonHandler(func() any { return b.ListRecent() }))
	mux.HandleFunc("/api/run", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetRunDetail(req.URL.Query().Get("id")))
	})
	mux.HandleFunc("/api/threads-for-space", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.ListThreadsForSpace(req.URL.Query().Get("space")))
	})
	mux.HandleFunc("/api/thread-detail", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetThreadDetail(req.URL.Query().Get("space"), req.URL.Query().Get("parent")))
	})
	mux.HandleFunc("/api/new-direct", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		out, err := b.NewDirectChat()
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, out)
	})
	mux.HandleFunc("/api/events", b.handleEvents)
	mux.HandleFunc("/api/send", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in SendRequest
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if mock {
			b.MockStream(in)
			writeJSON(rw, map[string]string{"reply": ""})
			return
		}
		out, err := b.SendMessage(in)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(rw, map[string]string{"reply": out})
	})
	mux.HandleFunc("/api/stop", func(rw http.ResponseWriter, req *http.Request) {
		var in struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&in)
		if in.SessionID == "" {
			in.SessionID = req.URL.Query().Get("session")
		}
		_ = b.StopTurn(in.SessionID)
		writeJSON(rw, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/personas", jsonHandler(func() any { return b.ListPersonas() }))
	mux.HandleFunc("/api/models", jsonHandler(func() any { return b.ListModels() }))
	mux.HandleFunc("/api/tools", jsonHandler(func() any { return b.ListTools() }))
	mux.HandleFunc("/api/commands", jsonHandler(func() any { return b.ListCommands() }))
	return mux
}

func (b *Backend) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	events, cancel := b.Subscribe()
	defer cancel()
	flusher.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(rw, "event: bus\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func jsonHandler(get func() any) http.HandlerFunc {
	return func(rw http.ResponseWriter, _ *http.Request) {
		writeJSON(rw, get())
	}
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(rw)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (b *Backend) start(ctx context.Context) {
	if b.app == nil {
		return
	}
	events, cancel := b.app.Bus().Subscribe(2048)
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				b.subs.publish(toBusEvent(ev))
			}
		}
	}()
}

func toBusEvent(ev bus.Event) BusEvent {
	out := BusEvent{
		Type:            ev.Type,
		Source:          ev.Source,
		SessionID:       ev.SessionID,
		TaskID:          ev.TaskID,
		ToolCallID:      ev.ToolCallID,
		Tool:            ev.Tool,
		Input:           ev.Input,
		Output:          ev.Output,
		Text:            ev.Text,
		Err:             ev.Err,
		Time:            ev.Time,
		SpaceID:         ev.SpaceID,
		ParentMessageID: ev.ParentMessageID,
		AgentID:         ev.AgentID,
		StreamID:        ev.StreamID,
	}
	switch ev.Type {
	case bus.DelegateQueued:
		out.Type = "agent.delegate.started"
		out.ToolCallID = "delegate-" + ev.TaskID
		out.Input = ev.Text
	case bus.DelegateStarted:
		out.Type = "agent.delegate.progress"
		out.ToolCallID = "delegate-" + ev.TaskID
		out.Text = "running"
	case bus.DelegateFinished:
		out.Type = "agent.delegate.finished"
		out.ToolCallID = "delegate-" + ev.TaskID
	case bus.DelegateFailed:
		out.Type = "agent.delegate.failed"
		out.ToolCallID = "delegate-" + ev.TaskID
	case bus.DelegateCanceled:
		out.Type = "agent.delegate.canceled"
		out.ToolCallID = "delegate-" + ev.TaskID
	case bus.ToolCallStarted, bus.ToolCallFinished, bus.ToolCallFailed:
		switch ev.Tool {
		case "mention", "spawn", "spawn_specialist", "invite_agent":
			if ev.Type == bus.ToolCallStarted {
				out.Type = "agent.mention"
			} else if ev.Type == bus.ToolCallFinished {
				out.Type = "agent.mention.reply"
				if isSchedulingAck(ev.Output) {
					out.Output = ""
				}
			} else {
				out.Type = "agent.mention.reply"
				if out.Output == "" {
					out.Output = "(failed: " + ev.Err + ")"
				}
			}
			out.Tool = mentionTarget(ev)
		}
	}
	return out
}

func isSchedulingAck(s string) bool {
	low := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(low, "scheduled ") || strings.Contains(low, "task_id=")
}

func mentionTarget(ev bus.Event) string {
	if ev.Input == "" {
		return ev.Tool
	}
	var args struct {
		Target string `json:"target"`
		Agent  string `json:"agent"`
		Name   string `json:"name"`
		To     string `json:"to"`
	}
	if err := json.Unmarshal([]byte(ev.Input), &args); err != nil {
		return ev.Tool
	}
	for _, v := range []string{args.Target, args.Agent, args.Name, args.To} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ev.Tool
}

// convertMessages, collectEvents, collectEventsWithResults, and
// personaFromSource were the desktop session reader. P3.1–P3.5
// migrated every API to read from space.Manager directly, so these
// helpers no longer have a caller. They have been deleted in P3.6
// per Iris's review: "P3 的 delete list 聚焦 desktop session
// reader". The git history holds the previous shape.

func roleFor(m msg.Message) string {
	switch m.Role {
	case "user":
		return "user"
	case "assistant":
		return "agent"
	case "system":
		return "system"
	}
	return m.Role
}

func isCollabTool(name string) bool {
	switch name {
	case "mention", "spawn", "spawn_specialist", "invite_agent":
		return true
	}
	return false
}

func mentionTargetFromArgs(rawArgs []byte, fallback string) string {
	if len(rawArgs) == 0 {
		return fallback
	}
	var args struct {
		Target  string `json:"target"`
		Agent   string `json:"agent"`
		AgentID string `json:"agent_id"`
		Name    string `json:"name"`
		To      string `json:"to"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return fallback
	}
	for _, v := range []string{args.Target, args.Agent, args.AgentID, args.Name, args.To} {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return fallback
}

func titleCase(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func personaIDs(a *app.App) []string {
	out := make([]string, 0)
	for _, p := range a.Personas().List() {
		out = append(out, p.ID)
	}
	return out
}

func isThreadID(id string) bool {
	return strings.Contains(id, "-") && !strings.HasPrefix(id, defaultChannelID)
}

func workspaceName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "workspace"
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 && i < len(path)-1 {
		return path[i+1:]
	}
	return path
}

func threadTitle(title, summary string, updated time.Time) string {
	t := strings.TrimSpace(title)
	if t != "" && !looksInternal(t) {
		return t
	}
	if s := strings.TrimSpace(summary); s != "" {
		return truncate(s, 60)
	}
	return updated.Format("Jan 2, 15:04")
}

func threadTitleFromSession(title, summary string, messages []MessageView, updated time.Time) string {
	if t := strings.TrimSpace(title); t != "" && !looksInternal(t) {
		return t
	}
	for _, m := range messages {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return truncate(strings.ReplaceAll(strings.TrimSpace(m.Content), "\n", " "), 60)
		}
	}
	if s := strings.TrimSpace(summary); s != "" {
		return truncate(s, 60)
	}
	return updated.Format("Jan 2, 15:04")
}

func looksInternal(t string) bool {
	lower := strings.ToLower(t)
	if lower == "default" || lower == "(untitled)" || strings.HasPrefix(lower, "desktop:") {
		return true
	}
	return false
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func splitModel(s string) (string, string) {
	parts := strings.SplitN(s, " / ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return s, ""
}

func fallback(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}
