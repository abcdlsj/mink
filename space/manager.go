package space

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/msg"
)

// ErrStaleDeliveryWrite is returned by FinalizeDeliveryMessage/FailDeliveryMessage
// when the caller's Delivery execution version is below the version already
// accepted into the target placeholder. It is the write-side fence: a superseded
// worker that wins the manager lock last still cannot overwrite a newer owner's
// reply, because the version comparison happens inside the same critical section
// as the save. Callers treat it as a benign lost-race signal (the newer owner is
// authoritative), never as a turn failure.
var ErrStaleDeliveryWrite = errors.New("stale delivery write: message already carries a newer delivery version")

type Store interface {
	SaveSpace(*Space) error
	LoadSpace(id string) (*Space, error)
	ListSpaces() ([]*Space, error)
	FindSpaceByKindAndKey(kind Kind, key string) (*Space, error)
	DeleteSpace(id string) error
	// SaveSpaceUnderDeliveryFence persists sp only if the presented Delivery
	// fence (ownerID, version) still owns the live lease as of now, checked in
	// the same store critical section as the write. Returns ErrStaleDeliveryWrite
	// (writing nothing) when a newer owner has superseded the fence. The fence is
	// passed as raw values so this package need not depend on the delivery domain.
	SaveSpaceUnderDeliveryFence(deliveryID, fenceOwnerID string, fenceVersion int64, now time.Time, sp *Space) error
}

type PersonaInfo struct {
	ID      string
	Display string
	Role    string
}

type Manager struct {
	store       Store
	mu          sync.Mutex
	drafts      map[string]*Space
	userID      string
	userDisplay string
	events      func(bus.Event)
}

func NewManager(store Store, userID, userDisplay string) *Manager {
	if userID == "" {
		userID = "user"
	}
	if userDisplay == "" {
		userDisplay = "You"
	}
	return &Manager{store: store, drafts: map[string]*Space{}, userID: userID, userDisplay: userDisplay}
}

func (m *Manager) SetEventSink(fn func(bus.Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = fn
}

func (m *Manager) publish(ev bus.Event) {
	if m == nil || m.events == nil {
		return
	}
	m.events(ev)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(id)
}

func (m *Manager) DeleteSpace(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	m.mu.Lock()
	if _, ok := m.drafts[id]; ok {
		delete(m.drafts, id)
		m.mu.Unlock()
		m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: id})
		return nil
	}
	err := m.store.DeleteSpace(id)
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: id})
	return nil
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
	case strings.HasPrefix(source, "cli:direct:"):
		rest := strings.TrimPrefix(source, "cli:direct:")
		if i := strings.IndexByte(rest, ':'); i >= 0 {
			rest = rest[:i]
		}
		return SourceTarget{Kind: KindDirectChat, Seed: rest}
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
		if sp, err := m.LoadSpace(t.Seed); err == nil && sp != nil && sp.Kind == t.Kind {
			return sp, nil
		}
		return nil, fmt.Errorf("%s space not found: %s", t.Kind, t.Seed)
	}
	return m.EnsureSpace(t.Kind, t.Seed, agent)
}

func (m *Manager) EnsureSpace(kind Kind, key string, agent PersonaInfo) (*Space, error) {
	m.mu.Lock()
	existing, err := m.store.FindSpaceByKindAndKey(kind, key)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if existing != nil {
		m.mu.Unlock()
		return existing, nil
	}
	parts, err := m.participants(kind, agent)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	sp := NewKeyed(kind, key, key, parts)
	if err := m.store.SaveSpace(sp); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{Type: bus.SpaceCreated, SpaceID: sp.ID, Text: string(sp.Kind)})
	return sp, nil
}

func (m *Manager) DraftSpace(kind Kind, key, title string, agent PersonaInfo) (*Space, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts, err := m.participants(kind, agent)
	if err != nil {
		return nil, err
	}
	sp := NewKeyed(kind, key, title, parts)
	m.drafts[sp.ID] = sp
	return sp, nil
}

func (m *Manager) loadLocked(id string) (*Space, error) {
	if sp := m.drafts[strings.TrimSpace(id)]; sp != nil {
		return sp, nil
	}
	return m.store.LoadSpace(id)
}

func (m *Manager) saveLocked(sp *Space) error {
	if err := m.store.SaveSpace(sp); err != nil {
		return err
	}
	delete(m.drafts, sp.ID)
	return nil
}

