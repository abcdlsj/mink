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

func (b *Backend) GetParticipants(channelID, threadID string) ParticipantsView {
	if b.app == nil {
		return mockParticipants(channelID, threadID)
	}
	agents := make([]AgentItem, 0)
	for _, p := range b.app.Personas().List() {
		agents = append(agents, AgentItem{ID: p.ID, Display: p.Display, Role: p.Description, Status: "idle"})
	}
	return ParticipantsView{Agents: agents}
}

func (b *Backend) GetAgentDM(agentID string) SessionDetail {
	if b.app == nil {
		return SessionDetail{}
	}
	source := "desktop:agent:" + agentID
	sessions, err := b.app.ListSessionsBySource(source)
	if err != nil || len(sessions) == 0 {
		return SessionDetail{
			Item: SessionItem{
				ID:        source,
				Title:     "@" + agentID,
				UpdatedAt: time.Now(),
			},
			Messages: []MessageView{},
		}
	}
	latest := sessions[0]
	for _, s := range sessions {
		if s.UpdatedAt.After(latest.UpdatedAt) {
			latest = s
		}
	}
	return SessionDetail{
		Item: SessionItem{
			ID:           source,
			Title:        "@" + agentID,
			UpdatedAt:    latest.UpdatedAt,
			MessageCount: len(latest.Messages),
		},
		Messages: convertMessages(latest),
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
	return BusEvent{
		Type:       ev.Type,
		SessionID:  ev.SessionID,
		ToolCallID: ev.ToolCallID,
		Tool:       ev.Tool,
		Input:      ev.Input,
		Output:     ev.Output,
		Text:       ev.Text,
		Err:        ev.Err,
		Time:       ev.Time,
	}
}

func convertMessages(s *session.Session) []MessageView {
	if s == nil {
		return nil
	}
	out := make([]MessageView, 0, len(s.Messages))
	for _, m := range s.Messages {
		view := MessageView{
			ID:         m.ID,
			Role:       roleFor(m),
			AuthorID:   m.AgentID,
			AuthorName: m.AgentID,
			Content:    m.Content,
			Reasoning:  m.Reasoning,
			Time:       m.Timestamp,
		}
		view.Events = collectEvents(m)
		out = append(out, view)
	}
	return out
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

func collectEvents(m msg.Message) []EventBlock {
	if len(m.ToolCalls) == 0 && len(m.ToolResults) == 0 {
		return nil
	}
	out := make([]EventBlock, 0, len(m.ToolCalls)+len(m.ToolResults))
	results := map[string]msg.ToolResult{}
	for _, r := range m.ToolResults {
		results[r.ToolCallID] = r
	}
	for _, c := range m.ToolCalls {
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
