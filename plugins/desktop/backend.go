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

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
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
	if isThreadID(req.SessionID) {
		if _, err := b.app.SwitchSession(desktopSource, req.SessionID); err != nil {
			return "", err
		}
	} else if strings.HasPrefix(req.SessionID, "desktop:agent:") {
		source = req.SessionID
	}
	if req.PersonaID != "" {
		return b.app.HandleInputAs(ctx, source, req.PersonaID, req.Input)
	}
	return b.app.HandleInput(ctx, source, req.Input)
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

func (b *Backend) ListChannels() []ChannelItem {
	if b.app == nil {
		return mockChannels()
	}
	cfg := b.app.Config()
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

func (b *Backend) ListThreads() []ThreadItem {
	if b.app == nil {
		return mockThreads()
	}
	idx, err := b.app.SessionIndex()
	if err != nil {
		return nil
	}
	out := make([]ThreadItem, 0, len(idx))
	for _, m := range idx {
		if !strings.HasPrefix(m.Source, desktopSource) {
			continue
		}
		out = append(out, ThreadItem{
			ID:         m.ID,
			ChannelID:  defaultChannelID,
			Title:      threadTitle(m.Title, m.Summary, m.UpdatedAt),
			UpdatedAt:  m.UpdatedAt,
			EventCount: m.Messages,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > 12 {
		out = out[:12]
	}
	return out
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

func (b *Backend) GetChannel(id string) SessionDetail {
	if b.app == nil {
		return mockChannelDetail(id)
	}
	name := workspaceName(b.app.Config().Workspace)
	return SessionDetail{
		Item: SessionItem{
			ID:        id,
			Title:     "#" + name,
			UpdatedAt: time.Now(),
		},
		Summary:  "",
		Messages: []MessageView{},
	}
}

func (b *Backend) GetThread(id string) SessionDetail {
	if b.app == nil {
		return mockThreadDetail(id)
	}
	sessions, err := b.app.ListSessions()
	if err != nil {
		return SessionDetail{}
	}
	for _, s := range sessions {
		if s.ID != id {
			continue
		}
		messages := convertMessages(s)
		messages = b.attachDelegateOutcomes(messages)
		return SessionDetail{
			Item: SessionItem{
				ID:           s.ID,
				Title:        threadTitleFromSession(s.Title, s.Summary, messages, s.UpdatedAt),
				UpdatedAt:    s.UpdatedAt,
				MessageCount: len(s.Messages),
			},
			Summary:  s.Summary,
			Messages: messages,
		}
	}
	return SessionDetail{}
}

// attachDelegateOutcomes scans every assistant message for mention tool
// calls whose result contains a "task_id=<id>" scheduling ack. For each
// task id it replays the collab task's bus history and appends a
// synthetic "delegate" event block under the same message so the
// persisted view shows the same called/delegated/completed semantics
// the live stream draws.
func (b *Backend) attachDelegateOutcomes(messages []MessageView) []MessageView {
	if b.app == nil {
		return messages
	}
	for i, m := range messages {
		if len(m.Events) == 0 {
			continue
		}
		extra := make([]EventBlock, 0)
		for _, ev := range m.Events {
			if ev.Kind != "mention" {
				continue
			}
			taskID := ev.TaskID
			if taskID == "" {
				taskID = parseTaskID(ev.Output)
			}
			if taskID == "" && ev.Reply != "" {
				taskID = parseTaskID(ev.Reply)
			}
			if taskID == "" {
				continue
			}
			block := b.replayDelegateTask(taskID, ev.AgentID, ev.AgentDisplay, ev.Args)
			if block != nil {
				extra = append(extra, *block)
			}
		}
		if len(extra) > 0 {
			messages[i].Events = append(messages[i].Events, extra...)
		}
	}
	return messages
}

func (b *Backend) replayDelegateTask(taskID, agentID, agentDisplay, mentionArgs string) *EventBlock {
	evs, err := b.app.ReplayTask(taskID, 64)
	if err != nil || len(evs) == 0 {
		return nil
	}
	block := EventBlock{
		Kind:         "delegate",
		Status:       "running",
		AgentID:      agentID,
		AgentDisplay: agentDisplay,
		TaskID:       taskID,
		Task:         questionFromArgs(mentionArgs),
		Time:         evs[0].Time,
	}
	var queuedAt, finishedAt time.Time
	for _, e := range evs {
		switch e.Type {
		case bus.DelegateQueued:
			block.Status = "pending"
			queuedAt = e.Time
			if block.Task == "" {
				block.Task = e.Text
			}
		case bus.DelegateStarted:
			block.Status = "running"
			if queuedAt.IsZero() {
				queuedAt = e.Time
			}
		case bus.DelegateFinished:
			block.Status = "done"
			block.Output = e.Output
			finishedAt = e.Time
			if !e.Time.IsZero() && !block.Time.IsZero() {
				block.DurationMs = e.Time.Sub(block.Time).Milliseconds()
			}
		case bus.DelegateFailed, bus.DelegateCanceled:
			block.Status = "error"
			block.Err = e.Err
			finishedAt = e.Time
		}
	}
	if queuedAt.IsZero() {
		queuedAt = block.Time
	}
	block.Steps = b.subtaskSteps(taskID, block.Status, queuedAt, finishedAt)
	return &block
}

// subtaskSteps walks the sub-session that ran the delegate task and
// returns a short, human-readable summary of the agent's actions for
// the first-level expand. We deliberately strip raw tool output and
// keep verb + target so the rail reads like a journal instead of a
// console replay.
//
// outerStatus + window bound the result so an old session that
// happens to share the source string can't bleed events into the
// current task. Successful tasks suppress exploratory tool errors;
// failed tasks keep the error step that ended the run.
func (b *Backend) subtaskSteps(taskID, outerStatus string, queuedAt, finishedAt time.Time) []DelegateStep {
	source := "subtask:" + taskID
	sessions, err := b.app.ListSessionsBySource(source)
	if err != nil || len(sessions) == 0 {
		return nil
	}
	sess := sessions[0]
	results := map[string]msg.ToolResult{}
	for _, m := range sess.Messages {
		for _, r := range m.ToolResults {
			results[r.ToolCallID] = r
		}
	}
	workspace := strings.TrimRight(b.app.Config().Workspace, "/")
	steps := make([]DelegateStep, 0, 8)
	for _, m := range sess.Messages {
		if m.Role == "tool" {
			continue
		}
		if !timeInWindow(m.Timestamp, queuedAt, finishedAt) {
			continue
		}
		for _, c := range m.ToolCalls {
			step := DelegateStep{
				Tool:   c.Name,
				Status: "done",
				Time:   m.Timestamp,
			}
			if r, ok := results[c.ID]; ok {
				if r.Error != "" {
					step.Status = "error"
					step.Err = oneLine(r.Error)
				}
			}
			step.Output = humanizeStep(c.Name, string(c.Args), workspace)
			steps = append(steps, step)
		}
	}
	if outerStatus == "done" {
		steps = dropExploratoryErrors(steps)
	}
	if len(steps) > 5 {
		head := steps[:2]
		tail := steps[len(steps)-3:]
		steps = append(append([]DelegateStep{}, head...), tail...)
	}
	return steps
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
	if threadID != "" {
		return b.threadParticipants(threadID)
	}
	// Channel view: synthesize from team aliases under the desktop source.
	return ParticipantsView{Agents: b.allAgents()}
}

func (b *Backend) threadParticipants(threadID string) ParticipantsView {
	sessions, err := b.app.ListSessions()
	if err != nil {
		return ParticipantsView{}
	}
	var sess *session.Session
	for _, s := range sessions {
		if s.ID == threadID {
			sess = s
			break
		}
	}
	if sess == nil {
		return ParticipantsView{}
	}

	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
	}

	if def := personaFromSource(sess.Source); def != "" {
		add(def)
	}

	resultsByID := map[string]msg.ToolResult{}
	for _, m := range sess.Messages {
		for _, r := range m.ToolResults {
			resultsByID[r.ToolCallID] = r
		}
	}

	recent := make([]AgentRun, 0)
	for _, m := range sess.Messages {
		if m.AgentID != "" {
			add(m.AgentID)
		}
		for _, c := range m.ToolCalls {
			if !isCollabTool(c.Name) {
				continue
			}
			target := mentionTargetFromArgs(c.Args, c.Name)
			add(target)
			if r, ok := resultsByID[c.ID]; ok && isSchedulingAck(r.Content) {
				if taskID := parseTaskID(r.Content); taskID != "" {
					if run := b.taskAsRun(taskID, target); run != nil {
						recent = append(recent, *run)
					}
				}
			}
		}
	}

	personas := b.app.Personas().List()
	agents := make([]AgentItem, 0, len(seen))
	for id := range seen {
		display := id
		role := ""
		for _, p := range personas {
			if p.ID == id {
				display = p.Display
				role = p.Description
				break
			}
		}
		agents = append(agents, AgentItem{ID: id, Display: display, Role: role, Status: "idle"})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })

	if len(recent) > 6 {
		recent = recent[len(recent)-6:]
	}

	return ParticipantsView{
		Agents:     agents,
		RecentRuns: recent,
	}
}

func (b *Backend) allAgents() []AgentItem {
	out := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		out = append(out, AgentItem{ID: p.ID, Display: p.Display, Role: p.Description, Status: "idle"})
	}
	return out
}

func (b *Backend) taskAsRun(taskID, agentID string) *AgentRun {
	evs, err := b.app.ReplayTask(taskID, 32)
	if err != nil || len(evs) == 0 {
		return nil
	}
	run := &AgentRun{
		ID:      taskID,
		AgentID: agentID,
		Status:  "running",
	}
	var firstTime, lastTime time.Time
	for _, e := range evs {
		if firstTime.IsZero() || e.Time.Before(firstTime) {
			firstTime = e.Time
		}
		if lastTime.IsZero() || e.Time.After(lastTime) {
			lastTime = e.Time
		}
		switch e.Type {
		case bus.DelegateQueued:
			run.Status = "pending"
			if run.Title == "" {
				run.Title = e.Text
			}
		case bus.DelegateStarted:
			run.Status = "running"
		case bus.DelegateFinished:
			run.Status = "done"
		case bus.DelegateFailed, bus.DelegateCanceled:
			run.Status = "error"
		}
	}
	if run.Title == "" {
		run.Title = "delegated"
	}
	if !firstTime.IsZero() {
		run.Time = firstTime
	}
	if !lastTime.IsZero() && !firstTime.IsZero() {
		run.DurationMs = lastTime.Sub(firstTime).Milliseconds()
	}
	return run
}

func (b *Backend) GetAgentDM(agentID string) SessionDetail {
	if b.app == nil {
		return SessionDetail{}
	}
	prefix := "desktop:agent:" + agentID
	all, err := b.app.ListSessions()
	if err != nil {
		return SessionDetail{}
	}
	var latest *session.Session
	for _, s := range all {
		if !strings.HasPrefix(s.Source, prefix) {
			continue
		}
		if latest == nil || s.UpdatedAt.After(latest.UpdatedAt) {
			latest = s
		}
	}
	if latest == nil {
		return SessionDetail{
			Item: SessionItem{
				ID:        prefix,
				Title:     "@" + agentID,
				UpdatedAt: time.Now(),
			},
			Messages: []MessageView{},
		}
	}
	messages := b.attachDelegateOutcomes(convertMessages(latest))
	return SessionDetail{
		Item: SessionItem{
			ID:           prefix,
			Title:        "@" + agentID,
			UpdatedAt:    latest.UpdatedAt,
			MessageCount: len(latest.Messages),
		},
		Messages: messages,
	}
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
		Type:       ev.Type,
		Source:     ev.Source,
		SessionID:  ev.SessionID,
		TaskID:     ev.TaskID,
		ToolCallID: ev.ToolCallID,
		Tool:       ev.Tool,
		Input:      ev.Input,
		Output:     ev.Output,
		Text:       ev.Text,
		Err:        ev.Err,
		Time:       ev.Time,
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
	case bus.DelegateFailed, bus.DelegateCanceled:
		out.Type = "agent.delegate.failed"
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

func convertMessages(s *session.Session) []MessageView {
	if s == nil {
		return nil
	}
	defaultAgent := personaFromSource(s.Source)
	resultsByID := map[string]msg.ToolResult{}
	for _, m := range s.Messages {
		for _, r := range m.ToolResults {
			resultsByID[r.ToolCallID] = r
		}
	}
	out := make([]MessageView, 0, len(s.Messages))
	for _, m := range s.Messages {
		if m.Role == "tool" {
			continue
		}
		authorID := m.AgentID
		if authorID == "" && roleFor(m) != "user" && defaultAgent != "" {
			authorID = defaultAgent
		}
		view := MessageView{
			ID:         m.ID,
			Role:       roleFor(m),
			AuthorID:   authorID,
			AuthorName: authorID,
			Content:    m.Content,
			Reasoning:  m.Reasoning,
			Time:       m.Timestamp,
		}
		view.Events = collectEventsWithResults(m, resultsByID)
		out = append(out, view)
	}
	return out
}

func collectEvents(m msg.Message) []EventBlock {
	results := map[string]msg.ToolResult{}
	for _, r := range m.ToolResults {
		results[r.ToolCallID] = r
	}
	return collectEventsWithResults(m, results)
}

func collectEventsWithResults(m msg.Message, results map[string]msg.ToolResult) []EventBlock {
	if len(m.ToolCalls) == 0 {
		return nil
	}
	out := make([]EventBlock, 0, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		if isCollabTool(c.Name) {
			ev := EventBlock{
				Kind:         "mention",
				ToolName:     c.Name,
				Args:         string(c.Args),
				Status:       "running",
				Time:         m.Timestamp,
				AgentID:      mentionTargetFromArgs(c.Args, c.Name),
				AgentDisplay: titleCase(mentionTargetFromArgs(c.Args, c.Name)),
			}
			if r, ok := results[c.ID]; ok {
				ev.Status = "done"
				if r.Error != "" {
					ev.Status = "error"
					ev.Err = r.Error
				}
				if isSchedulingAck(r.Content) {
					ev.TaskID = parseTaskID(r.Content)
				} else {
					ev.Reply = r.Content
				}
			}
			out = append(out, ev)
			continue
		}
		ev := EventBlock{
			Kind:     "tool_call",
			ToolName: c.Name,
			Args:     string(c.Args),
			Status:   "done",
			Time:     m.Timestamp,
		}
		if r, ok := results[c.ID]; ok {
			ev.Output = r.Content
			if r.Error != "" {
				ev.Err = r.Error
				ev.Status = "error"
			}
		}
		out = append(out, ev)
	}
	return out
}

// personaFromSource extracts the agent id from a session source string.
// Examples:
//   "desktop:agent:coder"            -> "coder"
//   "desktop:agent:coder:persona:coder" -> "coder"
//   "desktop:persona:reviewer"       -> "reviewer"
//   "desktop"                        -> ""
func personaFromSource(src string) string {
	const personaTag = ":persona:"
	if i := strings.LastIndex(src, personaTag); i >= 0 {
		rest := src[i+len(personaTag):]
		if j := strings.IndexByte(rest, ':'); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	const agentTag = "desktop:agent:"
	if strings.HasPrefix(src, agentTag) {
		rest := src[len(agentTag):]
		if j := strings.IndexByte(rest, ':'); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	return ""
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