// saveSpaceUnderFence persists sp only if the routed worker's Delivery fence
// still owns the live lease, delegating the atomic check+write to the store
// (which serializes it against Claim on the shared store mutex). It mirrors
// saveLocked's draft cleanup on success. Must be called with m.mu held.
func (m *Manager) saveSpaceUnderFence(deliveryID, fenceOwnerID string, fenceVersion int64, now time.Time, sp *Space) error {
	if err := m.store.SaveSpaceUnderDeliveryFence(deliveryID, fenceOwnerID, fenceVersion, now, sp); err != nil {
		return err
	}
	delete(m.drafts, sp.ID)
	return nil
}

func (m *Manager) participants(kind Kind, agent PersonaInfo) ([]Participant, error) {
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
	return parts, nil
}

func (m *Manager) AppendMessageWithRouting(spaceID string, draft Message, resolved []string, resolveInfo func(id string) PersonaInfo) (Message, []string, error) {
	if strings.TrimSpace(draft.AuthorID) == "" {
		return Message{}, nil, fmt.Errorf("message missing author_id")
	}
	if draft.AuthorKind == "" {
		return Message{}, nil, fmt.Errorf("message missing author_kind")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, nil, err
	}
	_, wasDraft := m.drafts[sp.ID]
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
	if err := m.saveLocked(sp); err != nil {
		*sp = snapshot
		m.mu.Unlock()
		return Message{}, nil, err
	}
	m.mu.Unlock()
	if wasDraft {
		m.publish(bus.Event{Type: bus.SpaceCreated, SpaceID: sp.ID, Text: string(sp.Kind)})
	}
	m.publish(bus.Event{
		Type:            bus.SpaceMessageAdded,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	if len(added) > 0 {
		m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID})
	}
	return written, added, nil
}

// AppendMessageWithIntents appends draft and, in the SAME lock+save, attaches
// the routing intents produced by buildIntents. Persisting the message and its
// wake intents as one Space file commit is the durability guarantee behind
// "Space append-before-claim": a crash after this returns can always be
// recovered by reconcile reading the persisted RoutingIntents, because the
// intents can never be written in a later commit than the message.
//
// buildIntents MUST be a pure callback: it may only read the assigned message ID
// and the immutable snapshot of messages that existed BEFORE this append. It
// must NOT call back into the Manager or Space (the Manager lock is held —
// re-entering would deadlock). Every returned intent must carry a non-empty
// ChainRoot, and all intents in one append must share the same ChainRoot (a
// single message opens or continues exactly one chain); otherwise the append is
// rejected and rolled back so a degenerate intent can never reach the store and
// undercount the routing budget.
func (m *Manager) AppendMessageWithIntents(spaceID string, draft Message, resolved []string, resolveInfo func(id string) PersonaInfo, buildIntents func(assignedID string, existing []Message) []RoutingIntent) (Message, []string, error) {
	if strings.TrimSpace(draft.AuthorID) == "" {
		return Message{}, nil, fmt.Errorf("message missing author_id")
	}
	if draft.AuthorKind == "" {
		return Message{}, nil, fmt.Errorf("message missing author_kind")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, nil, err
	}
	_, wasDraft := m.drafts[sp.ID]
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
	if buildIntents != nil {
		// existing = messages that existed BEFORE this append (immutable copy).
		intents := buildIntents(written.ID, snapshot.Messages)
		if err := validateRoutingIntents(intents); err != nil {
			*sp = snapshot
			m.mu.Unlock()
			return Message{}, nil, err
		}
		if len(intents) > 0 {
			for i := range sp.Messages {
				if sp.Messages[i].ID == written.ID {
					sp.Messages[i].RoutingIntents = intents
					written.RoutingIntents = intents
					break
				}
			}
		}
	}
	if err := m.saveLocked(sp); err != nil {
		*sp = snapshot
		m.mu.Unlock()
		return Message{}, nil, err
	}
	m.mu.Unlock()
	if wasDraft {
		m.publish(bus.Event{Type: bus.SpaceCreated, SpaceID: sp.ID, Text: string(sp.Kind)})
	}
	m.publish(bus.Event{
		Type:            bus.SpaceMessageAdded,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	if len(added) > 0 {
		m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID})
	}
	return written, added, nil
}

