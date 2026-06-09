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
const defaultSumiDirectTitle = "Sumi"

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

func (b *Backend) SendMessage(req SendRequest) (string, error) {
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
	var parentMessageID string
	messageCountBefore := -1
	if loaded, err := b.app.Spaces().LoadSpace(req.SessionID); err == nil && loaded != nil {
		sp = loaded
		messageCountBefore = len(sp.Messages)
		switch sp.Kind {
		case space.KindChannel:
			source = "desktop:channel:" + sp.ID
		case space.KindDirectChat:
			if isDefaultSumiDirect(sp) {
				source = desktopSource
			} else {
				source = "desktop:direct:" + sp.ID
			}
		case space.KindAgentDM:
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
		parentMessageID = normalized
		ctx = command.WithParentMessage(ctx, normalized)
	}
	var out string
	var err error
	if req.PersonaID != "" {
		out, err = b.app.HandleInputAs(ctx, source, req.PersonaID, req.Input)
	} else {
		out, err = b.app.HandleInput(ctx, source, req.Input)
	}
	if err != nil {
		b.persistSendFailure(sp, parentMessageID, req.Input, messageCountBefore, err)
		return "", err
	}
	return out, nil
}

func (b *Backend) persistSendFailure(sp *space.Space, parentMessageID, input string, messageCountBefore int, sendErr error) {
	if sp == nil || sendErr == nil {
		return
	}
	if messageCountBefore >= 0 {
		if latest, err := b.app.Spaces().LoadSpace(sp.ID); err == nil && latest != nil && len(latest.Messages) <= messageCountBefore {
			_, _, _ = b.app.Spaces().AppendMessageWithRouting(sp.ID, space.Message{
				AuthorID:        b.app.Spaces().UserParticipant().ID,
				AuthorKind:      space.ParticipantUser,
				Content:         input,
				ParentMessageID: strings.TrimSpace(parentMessageID),
			}, nil, nil)
		}
	}
	_, _, _ = b.app.Spaces().AppendMessageWithRouting(sp.ID, space.Message{
		AuthorID:        "sumi",
		AuthorKind:      space.ParticipantSystem,
		Content:         "Send failed: " + sendErr.Error(),
		ParentMessageID: strings.TrimSpace(parentMessageID),
	}, nil, nil)
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

func (b *Backend) NewDirectChat() (SessionDetail, error) {
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

func (b *Backend) ListDirectChats() []DirectChatItem {
	if _, err := b.ensureDefaultSumiDirect(); err != nil {
		return []DirectChatItem{}
	}
	spaces, err := b.app.Spaces().ListSpaces()
	if err != nil {
		return []DirectChatItem{}
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
	sort.Slice(all, func(i, j int) bool { return all[i].sp.UpdatedAt.After(all[j].sp.UpdatedAt) })

	out := make([]DirectChatItem, 0, len(all))
	keptEmpty := false
	for _, e := range all {
		if e.empty && !isDefaultSumiDirect(e.sp) {
			if keptEmpty {
				continue
			}
			keptEmpty = true
		}
		out = append(out, DirectChatItem{
			ID:        e.sp.ID,
			Kind:      "direct_chat",
			Title:     directChatTitle(e.sp),
			Agents:    directChatAgentIDs(e.sp),
			UpdatedAt: e.sp.UpdatedAt,
		})
	}
	out = append(out, b.defaultAgentDMItems(spaces)...)
	sort.Slice(out, func(i, j int) bool {
		if isDefaultSumiDirectItem(out[i]) != isDefaultSumiDirectItem(out[j]) {
			return isDefaultSumiDirectItem(out[i])
		}
		if out[i].UpdatedAt.IsZero() != out[j].UpdatedAt.IsZero() {
			return !out[i].UpdatedAt.IsZero()
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (b *Backend) ensureDefaultSumiDirect() (*space.Space, error) {
	spaces, err := b.app.Spaces().ListSpaces()
	if err == nil {
		for _, sp := range spaces {
			if isDefaultSumiDirect(sp) {
				return sp, nil
			}
		}
	}
	return b.app.Spaces().EnsureSpace(space.KindDirectChat, defaultSumiDirectTitle, space.PersonaInfo{})
}

func isDefaultSumiDirect(sp *space.Space) bool {
	return sp != nil &&
		sp.Kind == space.KindDirectChat &&
		strings.EqualFold(strings.TrimSpace(sp.Title), defaultSumiDirectTitle)
}

func isDefaultSumiDirectItem(item DirectChatItem) bool {
	return item.Kind == "direct_chat" && strings.EqualFold(strings.TrimSpace(item.Title), defaultSumiDirectTitle)
}

func directChatAgentIDs(sp *space.Space) []string {
	if isDefaultSumiDirect(sp) {
		return []string{}
	}
	return spaceAgentIDs(sp)
}

func (b *Backend) defaultAgentDMItems(spaces []*space.Space) []DirectChatItem {
	out := make([]DirectChatItem, 0)
	for _, sp := range spaces {
		if sp.Kind != space.KindAgentDM || !isDefaultAgentDM(sp) {
			continue
		}
		pid := space.AgentParticipantID(sp)
		if pid == "" {
			continue
		}
		display := pid
		if p := b.app.Personas().Get(pid); p != nil {
			display = p.Display
		}
		item := DirectChatItem{
			ID:          sp.ID,
			Kind:        "agent_dm",
			PersonaID:   pid,
			PersonaName: display,
			Title:       "@" + fallback(display, pid),
			Agents:      []string{pid},
			UpdatedAt:   sp.UpdatedAt,
		}
		out = append(out, item)
	}
	return out
}

func (b *Backend) GetDirectChat(id string) SessionDetail {
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

func directChatTitle(sp *space.Space) string {
	if sp == nil {
		return "New chat"
	}
	if isDefaultSumiDirect(sp) {
		return defaultSumiDirectTitle
	}
	for _, m := range sp.Messages {
		if m.AuthorKind == space.ParticipantUser {
			if t := strings.TrimSpace(m.Content); t != "" {
				return preview(t, 48)
			}
		}
	}
	if t := strings.TrimSpace(sp.Title); t != "" && !strings.HasPrefix(t, "dchat-") {
		return t
	}
	return "New chat"
}

func newDirectChatSeed() string {
	return "dchat-" + time.Now().Format("20060102-150405") + "-" + uuid.NewString()[:4]
}

func (b *Backend) ListChannels() []ChannelItem {
	cfg := b.app.Config()
	spaces, err := b.app.Spaces().Store().ListSpaces()
	if err == nil {
		out := make([]ChannelItem, 0, 1)
		for _, sp := range spaces {
			if sp.Kind != space.KindChannel {
				continue
			}
			out = append(out, ChannelItem{
				ID:         sp.ID,
				Name:       channelDisplayName(sp, cfg.Workspace),
				Topic:      "",
				Agents:     spaceAgentIDs(sp),
				AgentModes: sp.AgentModes,
				UpdatedAt:  sp.UpdatedAt,
			})
		}
		if len(out) > 0 {
			sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
			return out
		}
	}
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

func (b *Backend) ListRecent() []RecentItem {
	spaces, err := b.app.Spaces().ListSpaces()
	if err != nil {
		return []RecentItem{}
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
			pid := space.AgentParticipantID(sp)
			display := pid
			if p := b.app.Personas().Get(pid); p != nil {
				display = p.Display
			}
			title := visibleAgentDMTitle(sp, pid)
			if title == "New chat" || isDefaultAgentDM(sp) {
				title = "@" + fallback(display, pid)
			}
			item = RecentItem{
				ID:        sp.ID,
				Kind:      "agent_dm",
				Title:     title,
				Subtitle:  recentSubtitle(sp),
				UpdatedAt: sp.UpdatedAt,
			}
		default:
			continue
		}
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
func channelDisplayName(sp *space.Space, workspace string) string {
	if sp == nil {
		return ""
	}
	if strings.TrimSpace(sp.Title) == "" || sp.Title == "default" {
		return workspaceName(workspace)
	}
	return sp.Title
}

func spaceAgentIDs(sp *space.Space) []string {
	if sp == nil {
		return []string{}
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

func (b *Backend) ListThreads() []ThreadItem {
	return []ThreadItem{}
}

func (b *Backend) ListAgents() []AgentItem {
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, personaAgentItem(p, "idle"))
	}
	return out
}

func (b *Backend) CreateChannel(name string) (ChannelItem, error) {
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
		ID:         sp.ID,
		Name:       channelDisplayName(sp, b.app.Config().Workspace),
		Topic:      "",
		Agents:     spaceAgentIDs(sp),
		AgentModes: sp.AgentModes,
		UpdatedAt:  sp.UpdatedAt,
	}, nil
}

func (b *Backend) SetChannelAgentMode(channelID, personaID, mode string) error {
	return b.app.Spaces().SetAgentMode(channelID, personaID, mode)
}

func (b *Backend) AddAgentToChannel(channelID, personaID string) error {
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return fmt.Errorf("persona id required")
	}
	p := b.app.Personas().Get(personaID)
	if p == nil {
		return fmt.Errorf("persona not registered: %s", personaID)
	}
	return b.app.Spaces().AddAgentParticipant(channelID, space.PersonaInfo{
		ID:      p.ID,
		Display: p.Display,
		Role:    p.Description,
	})
}

func (b *Backend) SetThreadAgentMode(spaceID, parentMessageID, personaID, mode string) error {
	return b.app.Spaces().SetThreadAgentMode(spaceID, parentMessageID, personaID, mode)
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
	cfg := b.app.Config()
	var sp *space.Space
	if space.IsSpaceID(id) {
		if loaded, err := b.app.Spaces().LoadSpace(id); err == nil && loaded != nil && loaded.Kind == space.KindChannel {
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
		return []MessageView{}
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
		view := baseMessageView(sp, m, resolver)
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

func baseMessageView(sp *space.Space, m space.Message, resolver space.DisplayResolver) MessageView {
	view := MessageView{
		ID:              m.ID,
		Role:            roleForKind(m.AuthorKind),
		AuthorID:        m.AuthorID,
		AuthorName:      space.MessageAuthorDisplay(sp, m, resolver),
		Content:         m.Content,
		Reasoning:       m.Reasoning,
		Time:            m.CreatedAt,
		ThreadID:        m.ParentMessageID,
		AutoReplyReason: m.AutoReplyReason,
		RuntimeMeta:     copyStringMap(m.RuntimeMeta),
	}
	if m.Usage != nil {
		view.Usage = &TokenUsage{
			Input:   m.Usage.Input,
			Output:  m.Usage.Output,
			Total:   m.Usage.Total,
			CostUSD: m.Usage.CostUSD,
			Model:   m.Usage.Model,
			Source:  m.Usage.Source,
		}
	}
	return view
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
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

func roleForKind(k space.ParticipantKind) string {
	switch k {
	case space.ParticipantUser:
		return "user"
	case space.ParticipantAgent:
		return "agent"
	case space.ParticipantSystem:
		return "system"
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

type appAccessor interface {
	Personas() *persona.Registry
	Tasks() *taskpkg.Manager
}

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
	spaceID := strings.TrimSpace(threadID)
	if spaceID == "" {
		spaceID = strings.TrimSpace(channelID)
	}
	if spaceID == "" {
		return ParticipantsView{Agents: b.allAgents()}
	}
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil || sp == nil {
		return ParticipantsView{Agents: []AgentItem{}}
	}
	return ParticipantsView{
		Agents:     spaceParticipantsAsAgents(sp, b.app),
		RecentRuns: b.spaceRecentRuns(sp),
	}
}

func spaceParticipantsAsAgents(sp *space.Space, a appAccessor) []AgentItem {
	if sp == nil {
		return []AgentItem{}
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
			Runtime: personaRuntime(p.ID, a),
			Model:   personaModel(p.ID, a),
			Status:  "idle",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Backend) spaceRecentRuns(sp *space.Space) []AgentRun {
	if sp == nil || b.app.Tasks() == nil {
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
	TaskID           string        `json:"task_id"`
	SpaceID          string        `json:"space_id"`
	WorkerID         string        `json:"worker_id"`
	WorkerName       string        `json:"worker_name,omitempty"`
	Title            string        `json:"title"`
	Status           string        `json:"status"`
	Outcome          string        `json:"outcome,omitempty"`
	ResultMessageID  string        `json:"result_message_id,omitempty"`
	TriggerMessageID string        `json:"trigger_message_id,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
	KeySteps         []RunStep     `json:"key_steps,omitempty"`
	State            TaskStateView `json:"state,omitempty"`
}

func (b *Backend) GetRunDetail(taskID string) RunDetail {
	if b.app.Tasks() == nil {
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
		State:            taskStateView(tk.State),
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
	if detail.State.Goal == "" && detail.State.Checkpoint == "" && len(detail.State.Todo) == 0 {
		detail.State = taskStateView(latest.State)
	}
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

func personaAgentItem(p *persona.Persona, status string) AgentItem {
	if p == nil {
		return AgentItem{Status: status}
	}
	return AgentItem{
		ID:      p.ID,
		Display: p.Display,
		Role:    p.Description,
		Runtime: p.Runtime,
		Model:   p.Model,
		Status:  status,
	}
}

func personaRuntime(id string, a appAccessor) string {
	if a == nil || strings.TrimSpace(id) == "" {
		return ""
	}
	if p := a.Personas().Get(id); p != nil {
		return p.Runtime
	}
	return ""
}

func personaModel(id string, a appAccessor) string {
	if a == nil || strings.TrimSpace(id) == "" {
		return ""
	}
	if p := a.Personas().Get(id); p != nil {
		return p.Model
	}
	return ""
}

func (b *Backend) allAgents() []AgentItem {
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, personaAgentItem(p, "idle"))
	}
	return out
}

func (b *Backend) GetAgentDM(agentID string) SessionDetail {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return SessionDetail{}
	}
	var sp *space.Space
	if space.IsSpaceID(agentID) {
		if loaded, err := b.app.Spaces().LoadSpace(agentID); err == nil && loaded != nil && loaded.Kind == space.KindAgentDM {
			sp = loaded
		}
	}
	display := agentID
	role := ""
	if sp != nil {
		if pid := space.AgentParticipantID(sp); pid != "" {
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
	pid := space.AgentParticipantID(sp)
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
			PersonaID:    pid,
			PersonaName:  display,
			UpdatedAt:    sp.UpdatedAt,
			MessageCount: len(sp.Messages),
		},
		Messages: spaceMessagesToView(sp, b.app),
	}
}

func (b *Backend) CreateAgentDM(personaID, title string) (AgentDMItem, error) {
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
	if title = strings.TrimSpace(title); title != "" {
		if err := b.app.Spaces().UpdateTitle(sp.ID, title); err != nil {
			return AgentDMItem{}, err
		}
		if updated, err := b.app.Spaces().LoadSpace(sp.ID); err == nil && updated != nil {
			sp = updated
		}
	}
	return agentDMItemFromSpace(sp, b.app), nil
}

func (b *Backend) UpdateAgentDMTitle(spaceID, title string) (AgentDMItem, error) {
	spaceID = strings.TrimSpace(spaceID)
	title = strings.TrimSpace(title)
	if spaceID == "" || title == "" {
		return AgentDMItem{}, fmt.Errorf("space id and title required")
	}
	sp, err := b.app.Spaces().LoadSpace(spaceID)
	if err != nil {
		return AgentDMItem{}, err
	}
	if sp == nil || sp.Kind != space.KindAgentDM {
		return AgentDMItem{}, fmt.Errorf("agent chat not found: %s", spaceID)
	}
	if isDefaultAgentDM(sp) {
		return AgentDMItem{}, fmt.Errorf("default agent dm title is fixed")
	}
	if err := b.app.Spaces().UpdateTitle(sp.ID, title); err != nil {
		return AgentDMItem{}, err
	}
	updated, err := b.app.Spaces().LoadSpace(sp.ID)
	if err != nil {
		return AgentDMItem{}, err
	}
	return agentDMItemFromSpace(updated, b.app), nil
}

func (b *Backend) ListAgentDMs() []AgentDMItem {
	all, err := b.app.Spaces().Store().ListSpaces()
	if err != nil {
		return []AgentDMItem{}
	}
	out := make([]AgentDMItem, 0, len(all))
	for _, sp := range all {
		if sp.Kind != space.KindAgentDM {
			continue
		}
		if isDefaultAgentDM(sp) {
			continue
		}
		out = append(out, agentDMItemFromSpace(sp, b.app))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func agentDMItemFromSpace(sp *space.Space, a appAccessor) AgentDMItem {
	pid := space.AgentParticipantID(sp)
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
	if t == strings.TrimSpace(personaID) {
		return "New chat"
	}
	if t == "" || isAgentDMMachineSeed(t, personaID) {
		return "New chat"
	}
	return t
}

func isDefaultAgentDM(sp *space.Space) bool {
	if sp == nil || sp.Kind != space.KindAgentDM {
		return false
	}
	pid := space.AgentParticipantID(sp)
	return pid != "" && strings.TrimSpace(sp.Title) == pid
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

func (b *Backend) ListPersonas() []PersonaItem {
	out := make([]PersonaItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, PersonaItem{
			ID:            p.ID,
			Display:       p.Display,
			Runtime:       p.Runtime,
			Model:         p.Model,
			Description:   p.Description,
			Tools:         p.Tools,
			ShowInSidebar: p.ShowInSidebar,
		})
	}
	return out
}

func (b *Backend) ListModels() []ModelItem {
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
	tools := b.app.Tools()
	out := make([]ToolItem, 0, len(tools))
	for _, t := range tools {
		out = append(out, ToolItem{
			Name:        t.Name(),
			Description: t.Desc(),
			Enabled:     true,
		})
	}
	return out
}

func (b *Backend) ListCommands() []CommandItem {
	cmds := b.app.Commands()
	out := make([]CommandItem, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, CommandItem{Name: "/" + c.Name(), Summary: c.Desc()})
	}
	return out
}

func (b *Backend) Capabilities() CapabilityView {
	return CapabilityView{
		Skills:          skillViews(b.app.SkillDirectory()),
		Tasks:           taskStateCards(b.app.RecentTaskStates(6)),
		ActionProposals: actionProposalCards(b.app.RecentActionProposals(6)),
	}
}

func (b *Backend) ListSkills() []SkillView {
	return skillViews(b.app.SkillDirectory())
}

func (b *Backend) GetSkill(name string) SkillView {
	item, ok := b.app.SkillDetail(name)
	if !ok {
		return SkillView{}
	}
	return skillView(item)
}

func skillViews(in []app.SkillDirectoryItem) []SkillView {
	out := make([]SkillView, 0, len(in))
	for _, s := range in {
		out = append(out, skillView(s))
	}
	return out
}

func skillView(s app.SkillDirectoryItem) SkillView {
	return SkillView{
		Name:          s.Name,
		Description:   s.Description,
		When:          s.When,
		Risk:          s.Risk,
		Env:           s.Env,
		EnvNeeds:      skillEnvNeedViews(s.EnvNeeds),
		Entrypoints:   s.Entrypoints,
		Examples:      s.Examples,
		Path:          s.Path,
		Configured:    s.Configured,
		MissingEnv:    s.MissingEnv,
		LastAction:    s.LastAction,
		LastListed:    s.LastListed,
		LastDescribed: s.LastDescribed,
		LastUsed:      s.LastUsed,
		Body:          s.Body,
	}
}

func skillEnvNeedViews(in []app.SkillEnvNeed) []SkillEnvNeed {
	out := make([]SkillEnvNeed, 0, len(in))
	for _, need := range in {
		out = append(out, SkillEnvNeed{
			Name:       need.Name,
			Configured: need.Configured,
			Hint:       need.Hint,
		})
	}
	return out
}

func taskStateCards(in []app.TaskStateSummary) []TaskStateCard {
	out := make([]TaskStateCard, 0, len(in))
	for _, t := range in {
		out = append(out, TaskStateCard{
			ID:         t.ID,
			Title:      t.Title,
			Status:     t.Status,
			WorkerID:   t.WorkerID,
			SpaceID:    t.SpaceID,
			Source:     t.Source,
			UpdatedAt:  t.UpdatedAt,
			Outcome:    t.Outcome,
			State:      taskStateView(t.State),
			LatestRun:  t.LatestRun,
			RunStatus:  t.RunStatus,
			RunStarted: t.RunStarted,
		})
	}
	return out
}

func actionProposalCards(in []app.ActionProposalSummary) []ActionProposalCard {
	out := make([]ActionProposalCard, 0, len(in))
	for _, p := range in {
		out = append(out, ActionProposalCard{
			Time:      p.Time,
			Source:    p.Source,
			Tool:      p.Tool,
			Result:    p.Result,
			Intent:    p.Proposal.Intent,
			Target:    p.Proposal.Target,
			Risk:      p.Proposal.Risk,
			Preview:   p.Proposal.Preview,
			Rollback:  p.Proposal.Rollback,
			ExpiresAt: p.Proposal.ExpiresAt,
		})
	}
	return out
}

func taskStateView(s taskpkg.TaskState) TaskStateView {
	return TaskStateView{
		Goal:       s.Goal,
		Todo:       s.Todo,
		Checkpoint: s.Checkpoint,
		Artifacts:  s.Artifacts,
		Blockers:   s.Blockers,
		RelatedIDs: s.RelatedIDs,
	}
}

func (b *Backend) Subscribe() (<-chan BusEvent, func()) {
	return b.subs.subscribe(256)
}

func (b *Backend) APIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", jsonHandler(func() any { return b.WorkspaceInfo() }))
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
	mux.HandleFunc("/api/channel/agent-mode", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ChannelID string `json:"channel_id"`
			PersonaID string `json:"persona_id"`
			Mode      string `json:"mode"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.SetChannelAgentMode(in.ChannelID, in.PersonaID, in.Mode); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/channel/add-agent", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ChannelID string `json:"channel_id"`
			PersonaID string `json:"persona_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.AddAgentToChannel(in.ChannelID, in.PersonaID); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/thread/agent-mode", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			SpaceID         string `json:"space_id"`
			ParentMessageID string `json:"parent_message_id"`
			PersonaID       string `json:"persona_id"`
			Mode            string `json:"mode"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		if err := b.SetThreadAgentMode(in.SpaceID, in.ParentMessageID, in.PersonaID, in.Mode); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, map[string]string{"ok": "true"})
	})
	mux.HandleFunc("/api/threads", jsonHandler(func() any { return b.ListThreads() }))
	mux.HandleFunc("/api/agents", jsonHandler(func() any { return b.ListAgents() }))
	mux.HandleFunc("/api/channel", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetChannel(req.URL.Query().Get("id")))
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
			Title     string `json:"title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.CreateAgentDM(in.PersonaID, in.Title)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(rw, item)
	})
	mux.HandleFunc("/api/agent-dm/title", func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		item, err := b.UpdateAgentDMTitle(in.ID, in.Title)
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
	mux.HandleFunc("/api/capabilities", jsonHandler(func() any { return b.Capabilities() }))
	mux.HandleFunc("/api/skills", jsonHandler(func() any { return b.ListSkills() }))
	mux.HandleFunc("/api/skill", func(rw http.ResponseWriter, req *http.Request) {
		writeJSON(rw, b.GetSkill(req.URL.Query().Get("name")))
	})
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
		RunID:           ev.RunID,
		MessageID:       ev.MessageID,
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
