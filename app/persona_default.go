package app

import (
	"strings"

	"github.com/abcdlsj/sumi/persona"
)

func (a *App) defaultPersona() *persona.Persona {
	if a == nil || a.personas == nil {
		return nil
	}
	configured := strings.TrimSpace(a.cfg.DefaultPersona)
	if configured != "" {
		if p := a.personas.Get(configured); p != nil {
			return p
		}
	}
	list := a.personas.List()
	if len(list) == 0 {
		return nil
	}
	return list[0]
}
