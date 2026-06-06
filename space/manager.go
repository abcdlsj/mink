package space

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/msg"
)

type Store interface {
	SaveSpace(*Space) error
	LoadSpace(id string) (*Space, error)
	ListSpaces() ([]*Space, error)
	FindSpaceByKindAndSeed(kind Kind, seed string) (*Space, error)
}

type PersonaInfo struct {
	ID      string
	Display string
	Role    string
}

type Manager struct {
	store       Store
	mu          sync.Mutex
	userID      string
	userDisplay string
}

func NewManager(store Store, userID, userDisplay string) *Manager {
	if userID == "" {
		userID = "user"
	}
	if userDisplay == "" {
		userDisplay = "You"
	}
	return &Manager{store: store, userID: userID, userDisplay: userDisplay}
}

func (m *Manager) UserParticipant() Participant {
	return Participant{
		ID:       m.userID,
		Kind:     ParticipantUser,
		Display:  m.userDisplay,
		Status:   StatusAvailable,
		JoinedAt: time.Now(),
	}
}

func (m *Manager) Store() Store { return m.store }

func (m *Manager) ListSpaces() ([]*Space, error) {
	return m.store.ListSpaces()
}

func (m *Manager) LoadSpace(id string) (*Space, error) {
	return m.store.LoadSpace(id)
}

type SourceTarget struct {
	Kind Kind
	Seed string
}

