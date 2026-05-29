package space

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Store is the persistence boundary used by Manager. It is satisfied
// by store.Store; an in-memory variant is provided for tests.
type Store interface {
	SaveSpace(*Space) error
	LoadSpace(id string) (*Space, error)
	ListSpaces() ([]*Space, error)
	FindSpaceByKindAndSeed(kind Kind, seed string) (*Space, error)
}

// PersonaInfo is the minimum data the manager needs to seed an
// agent participant. Adapters (app.App / desktop) project their
// richer persona structs into this.
type PersonaInfo struct {
	ID      string
	Display string
	Role    string
}

// Manager wraps a Store with the seeding + author rules required by
// the proposal. Every public method enforces:
//   - user participant is always present in any space the user can
//     post into;
//   - agent_dm has exactly user + that single agent;
//   - channels and direct_chats start with user only; agents are
//     added later via @-routing or explicit invite (P2);
//   - messages must carry a real AuthorID + AuthorKind; the manager
//     refuses writes that violate this.
type Manager struct {
	store Store
	mu    sync.Mutex
	// userID identifies the local human (single-tenant MVP).
	userID      string
	userDisplay string
}

// NewManager constructs a Manager bound to a persistence backend.
// userID identifies the local user; left empty it falls back to
// the literal "user" so participant rows are still well-formed.
func NewManager(store Store, userID, userDisplay string) *Manager {
	if userID == "" {
		userID = "user"
	}
	if userDisplay == "" {
		userDisplay = "You"
	}
	return &Manager{store: store, userID: userID, userDisplay: userDisplay}
}

// UserParticipant returns the seed user participant.
func (m *Manager) UserParticipant() Participant {
	return Participant{
		ID:       m.userID,
		Kind:     ParticipantUser,
		Display:  m.userDisplay,
		Status:   StatusAvailable,
		JoinedAt: time.Now(),
	}
}

// SourceTarget describes where a legacy `source` string lands in
// the new model. P1 uses this to route dual-writes; P3 swaps
// readers over.
type SourceTarget struct {
	Kind Kind
	Seed string // identifies the singleton instance per kind
}

// MapSource normalizes the existing source-string vocabulary onto
// (Kind, Seed). It is the bridge between sumi's per-source single
// session and the multi-Space model.
//
// Mapping rules (proposal §8):
//   "desktop"            -> channel "default"
//   "desktop:agent:<id>" -> agent_dm <id>      (incl. :persona:<id> tail)
//   "cli"                -> direct_chat "cli"
//   "tg:<chat>"          -> direct_chat tg:<chat>
//   "subtask:..."        -> not a Space; caller should not call MapSource
//   anything else        -> direct_chat with the raw source as seed
func MapSource(source string) SourceTarget {
	source = strings.TrimSpace(source)
	switch {
	case source == "" || source == "desktop":
		return SourceTarget{Kind: KindChannel, Seed: "default"}
	case strings.HasPrefix(source, "desktop:agent:"):
		rest := strings.TrimPrefix(source, "desktop:agent:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindAgentDM, Seed: rest}
	case source == "cli":
		return SourceTarget{Kind: KindDirectChat, Seed: "cli"}
	case strings.HasPrefix(source, "tg:"):
		return SourceTarget{Kind: KindDirectChat, Seed: source}
	case strings.HasPrefix(source, "subtask:"):
		// subtasks live on a Task's private timeline, not a Space;
		// callers are expected to skip MapSource for these.
		return SourceTarget{}
	default:
		return SourceTarget{Kind: KindDirectChat, Seed: source}
	}
}

// EnsureForSource returns (and creates if missing) the Space that
// represents the given source string. It applies the seeding rules
// from Iris's amendment 2 by passing through EnsureSpace.
func (m *Manager) EnsureForSource(source string, agent PersonaInfo) (*Space, error) {
	t := MapSource(source)
	if t.Kind == "" {
		return nil, fmt.Errorf("source %q does not map to a space", source)
	}
	return m.EnsureSpace(t.Kind, t.Seed, agent)
}

// EnsureSpace returns the singleton Space for (kind, seed), creating
// it with the proper participant seed if it does not exist yet.
//
// agent is consulted only when kind == KindAgentDM; for channels
// and direct chats it may be left zero.
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
		// user-only seed by design; agents join via @ in P2.
	default:
		return nil, fmt.Errorf("unknown space kind %q", kind)
	}
	sp := New(kind, seed, parts)
	if err := m.store.SaveSpace(sp); err != nil {
		return nil, err
	}
	return sp, nil
}

// AppendUserMessage writes a user-authored message into the given
// space and persists. content may be empty for tool-only turns.
func (m *Manager) AppendUserMessage(spaceID, content string, mentions []string) (Message, error) {
	return m.appendMessage(spaceID, Message{
		AuthorID:   m.userID,
		AuthorKind: ParticipantUser,
		Content:    content,
		Mentions:   mentions,
	})
}

// AppendAgentMessage writes an agent-authored message into the given
// space. The author must identify a real persona id; an empty id
// is rejected per Iris's hard rule.
func (m *Manager) AppendAgentMessage(spaceID string, agent PersonaInfo, content, reasoning string, mentions []string, parentMessageID string) (Message, error) {
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
	})
}

func (m *Manager) appendMessage(spaceID string, msg Message) (Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sp, err := m.store.LoadSpace(spaceID)
	if err != nil {
		return Message{}, err
	}
	if msg.AuthorID == "" {
		return Message{}, fmt.Errorf("message missing author_id (space=%s)", spaceID)
	}
	if msg.AuthorKind == "" {
		return Message{}, fmt.Errorf("message missing author_kind (space=%s, author=%s)", spaceID, msg.AuthorID)
	}
	written := sp.AddMessage(msg)
	if err := m.store.SaveSpace(sp); err != nil {
		return Message{}, err
	}
	return written, nil
}

func fallbackDisplay(display, id string) string {
	if d := strings.TrimSpace(display); d != "" {
		return d
	}
	return id
}