// validateRoutingIntents enforces the save-time invariant: every intent has a
// non-empty ChainRoot and they all share one root. An empty ChainRoot would let
// the routing-budget accounting (DefaultBudget - count(ChainRoot==root))
// undercount; mixed roots in one append would mean a single message opened two
// chains, which the recovery model cannot represent.
func validateRoutingIntents(intents []RoutingIntent) error {
	root := ""
	for _, it := range intents {
		cr := strings.TrimSpace(it.ChainRoot)
		if cr == "" {
			return fmt.Errorf("routing intent for %q has empty chain_root", it.AgentID)
		}
		if root == "" {
			root = cr
			continue
		}
		if cr != root {
			return fmt.Errorf("routing intents span multiple chain roots (%q vs %q)", root, cr)
		}
	}
	return nil
}

// AttachRoutingIntents persists continuation wake intents onto an existing
// message (typically an assistant reply that mentions further agents) in one
// lock+save. It is the "reply-before-complete" durability point for chained
// wakes: the intent that a reply triggers is written to the Space before the
// downstream Delivery is created, so a crash in between is recoverable. The same
// non-empty/single-root invariant applies.
func (m *Manager) AttachRoutingIntents(spaceID, messageID string, intents []RoutingIntent) (Message, error) {
	if err := validateRoutingIntents(intents); err != nil {
		return Message{}, err
	}
	return m.UpdateMessage(spaceID, messageID, func(msg *Message) {
		msg.RoutingIntents = intents
	})
}

