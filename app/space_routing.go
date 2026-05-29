package app

import (
	"strings"

	"github.com/abcdlsj/sumi/space"
)

// channelRouter is the lazily-built Router used by inputFlow when
// a source maps to a channel space. It is constructed once per App
// and survives reloads of the persona registry — the snapshot
// closure asks the registry on every lookup.
func (a *App) channelRouter() *space.Router {
	if a == nil || a.spaces == nil {
		return nil
	}
	a.spaceRouterOnce.Do(func() {
		a.spaceRouter = space.NewRouter(a.spaces, func(id string) (space.PersonaInfo, bool) {
			id = strings.TrimSpace(id)
			if id == "" || a.personas == nil {
				return space.PersonaInfo{}, false
			}
			if p := a.personas.Get(id); p != nil {
				return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}, true
			}
			// Try lower-case fallback (display match path used by parser).
			lower := strings.ToLower(id)
			for _, p := range a.personas.List() {
				if strings.ToLower(p.Display) == lower || strings.ToLower(p.ID) == lower {
					return space.PersonaInfo{ID: p.ID, Display: p.Display, Role: p.Description}, true
				}
			}
			return space.PersonaInfo{}, false
		}, 4)
	})
	return a.spaceRouter
}

// sourceIsChannel returns true when a source string lands in a
// Space of kind channel. P2.5a only intercepts these; DM, direct
// chat, subtask sources continue through the legacy active-persona
// path.
func sourceIsChannel(source string) bool {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "subtask:") {
		return false
	}
	target := space.MapSource(source)
	return target.Kind == space.KindChannel
}

// channelInterceptResult captures everything the input flow needs
// to know after deferring channel handling to the router. wakes
// is the list of routing decisions; notices is the list of
// non-wake outcomes for the bus to surface (P2.5c will publish).
//
// In P2.5a wakes is computed but never executed; runtime invocation
// arrives in P2.5b.
type channelInterceptResult struct {
	spaceID string
	wakes   []space.RoutingTarget
	notices []space.RoutingNotice
}

// interceptChannelInput is the P2.5a entry point. It writes the
// user message to the Space via the router (which also handles
// atomic participant insertion) and returns the wake plan without
// running any agent runtime.
//
// Errors propagate: if the Space write fails, the user sees the
// failure rather than a half-applied turn.
func (a *App) interceptChannelInput(source, content string) (*channelInterceptResult, error) {
	r := a.channelRouter()
	if r == nil {
		return nil, nil
	}
	target := space.MapSource(source)
	if target.Kind != space.KindChannel {
		return nil, nil
	}
	sp, err := a.spaces.EnsureSpace(target.Kind, target.Seed, space.PersonaInfo{})
	if err != nil {
		return nil, err
	}
	wakes, notices, err := r.RouteUserChannelMessage(sp.ID, content)
	if err != nil {
		return nil, err
	}
	return &channelInterceptResult{
		spaceID: sp.ID,
		wakes:   wakes,
		notices: notices,
	}, nil
}
