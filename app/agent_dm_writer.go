package app

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

var (
	ErrNotAgentDMSource         = errors.New("agent_dm: source does not map to KindAgentDM")
	ErrAgentDMPersonaRequired   = errors.New("agent_dm: persona id required")
	ErrAgentDMPersonaConflict   = errors.New("agent_dm: source seed and explicit persona disagree")
	ErrAgentDMPersonaNotFound   = errors.New("agent_dm: persona not registered")
	ErrAgentDMSpaceNotFound     = errors.New("agent_dm: space id not found")
	ErrAgentDMSpaceMissingAgent = errors.New("agent_dm: space has no registered agent participant")
)

// agentDMSpaceIDPattern matches space ids that begin with an 8-digit
// date stamp (e.g. 20260101-agent-...). Persona ids never look like
// this, so the seed format is enough to disambiguate the two
// addressing modes for `desktop:agent:<X>`.
var agentDMSpaceIDPattern = regexp.MustCompile(`^\d{8}-`)

// resolveAgentDMPersonaID returns the persona id + info for an
// AgentDM source. The source seed may be either:
//   - a registered persona id (legacy / cli:agent:* / pre-multi-
//     instance UI); the singleton AgentDM Space for that persona is
//     returned by callers' EnsureSpace lookups.
//   - a Space id (multi-instance UI introduced in P10): the Space
//     is looked up directly and its registered agent participant is
//     used as the persona.
//
// The explicit personaID, when supplied, is cross-checked against
// whichever resolution mode applies.
func (a *App) resolveAgentDMPersonaID(source, explicit string) (string, *space.PersonaInfo, error) {
	target := space.MapSource(source)
	if target.Kind != space.KindAgentDM {
		return "", nil, ErrNotAgentDMSource
	}
	seed := strings.TrimSpace(target.Seed)
	got := strings.TrimSpace(explicit)
	if seed == "" && got == "" {
		return "", nil, ErrAgentDMPersonaRequired
	}

	// Multi-instance mode: seed is a Space id.
	if seed != "" && agentDMSpaceIDPattern.MatchString(seed) {
		sp, err := a.spaces.LoadSpace(seed)
		if err != nil || sp == nil {
			return "", nil, fmt.Errorf("%w: %s", ErrAgentDMSpaceNotFound, seed)
		}
		personaID := agentParticipantID(sp)
		if personaID == "" {
			return "", nil, fmt.Errorf("%w: %s", ErrAgentDMSpaceMissingAgent, seed)
		}
		if got != "" && got != personaID {
			return "", nil, ErrAgentDMPersonaConflict
		}
		p := a.personas.Get(personaID)
		if p == nil {
			return "", nil, fmt.Errorf("%w: %s", ErrAgentDMPersonaNotFound, personaID)
		}
		return p.ID, &space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}, nil
	}

	// Legacy / singleton mode: seed is the persona id.
	if seed != "" && got != "" && seed != got {
		return "", nil, ErrAgentDMPersonaConflict
	}
	id := got
	if id == "" {
		id = seed
	}
	p := a.personas.Get(id)
	if p == nil {
		return "", nil, fmt.Errorf("%w: %s", ErrAgentDMPersonaNotFound, id)
	}
	return p.ID, &space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}, nil
}

func agentParticipantID(sp *space.Space) string {
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

// resolveAgentDMTargetSpace returns the Space the source addresses.
// For multi-instance sources (seed = Space id) it loads that Space
// directly. For legacy / singleton sources (seed = persona id) it
// performs the EnsureSpace lookup so a fresh install also works.
func (a *App) resolveAgentDMTargetSpace(source, explicitPersonaID string) (*space.Space, *space.PersonaInfo, error) {
	target := space.MapSource(source)
	if target.Kind != space.KindAgentDM {
		return nil, nil, ErrNotAgentDMSource
	}
	personaID, info, err := a.resolveAgentDMPersonaID(source, explicitPersonaID)
	if err != nil {
		return nil, nil, err
	}
	seed := strings.TrimSpace(target.Seed)
	if seed != "" && agentDMSpaceIDPattern.MatchString(seed) {
		sp, err := a.spaces.LoadSpace(seed)
		if err != nil || sp == nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrAgentDMSpaceNotFound, seed)
		}
		return sp, info, nil
	}
	sp, err := a.spaces.EnsureSpace(space.KindAgentDM, personaID, *info)
	if err != nil {
		return nil, nil, err
	}
	return sp, info, nil
}

func (a *App) appendAgentDMUserToSpace(source, explicitPersonaID, content string) (*space.Message, error) {
	sp, _, err := a.resolveAgentDMTargetSpace(source, explicitPersonaID)
	if err != nil {
		return nil, err
	}
	written, err := a.spaces.AppendUserMessage(sp.ID, content, nil)
	if err != nil {
		return nil, err
	}
	return &written, nil
}

func (a *App) appendAgentDMAssistantToSpace(source, explicitPersonaID, content, reasoning string, mentions []string, parentMessageID string) (*space.Message, error) {
	sp, info, err := a.resolveAgentDMTargetSpace(source, explicitPersonaID)
	if err != nil {
		return nil, err
	}
	written, err := a.spaces.AppendAgentMessage(sp.ID, *info, content, reasoning, mentions, parentMessageID)
	if err != nil {
		return nil, err
	}
	return &written, nil
}
