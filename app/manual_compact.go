package app

import (
	"errors"
	"strings"

	"github.com/abcdlsj/sumi/space"
)

// ErrManualCompactSpaceBacked is returned when a user runs a manual compact
// command (/compact, !compact) on a source whose conversation is a persisted
// Space. Space-backed context is rebuilt deterministically from Space every
// round via ContextView.Apply, and overflow is folded automatically into a
// projection checkpoint. An in-place s.Compact here cannot anchor such a
// checkpoint: the next turn's projection identity (SpaceID, ParentMessageID,
// AgentID, Profile) is not knowable at compact time — the reply may land in a
// different thread or persona — so any checkpoint we wrote would be silently
// discarded by resolveCheckpoint on the very next turn, and a bare in-place
// compact is unconditionally overwritten by the next Apply. Rather than fake a
// mutation the projection will throw away, we refuse and explain.
var ErrManualCompactSpaceBacked = errors.New("manual compact is not supported for space-backed conversations: context is rebuilt from the space each turn and overflow is compacted automatically")

// manualCompactSpaceBacked reports whether source maps to a persisted Space, in
// which case a manual in-place compact must be refused (see
// ErrManualCompactSpaceBacked). A source that maps to no space, or to a space
// key that has not been persisted yet (in-memory Draft), is legacy Space-less
// and keeps the in-place compact behavior.
func (a *App) manualCompactSpaceBacked(source string) bool {
	if a == nil || a.spaces == nil {
		return false
	}
	target := space.MapSource(source)
	if target.Kind == "" || strings.TrimSpace(target.Seed) == "" {
		return false
	}
	if space.IsSpaceID(target.Seed) {
		sp, err := a.spaces.LoadSpace(target.Seed)
		return err == nil && sp != nil && sp.Kind == target.Kind
	}
	sp, err := a.spaces.Store().FindSpaceByKindAndKey(target.Kind, target.Seed)
	return err == nil && sp != nil
}
