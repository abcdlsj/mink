package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

var (
	ErrNotAgentDMSource         = errors.New("agent_dm: source does not map to KindAgentDM")
	ErrAgentDMPersonaRequired   = errors.New("agent_dm: persona id required")
	ErrAgentDMPersonaConflict   = errors.New("agent_dm: source seed and explicit persona disagree")
	ErrAgentDMPersonaNotFound   = errors.New("agent_dm: persona not registered")
)

func (a *App) resolveAgentDMPersonaID(source, explicit string) (string, *space.PersonaInfo, error) {
	target := space.MapSource(source)
	if target.Kind != space.KindAgentDM {
		return "", nil, ErrNotAgentDMSource
	}
	seed := strings.TrimSpace(target.Seed)
	got := strings.TrimSpace(explicit)
	switch {
	case seed == "" && got == "":
		return "", nil, ErrAgentDMPersonaRequired
	case seed != "" && got != "" && seed != got:
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

func (a *App) appendAgentDMUserToSpace(source, explicitPersonaID, content string) (*space.Message, error) {
	personaID, info, err := a.resolveAgentDMPersonaID(source, explicitPersonaID)
	if err != nil {
		return nil, err
	}
	sp, err := a.spaces.EnsureSpace(space.KindAgentDM, personaID, *info)
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
	personaID, info, err := a.resolveAgentDMPersonaID(source, explicitPersonaID)
	if err != nil {
		return nil, err
	}
	sp, err := a.spaces.EnsureSpace(space.KindAgentDM, personaID, *info)
	if err != nil {
		return nil, err
	}
	written, err := a.spaces.AppendAgentMessage(sp.ID, *info, content, reasoning, mentions, parentMessageID)
	if err != nil {
		return nil, err
	}
	return &written, nil
}
