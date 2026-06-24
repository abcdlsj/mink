package app

import (
	"strings"

	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
)

type FuzzyResolver struct {
	Personas *persona.Registry
}

func (r FuzzyResolver) Resolve(id string) (space.PersonaInfo, bool) {
	id = strings.TrimSpace(id)
	if id == "" || r.Personas == nil {
		return space.PersonaInfo{}, false
	}
	if p := r.Personas.Get(id); p != nil {
		return personaInfoFromPersona(p), true
	}
	lower := strings.ToLower(id)
	for _, p := range r.Personas.List() {
		if strings.ToLower(p.Display) == lower || strings.ToLower(p.ID) == lower {
			return personaInfoFromPersona(p), true
		}
	}
	return space.PersonaInfo{}, false
}

func (r FuzzyResolver) Info(id string) space.PersonaInfo {
	if info, ok := r.Resolve(id); ok {
		return info
	}
	return space.PersonaInfo{ID: strings.TrimSpace(id)}
}

func (a *App) fuzzyPersonaResolver() FuzzyResolver {
	if a == nil {
		return FuzzyResolver{}
	}
	return FuzzyResolver{Personas: a.personas}
}

func personaInfoFromPersona(p *persona.Persona) space.PersonaInfo {
	if p == nil {
		return space.PersonaInfo{}
	}
	return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
}
