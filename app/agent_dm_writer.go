package app

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

var (
	ErrNotAgentDMSource         = errors.New("agent_dm: source is not agent_dm")
	ErrAgentDMPersonaRequired   = errors.New("agent_dm: persona id required")
	ErrAgentDMPersonaConflict   = errors.New("agent_dm: source and explicit persona disagree")
	ErrAgentDMPersonaNotFound   = errors.New("agent_dm: persona not registered")
	ErrAgentDMSpaceNotFound     = errors.New("agent_dm: space id not found")
	ErrAgentDMSpaceMissingAgent = errors.New("agent_dm: space has no agent participant")
)

var spaceIDPattern = regexp.MustCompile(`^\d{8}-`)

func isSpaceID(s string) bool { return spaceIDPattern.MatchString(s) }

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

	if seed != "" && isSpaceID(seed) {
		sp, err := a.spaces.LoadSpace(seed)
		if err != nil || sp == nil {
			return "", nil, fmt.Errorf("%w: %s", ErrAgentDMSpaceNotFound, seed)
		}
		pid := agentParticipantID(sp)
		if pid == "" {
			return "", nil, fmt.Errorf("%w: %s", ErrAgentDMSpaceMissingAgent, seed)
		}
		if got != "" && got != pid {
			return "", nil, ErrAgentDMPersonaConflict
		}
		return a.personaInfo(pid)
	}

	if seed != "" && got != "" && seed != got {
		return "", nil, ErrAgentDMPersonaConflict
	}
	id := got
	if id == "" {
		id = seed
	}
	return a.personaInfo(id)
}

func (a *App) personaInfo(id string) (string, *space.PersonaInfo, error) {
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

func (a *App) resolveAgentDMTargetSpace(source, explicit string) (*space.Space, *space.PersonaInfo, error) {
	pid, info, err := a.resolveAgentDMPersonaID(source, explicit)
	if err != nil {
		return nil, nil, err
	}
	seed := strings.TrimSpace(space.MapSource(source).Seed)
	if isSpaceID(seed) {
		sp, err := a.spaces.LoadSpace(seed)
		if err != nil || sp == nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrAgentDMSpaceNotFound, seed)
		}
		return sp, info, nil
	}
	sp, err := a.spaces.EnsureSpace(space.KindAgentDM, pid, *info)
	if err != nil {
		return nil, nil, err
	}
	return sp, info, nil
}

func (a *App) appendAgentDMUserToSpace(source, explicit, content string) (*space.Message, error) {
	sp, _, err := a.resolveAgentDMTargetSpace(source, explicit)
	if err != nil {
		return nil, err
	}
	m, err := a.spaces.AppendUserMessage(sp.ID, content, nil)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *App) appendAgentDMAssistantToSpace(source, explicit, content, reasoning string, mentions []string, parentID string) (*space.Message, error) {
	sp, info, err := a.resolveAgentDMTargetSpace(source, explicit)
	if err != nil {
		return nil, err
	}
	m, err := a.spaces.AppendAgentMessage(sp.ID, *info, content, reasoning, mentions, parentID)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