func MapSource(source string) SourceTarget {
	source = strings.TrimSpace(source)
	switch {
	case source == "" || source == "desktop":
		return SourceTarget{Kind: KindDirectChat, Seed: "Sumi"}
	case strings.HasPrefix(source, "desktop:channel:"):
		rest := strings.TrimPrefix(source, "desktop:channel:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindChannel, Seed: rest}
	case strings.HasPrefix(source, "cli:channel:"):
		rest := strings.TrimPrefix(source, "cli:channel:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindChannel, Seed: rest}
	case strings.HasPrefix(source, "desktop:agent:"):
		rest := strings.TrimPrefix(source, "desktop:agent:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindAgentDM, Seed: rest}
	case strings.HasPrefix(source, "desktop:direct:"):
		rest := strings.TrimPrefix(source, "desktop:direct:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindDirectChat, Seed: rest}
	case strings.HasPrefix(source, "cli:agent:"):
		rest := strings.TrimPrefix(source, "cli:agent:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindAgentDM, Seed: rest}
	case source == "cli":
		return SourceTarget{Kind: KindDirectChat, Seed: "cli"}
	case strings.HasPrefix(source, "tg:dm:"):
		return SourceTarget{Kind: KindDirectChat, Seed: source}
	case strings.HasPrefix(source, "tg:channel:"):
		return SourceTarget{Kind: KindChannel, Seed: source}
	case strings.HasPrefix(source, "subtask:"):
		return SourceTarget{}
	case strings.HasPrefix(source, "scratch:"):
		return SourceTarget{}
	default:
		return SourceTarget{Kind: KindDirectChat, Seed: source}
	}
}

func (m *Manager) Resolve(source string, agent PersonaInfo) (*Space, error) {
	t := MapSource(source)
	if t.Kind == "" {
		return nil, fmt.Errorf("source %q does not map to a space", source)
	}
	if IsSpaceID(t.Seed) {
		if sp, err := m.store.LoadSpace(t.Seed); err == nil && sp != nil && sp.Kind == t.Kind {
			return sp, nil
		}
	}
	return m.EnsureSpace(t.Kind, t.Seed, agent)
}

func (m *Manager) EnsureSpace(kind Kind, seed string, agent PersonaInfo) (*Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, err := m.store.FindSpaceByKindAndSeed(kind, seed)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	parts := []Participant{m.UserParticipant()}
	switch kind {
	case KindAgentDM:
		if strings.TrimSpace(agent.ID) == "" {
			return nil, fmt.Errorf("agent_dm requires a persona id; got empty")
		}
		parts = append(parts, Participant{
			ID:       agent.ID,
			Kind:     ParticipantAgent,
			Display:  fallbackDisplay(agent.Display, agent.ID),
			Role:     agent.Role,
			Status:   StatusAvailable,
			JoinedAt: time.Now(),
		})
	case KindChannel, KindDirectChat:
	default:
		return nil, fmt.Errorf("unknown space kind %q", kind)
	}
	sp := New(kind, seed, parts)
	if err := m.store.SaveSpace(sp); err != nil {
		return nil, err
	}
	return sp, nil
}

func (m *Manager) AppendMessageWithRouting(spaceID string, draft Message, resolved []string, resolveInfo func(id string) PersonaInfo) (Message, []string, error) {
	if strings.TrimSpace(draft.AuthorID) == "" {
		return Message{}, nil, fmt.Errorf("message missing author_id")
	}
	if draft.AuthorKind == "" {
		return Message{}, nil, fmt.Errorf("message missing author_kind")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return Message{}, nil, err
	}
	snapshot := *sp
	snapshot.Participants = append([]Participant(nil), sp.Participants...)
	snapshot.Messages = append([]Message(nil), sp.Messages...)

	added := make([]string, 0, len(resolved))
	for _, id := range resolved {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if sp.HasParticipant(id) {
			continue
		}
		info := PersonaInfo{ID: id}
		if resolveInfo != nil {
			info = resolveInfo(id)
			if info.ID == "" {
				info.ID = id
			}
		}
		sp.AddParticipant(Participant{
			ID:       info.ID,
			Kind:     ParticipantAgent,
			Display:  fallbackDisplay(info.Display, info.ID),
			Role:     info.Role,
			Status:   StatusAvailable,
			JoinedAt: time.Now(),
		})
		added = append(added, info.ID)
	}

	written := sp.AddMessage(draft)
	if err := m.store.SaveSpace(sp); err != nil {
		*sp = snapshot
		return Message{}, nil, err
	}
	return written, added, nil
}

func (m *Manager) AppendUserMessage(spaceID, content string, mentions []string) (Message, error) {
	return m.appendMessage(spaceID, Message{
		AuthorID:   m.userID,
		AuthorKind: ParticipantUser,
		Content:    content,
		Mentions:   mentions,
	})
}

func (m *Manager) AppendAgentMessage(spaceID string, agent PersonaInfo, content, reasoning string, mentions []string, parentMessageID string, usage *msg.TokenUsage) (Message, error) {
	if strings.TrimSpace(agent.ID) == "" {
		return Message{}, fmt.Errorf("agent message rejected: empty author_id")
	}
	return m.appendMessage(spaceID, Message{
		AuthorID:        agent.ID,
		AuthorKind:      ParticipantAgent,
		Content:         content,
		Reasoning:       reasoning,
		Mentions:        mentions,
		ParentMessageID: parentMessageID,
		Usage:           usage,
	})
}

func (m *Manager) appendMessage(spaceID string, message Message) (Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return Message{}, err
	}
	if message.AuthorID == "" {
		return Message{}, fmt.Errorf("message missing author_id (space=%s)", spaceID)
	}
	if message.AuthorKind == "" {
		return Message{}, fmt.Errorf("message missing author_kind (space=%s, author=%s)", spaceID, message.AuthorID)
	}
	written := sp.AddMessage(message)
	if err := m.store.SaveSpace(sp); err != nil {
		return Message{}, err
	}
	return written, nil
}

func (m *Manager) UpdateTitle(spaceID, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return err
	}
	t := strings.TrimSpace(title)
	if sp.Title == t {
		return nil
	}
	sp.Title = t
	sp.UpdatedAt = time.Now()
	return m.store.SaveSpace(sp)
}

func (m *Manager) SetAgentMode(spaceID, personaID, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return err
	}
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		return fmt.Errorf("persona id required")
	}
	if sp.AgentModes == nil {
		sp.AgentModes = map[string]string{}
	}
	switch mode {
	case "listen":
		sp.AgentModes[personaID] = "listen"
	case "mention_only", "":
		delete(sp.AgentModes, personaID)
	default:
		return fmt.Errorf("invalid agent mode: %s", mode)
	}
	sp.UpdatedAt = time.Now()
	return m.store.SaveSpace(sp)
}

func (m *Manager) AddAgentParticipant(spaceID string, info PersonaInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(info.ID) == "" {
		return fmt.Errorf("persona id required")
	}
	if !sp.AddParticipant(Participant{
		ID:      info.ID,
		Kind:    ParticipantAgent,
		Display: fallbackDisplay(info.Display, info.ID),
		Role:    info.Role,
		Status:  StatusAvailable,
	}) {
		return nil
	}
	return m.store.SaveSpace(sp)
}

func (m *Manager) SetThreadAgentMode(spaceID, parentMessageID, personaID, mode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return err
	}
	parentMessageID = strings.TrimSpace(parentMessageID)
	personaID = strings.TrimSpace(personaID)
	if parentMessageID == "" || personaID == "" {
		return fmt.Errorf("parent message id and persona id required")
	}
	if sp.ThreadAgentModes == nil {
		sp.ThreadAgentModes = map[string]map[string]string{}
	}
	bucket, ok := sp.ThreadAgentModes[parentMessageID]
	if !ok {
		bucket = map[string]string{}
	}
	switch mode {
	case "listen", "mention_only":
		bucket[personaID] = mode
	case "":
		delete(bucket, personaID)
	default:
		return fmt.Errorf("invalid agent mode: %s", mode)
	}
	if len(bucket) == 0 {
		delete(sp.ThreadAgentModes, parentMessageID)
	} else {
		sp.ThreadAgentModes[parentMessageID] = bucket
	}
	sp.UpdatedAt = time.Now()
	return m.store.SaveSpace(sp)
}

func fallbackDisplay(display, id string) string {
	if d := strings.TrimSpace(display); d != "" {
		return d
	}
	return id
}
