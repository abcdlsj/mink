package desktop

import (
	"context"
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
	if isThreadID(req.SessionID) {
		if _, err := b.app.SwitchSession(desktopSource, req.SessionID); err != nil {
			return "", err
		}
	}
	if req.PersonaID != "" {
		return b.app.HandleInputAs(ctx, desktopSource, req.PersonaID, req.Input)
	}
	return b.app.HandleInput(ctx, desktopSource, req.Input)
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
			Name:      "default",
			Topic:     cfg.Workspace,
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
			Title:      fallback(m.Title, "(untitled)"),
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
	return SessionDetail{
		Item: SessionItem{
			ID:        id,
			Title:     "#default",
			UpdatedAt: time.Now(),
		},
		Summary:  b.app.Config().Workspace,
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
		return SessionDetail{
			Item: SessionItem{
				ID:           s.ID,
				Title:        fallback(s.Title, "(untitled)"),
				UpdatedAt:    s.UpdatedAt,
				MessageCount: len(s.Messages),
			},
			Summary:  s.Summary,
			Messages: convertMessages(s),
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