// EnsureDeliveryPlaceholder returns the single assistant placeholder message for
// a Delivery, appending a new pending one only if none exists yet. It is
// idempotent by DeliveryID: the worker calls it on every (re)claim, and the
// invariant "one assistant placeholder per DeliveryID in a Space" holds across
// crashes in the append->BindResultMessage window. If a placeholder already
// exists (found by DeliveryID), it is returned untouched so a retry reuses the
// same visible message rather than appending a duplicate; existing == true lets
// the caller distinguish "recovered" from "freshly created".
//
// deliveryID must be non-empty — an empty DeliveryID would collapse every
// direct-path reply into one placeholder. agentID is the replying persona;
// parentMessageID threads the placeholder under the origin (empty for a
// top-level channel wake).
func (m *Manager) EnsureDeliveryPlaceholder(spaceID, deliveryID, agentID, parentMessageID string, resolveInfo func(id string) PersonaInfo) (message Message, existing bool, err error) {
	deliveryID = strings.TrimSpace(deliveryID)
	agentID = strings.TrimSpace(agentID)
	if deliveryID == "" {
		return Message{}, false, fmt.Errorf("delivery placeholder requires delivery_id")
	}
	if agentID == "" {
		return Message{}, false, fmt.Errorf("delivery placeholder requires agent_id")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, false, err
	}
	for i := range sp.Messages {
		if sp.Messages[i].DeliveryID == deliveryID {
			found := sp.Messages[i]
			m.mu.Unlock()
			return found, true, nil
		}
	}
	if !sp.HasParticipant(agentID) {
		info := PersonaInfo{ID: agentID}
		if resolveInfo != nil {
			info = resolveInfo(agentID)
			if info.ID == "" {
				info.ID = agentID
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
	}
	written := sp.AddMessage(Message{
		AuthorID:        agentID,
		AuthorKind:      ParticipantAgent,
		Status:          "pending",
		ParentMessageID: strings.TrimSpace(parentMessageID),
		DeliveryID:      deliveryID,
	})
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return Message{}, false, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:            bus.SpaceMessageAdded,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	return written, false, nil
}

// FinalizeDeliveryMessage fills the Delivery's pending placeholder with the
// produced reply and, in the SAME lock+save, attaches any continuation wake
// intents the reply triggers. Binding message content and its downstream intents
// as one Space commit is the "reply-before-complete" durability point: a crash
// after this returns is recoverable because the chained intent can never be
// written in a later commit than the reply that produced it.
//
// The placeholder is located by messageID (the id BindResultMessage fenced onto
// the Delivery), NOT re-appended, so the one-placeholder-per-DeliveryID invariant
// is preserved. buildIntents is a pure callback with the same contract as
// AppendMessageWithIntents: it may only read the assigned id and the immutable
// pre-existing message snapshot, never call back into the Manager. added lists
// any participants newly introduced by the reply's mentions.
func (m *Manager) FinalizeDeliveryMessage(spaceID, messageID, deliveryID, fenceOwnerID string, fenceVersion int64, now time.Time, apply func(*Message), resolved []string, resolveInfo func(id string) PersonaInfo, buildIntents func(assignedID string, existing []Message) []RoutingIntent) (message Message, added []string, err error) {
	spaceID = strings.TrimSpace(spaceID)
	messageID = strings.TrimSpace(messageID)
	if spaceID == "" || messageID == "" {
		return Message{}, nil, fmt.Errorf("space id and message id required")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, nil, err
	}
	snapshot := *sp
	snapshot.Participants = append([]Participant(nil), sp.Participants...)
	snapshot.Messages = append([]Message(nil), sp.Messages...)

	idx := -1
	for i := range sp.Messages {
		if sp.Messages[i].ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return Message{}, nil, fmt.Errorf("delivery placeholder not found: %s", messageID)
	}

	added = make([]string, 0, len(resolved))
	for _, id := range resolved {
		id = strings.TrimSpace(id)
		if id == "" || sp.HasParticipant(id) {
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

	if apply != nil {
		apply(&sp.Messages[idx])
	}
	if buildIntents != nil {
		intents := buildIntents(messageID, snapshot.Messages)
		if err := validateRoutingIntents(intents); err != nil {
			*sp = snapshot
			m.mu.Unlock()
			return Message{}, nil, err
		}
		if len(intents) > 0 {
			sp.Messages[idx].RoutingIntents = intents
		}
	}
	sp.UpdatedAt = time.Now()
	written := sp.Messages[idx]
	// Persist under the write-side fence: the store re-reads the authoritative
	// Delivery and rejects (writing nothing) if this fence no longer owns the
	// live lease — the linearization point that stops a superseded worker from
	// landing content over a newer owner. m.mu is held across this call so a
	// concurrent user append cannot lost-update against the same load snapshot.
	if err := m.saveSpaceUnderFence(deliveryID, fenceOwnerID, fenceVersion, now, sp); err != nil {
		*sp = snapshot
		m.mu.Unlock()
		return Message{}, nil, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:            bus.SpaceUpdated,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	if len(added) > 0 {
		m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID})
	}
	return written, added, nil
}

// FailDeliveryMessage marks a Delivery's placeholder failed under the same
// write-side live-lease fence as FinalizeDeliveryMessage. It is the
// headless-orphan projection: on a server with no desktop backend consuming
// TurnError, the placeholder is the sole durable record of the failure, so a
// failed worker must flip it out of "pending" — but only while its fence still
// owns the live lease. A superseded worker's Fail can therefore never flip a
// newer owner's reply/placeholder to failed (returns ErrStaleDeliveryWrite).
func (m *Manager) FailDeliveryMessage(spaceID, messageID, deliveryID, fenceOwnerID string, fenceVersion int64, now time.Time, errText string) (Message, error) {
	spaceID = strings.TrimSpace(spaceID)
	messageID = strings.TrimSpace(messageID)
	if spaceID == "" || messageID == "" {
		return Message{}, fmt.Errorf("space id and message id required")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, err
	}
	snapshot := *sp
	snapshot.Messages = append([]Message(nil), sp.Messages...)
	idx := -1
	for i := range sp.Messages {
		if sp.Messages[i].ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return Message{}, fmt.Errorf("delivery placeholder not found: %s", messageID)
	}
	sp.Messages[idx].Status = "failed"
	sp.Messages[idx].Error = errText
	sp.UpdatedAt = time.Now()
	written := sp.Messages[idx]
	if err := m.saveSpaceUnderFence(deliveryID, fenceOwnerID, fenceVersion, now, sp); err != nil {
		*sp = snapshot
		m.mu.Unlock()
		return Message{}, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:            bus.SpaceUpdated,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	return written, nil
}

func (m *Manager) ResetDeliveryMessage(spaceID, messageID, deliveryID, fenceOwnerID string, fenceVersion int64, now time.Time) (Message, error) {
	spaceID = strings.TrimSpace(spaceID)
	messageID = strings.TrimSpace(messageID)
	if spaceID == "" || messageID == "" {
		return Message{}, fmt.Errorf("space id and message id required")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, err
	}
	snapshot := *sp
	snapshot.Messages = append([]Message(nil), sp.Messages...)
	idx := -1
	for i := range sp.Messages {
		if sp.Messages[i].ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.mu.Unlock()
		return Message{}, fmt.Errorf("delivery placeholder not found: %s", messageID)
	}
	sp.Messages[idx].Status = "pending"
	sp.Messages[idx].Error = ""
	sp.UpdatedAt = time.Now()
	written := sp.Messages[idx]
	if err := m.saveSpaceUnderFence(deliveryID, fenceOwnerID, fenceVersion, now, sp); err != nil {
		*sp = snapshot
		m.mu.Unlock()
		return Message{}, err
	}
	m.mu.Unlock()
	m.publish(bus.Event{
		Type:            bus.SpaceUpdated,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	return written, nil
}

func (m *Manager) AppendUserMessage(spaceID, content string, mentions []string) (Message, error) {
	return m.AppendUserMessageInThread(spaceID, "", content, mentions)
}

func (m *Manager) AppendUserMessageInThread(spaceID, parentMessageID, content string, mentions []string) (Message, error) {
	return m.AppendUserMessageWithAttachmentsInThread(spaceID, parentMessageID, content, mentions, nil)
}

func (m *Manager) AppendUserMessageWithAttachmentsInThread(spaceID, parentMessageID, content string, mentions []string, attachments []msg.Attachment) (Message, error) {
	return m.appendMessage(spaceID, Message{
		AuthorID:        m.userID,
		AuthorKind:      ParticipantUser,
		Content:         content,
		Attachments:     cloneAttachments(attachments),
		Mentions:        mentions,
		ParentMessageID: strings.TrimSpace(parentMessageID),
	})
}

func (m *Manager) AppendAgentMessage(spaceID string, agent PersonaInfo, content, reasoning string, mentions []string, parentMessageID string, usage *msg.TokenUsage, runtimeMeta map[string]string) (Message, error) {
	return m.AppendAgentMessageWithAttachments(spaceID, agent, content, reasoning, mentions, parentMessageID, usage, runtimeMeta, nil)
}

func (m *Manager) AppendAgentMessageWithAttachments(spaceID string, agent PersonaInfo, content, reasoning string, mentions []string, parentMessageID string, usage *msg.TokenUsage, runtimeMeta map[string]string, attachments []msg.Attachment) (Message, error) {
	if strings.TrimSpace(agent.ID) == "" {
		return Message{}, fmt.Errorf("agent message rejected: empty author_id")
	}
	return m.appendMessage(spaceID, Message{
		AuthorID:        agent.ID,
		AuthorKind:      ParticipantAgent,
		Content:         content,
		Attachments:     cloneAttachments(attachments),
		Reasoning:       reasoning,
		Mentions:        mentions,
		ParentMessageID: parentMessageID,
		Usage:           usage,
		RuntimeMeta:     runtimeMeta,
	})
}

func cloneAttachments(in []msg.Attachment) []msg.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]msg.Attachment, len(in))
	copy(out, in)
	return out
}

func (m *Manager) UpdateMessage(spaceID, messageID string, update func(*Message)) (Message, error) {
	spaceID = strings.TrimSpace(spaceID)
	messageID = strings.TrimSpace(messageID)
	if spaceID == "" || messageID == "" {
		return Message{}, fmt.Errorf("space id and message id required")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, err
	}
	for i := range sp.Messages {
		if sp.Messages[i].ID != messageID {
			continue
		}
		if update != nil {
			update(&sp.Messages[i])
		}
		sp.UpdatedAt = time.Now()
		written := sp.Messages[i]
		if err := m.saveLocked(sp); err != nil {
			m.mu.Unlock()
			return Message{}, err
		}
		m.mu.Unlock()
		m.publish(bus.Event{
			Type:            bus.SpaceUpdated,
			SpaceID:         spaceID,
			MessageID:       written.ID,
			ParentMessageID: written.ParentMessageID,
			AgentID:         written.AuthorID,
		})
		return written, nil
	}
	m.mu.Unlock()
	return Message{}, fmt.Errorf("message not found: %s", messageID)
}

func (m *Manager) DeleteMessage(spaceID, messageID string) error {
	spaceID = strings.TrimSpace(spaceID)
	messageID = strings.TrimSpace(messageID)
	if spaceID == "" || messageID == "" {
		return nil
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	for i := range sp.Messages {
		if sp.Messages[i].ID != messageID {
			continue
		}
		parentID := sp.Messages[i].ParentMessageID
		agentID := sp.Messages[i].AuthorID
		sp.Messages = append(sp.Messages[:i], sp.Messages[i+1:]...)
		sp.UpdatedAt = time.Now()
		if err := m.saveLocked(sp); err != nil {
			m.mu.Unlock()
			return err
		}
		m.mu.Unlock()
		m.publish(bus.Event{
			Type:            bus.SpaceUpdated,
			SpaceID:         spaceID,
			MessageID:       messageID,
			ParentMessageID: parentID,
			AgentID:         agentID,
		})
		return nil
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) appendMessage(spaceID string, message Message) (Message, error) {
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return Message{}, err
	}
	_, wasDraft := m.drafts[sp.ID]
	if message.AuthorID == "" {
		m.mu.Unlock()
		return Message{}, fmt.Errorf("message missing author_id (space=%s)", spaceID)
	}
	if message.AuthorKind == "" {
		m.mu.Unlock()
		return Message{}, fmt.Errorf("message missing author_kind (space=%s, author=%s)", spaceID, message.AuthorID)
	}
	written := sp.AddMessage(message)
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return Message{}, err
	}
	m.mu.Unlock()
	if wasDraft {
		m.publish(bus.Event{Type: bus.SpaceCreated, SpaceID: sp.ID, Text: string(sp.Kind)})
	}
	m.publish(bus.Event{
		Type:            bus.SpaceMessageAdded,
		SpaceID:         spaceID,
		MessageID:       written.ID,
		ParentMessageID: written.ParentMessageID,
		AgentID:         written.AuthorID,
	})
	return written, nil
}

func (m *Manager) UpdateTitle(spaceID, title string) error {
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	t := strings.TrimSpace(title)
	if sp.Title == t {
		m.mu.Unlock()
		return nil
	}
	sp.Title = t
	sp.UpdatedAt = time.Now()
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish(bus.Event{Type: bus.SpaceTitleChanged, SpaceID: spaceID, Text: t})
	m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID})
	return nil
}

func (m *Manager) SetAgentMode(spaceID, personaID, mode string) error {
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	personaID = strings.TrimSpace(personaID)
	if personaID == "" {
		m.mu.Unlock()
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
		m.mu.Unlock()
		return fmt.Errorf("invalid agent mode: %s", mode)
	}
	sp.UpdatedAt = time.Now()
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID, AgentID: personaID})
	return nil
}

