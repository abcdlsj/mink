package agent

import (
	"strings"
	"sync"
)

func (d *TeamDispatcher) BindSource(src, teamID, threadID string) {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(teamID) == "" || strings.TrimSpace(threadID) == "" {
		return
	}
	d.mu.Lock()
	d.bindings[src] = TeamBinding{TeamID: teamID, ThreadID: threadID}
	d.mu.Unlock()
}

func (d *TeamDispatcher) UnbindSource(src string) {
	if strings.TrimSpace(src) == "" {
		return
	}
	d.mu.Lock()
	delete(d.bindings, src)
	d.mu.Unlock()
}

func (d *TeamDispatcher) Binding(src string) (TeamBinding, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	binding, ok := d.bindings[src]
	return binding, ok
}

func (d *TeamDispatcher) Schedule(src, speakerAgentID, prompt string) {
	if strings.TrimSpace(src) == "" || strings.TrimSpace(speakerAgentID) == "" {
		return
	}
	d.mu.Lock()
	d.pending[src] = TeamHandoff{
		SpeakerAgentID: speakerAgentID,
		Prompt:         strings.TrimSpace(prompt),
	}
	d.mu.Unlock()
}

func (d *TeamDispatcher) Pending(src string) (TeamHandoff, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	handoff, ok := d.pending[src]
	return handoff, ok
}

func (d *TeamDispatcher) takePending(src string) (TeamHandoff, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	handoff, ok := d.pending[src]
	if ok {
		delete(d.pending, src)
	}
	return handoff, ok
}

func (d *TeamDispatcher) lockFor(teamID string) func() {
	d.mu.Lock()
	lock, ok := d.locks[teamID]
	if !ok {
		lock = &sync.Mutex{}
		d.locks[teamID] = lock
	}
	d.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func teamRuntimeSource(teamID, threadID string) string {
	return "team:" + teamID + ":" + threadID
}
