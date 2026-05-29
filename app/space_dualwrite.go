package app

import (
	"strings"

	"github.com/abcdlsj/sumi/msg"
	"github.com/abcdlsj/sumi/session"
	"github.com/abcdlsj/sumi/space"
)

// Spaces returns the space.Manager. Plugins / adapters may use it
// during P2 onward when readers begin to migrate.
func (a *App) Spaces() *space.Manager { return a.spaces }

// dualWriteUserInput mirrors a user-authored message into the new
// Space store. The session write is still authoritative during
// P1; this is a shadow.
//
// source is the inbound source string ("desktop", "cli", etc.).
// personaID is the persona the user explicitly addressed (composer
// drop-down or @-routing hint), if any. It is used to seed the
// agent_dm space; it does NOT change the user message's authorship.
//
// Errors are intentionally swallowed: dual-write must never break
// the primary session path.
func (a *App) dualWriteUserInput(source, personaID, content string) {
	if a == nil || a.spaces == nil {
		return
	}
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "subtask:") {
		return
	}
	target := space.MapSource(source)
	if target.Kind == "" {
		return
	}
	agent := a.personaInfoFor(personaID, target)
	if target.Kind == space.KindAgentDM && agent.ID == "" {
		// Agent DM source must carry an agent id. Fall back to the
		// seed (which is the agent id by construction).
		agent.ID = target.Seed
	}
	sp, err := a.spaces.EnsureSpace(target.Kind, target.Seed, agent)
	if err != nil {
		return
	}
	_, _ = a.spaces.AppendUserMessage(sp.ID, content, nil)
}

// dualWriteAssistantMessage mirrors an assistant-authored message
// into the new Space store. The persona id must be non-empty per
// Iris's hard rule; if the caller cannot identify the working agent
// the write is skipped (and tests can detect the gap).
func (a *App) dualWriteAssistantMessage(source, personaID string, m msg.Message) {
	if a == nil || a.spaces == nil {
		return
	}
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "subtask:") {
		return
	}
	if strings.TrimSpace(personaID) == "" {
		return
	}
	target := space.MapSource(source)
	if target.Kind == "" {
		return
	}
	info := a.personaInfoFor(personaID, target)
	if info.ID == "" {
		return
	}
	sp, err := a.spaces.EnsureSpace(target.Kind, target.Seed, info)
	if err != nil {
		return
	}
	_, _ = a.spaces.AppendAgentMessage(sp.ID, info, m.Content, m.Reasoning, nil, "")
}

// dualWriteSession is invoked once per turn after session.Save to
// shadow any new assistant messages produced during the turn. We
// scan from the tail and write every assistant entry that does not
// yet have a space twin. P2 will replace this with a per-event hook.
func (a *App) dualWriteAssistantsFromSession(source, personaID string, s *session.Session) {
	if a == nil || a.spaces == nil || s == nil {
		return
	}
	for i := len(s.Messages) - 1; i >= 0; i-- {
		m := s.Messages[i]
		if m.Role != "assistant" {
			continue
		}
		// Only mirror the last-assistant message per turn during P1.
		// Multi-assistant batches will be handled cleanly once routing
		// owns the timeline (P2).
		a.dualWriteAssistantMessage(source, personaID, m)
		return
	}
}

// personaInfoFor builds the PersonaInfo the manager seeding rules
// expect. For agent_dm we must always supply an agent; otherwise
// PersonaInfo may be zero.
func (a *App) personaInfoFor(personaID string, target space.SourceTarget) space.PersonaInfo {
	id := strings.TrimSpace(personaID)
	if id == "" && target.Kind == space.KindAgentDM {
		id = target.Seed
	}
	if id == "" {
		return space.PersonaInfo{}
	}
	if a.personas != nil {
		if p := a.personas.Get(id); p != nil {
			return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}
		}
	}
	return space.PersonaInfo{ID: id}
}