func (m *Manager) AddAgentParticipant(spaceID string, info PersonaInfo) error {
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if strings.TrimSpace(info.ID) == "" {
		m.mu.Unlock()
		return fmt.Errorf("persona id required")
	}
	if !sp.AddParticipant(Participant{
		ID:      info.ID,
		Kind:    ParticipantAgent,
		Display: fallbackDisplay(info.Display, info.ID),
		Role:    info.Role,
		Status:  StatusAvailable,
	}) {
		m.mu.Unlock()
		return nil
	}
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID, AgentID: info.ID})
	return nil
}

func (m *Manager) SetThreadAgentMode(spaceID, parentMessageID, personaID, mode string) error {
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	parentMessageID = strings.TrimSpace(parentMessageID)
	personaID = strings.TrimSpace(personaID)
	if parentMessageID == "" || personaID == "" {
		m.mu.Unlock()
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
		m.mu.Unlock()
		return fmt.Errorf("invalid agent mode: %s", mode)
	}
	if len(bucket) == 0 {
		delete(sp.ThreadAgentModes, parentMessageID)
	} else {
		sp.ThreadAgentModes[parentMessageID] = bucket
	}
	sp.UpdatedAt = time.Now()
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID, ParentMessageID: parentMessageID, AgentID: personaID})
	return nil
}

func (m *Manager) DeleteThread(spaceID, parentMessageID string) error {
	spaceID = strings.TrimSpace(spaceID)
	parentMessageID = strings.TrimSpace(parentMessageID)
	if spaceID == "" || parentMessageID == "" {
		return fmt.Errorf("space id and parent message id required")
	}
	m.mu.Lock()
	sp, err := m.loadLocked(spaceID)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	next := sp.Messages[:0]
	for _, message := range sp.Messages {
		if strings.TrimSpace(message.ParentMessageID) == parentMessageID {
			continue
		}
		next = append(next, message)
	}
	sp.Messages = next
	if sp.ThreadAgentModes != nil {
		delete(sp.ThreadAgentModes, parentMessageID)
	}
	sp.UpdatedAt = time.Now()
	if err := m.saveLocked(sp); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.publish(bus.Event{Type: bus.SpaceUpdated, SpaceID: spaceID, ParentMessageID: parentMessageID})
	return nil
}

func fallbackDisplay(display, id string) string {
	if d := strings.TrimSpace(display); d != "" {
		return d
	}
	return id
}
