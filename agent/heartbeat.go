package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/abcdlsj/mink/bus"
	robcron "github.com/robfig/cron/v3"
)

const minHeartbeatInterval = 5 * time.Minute

type HeartbeatManager struct {
	registry *Registry
	bus      *bus.Bus
	cron     *robcron.Cron
	mu       sync.Mutex
}

func NewHeartbeatManager(reg *Registry, b *bus.Bus) *HeartbeatManager {
	return &HeartbeatManager{
		registry: reg,
		bus:      b,
	}
}

func (h *HeartbeatManager) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.cron = robcron.New()

	for _, state := range h.registry.All() {
		hb := state.Descriptor.Heartbeat
		if hb == nil || hb.Schedule == "" || hb.Prompt == "" {
			continue
		}
		agentID := state.Descriptor.ID
		prompt := hb.Prompt

		_, err := h.cron.AddFunc(hb.Schedule, func() {
			h.fireHeartbeat(agentID, prompt)
		})
		if err != nil {
			fmt.Printf("heartbeat: invalid schedule for %s: %v\n", agentID, err)
		}
	}

	h.cron.Start()
	go func() {
		<-ctx.Done()
		h.cron.Stop()
	}()
	return nil
}

func (h *HeartbeatManager) fireHeartbeat(agentID, prompt string) {
	if h.registry == nil {
		return
	}
	state := h.registry.Get(agentID)
	if state == nil || state.Status == StatusOffline {
		return
	}

	src := fmt.Sprintf("heartbeat:%s", agentID)
	_ = h.bus.Pub(bus.Msg{
		Type:    bus.TypeCronTrigger,
		From:    src,
		To:      agentID,
		Payload: prompt,
	})
}

func (h *HeartbeatManager) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cron != nil {
		h.cron.Stop()
	}
}
