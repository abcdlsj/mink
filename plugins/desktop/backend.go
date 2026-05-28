package desktop

import (
	"context"
	"sort"
	"strings"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
)

type Backend struct {
	app  *app.App
	subs *fanout
}

func newBackend(a *app.App) *Backend {
	return &Backend{app: a, subs: newFanout()}
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
	return SessionDetail{}, nil
}

func (b *Backend) ListChannels() []ChannelItem {
	if b.app == nil {
		return mockChannels()
	}
	return mockChannels()
}

func (b *Backend) ListThreads() []ThreadItem {
	if b.app == nil {
		return mockThreads()
	}
	return mockThreads()
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
	return mockChannelDetail(id)
}

func (b *Backend) GetThread(id string) SessionDetail {
	return mockThreadDetail(id)
}

func (b *Backend) GetParticipants(channelID, threadID string) ParticipantsView {
	return mockParticipants(channelID, threadID)
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
	if b.app == nil {
		return mockTools()
	}
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

func (b *Backend) SendMessage(req SendRequest) (string, error) {
	if b.app == nil {
		return "", nil
	}
	ctx := context.Background()
	if req.PersonaID != "" {
		return b.app.HandleInputAs(ctx, "desktop", req.PersonaID, req.Input)
	}
	return b.app.HandleInput(ctx, "desktop", req.Input)
}

func (b *Backend) StopTurn(sessionID string) error {
	return nil
}

func (b *Backend) Subscribe() (<-chan BusEvent, func()) {
	return b.subs.subscribe(256)
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
