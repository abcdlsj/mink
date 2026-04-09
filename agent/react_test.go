package agent

import (
	"context"
	"testing"

	"github.com/abcdlsj/mink/bus"
)

func TestPublishThinkingFallback(t *testing.T) {
	eventBus := bus.New()
	ch := make(chan bus.Msg, 4)
	eventBus.Observe(ch)

	a := &Agent{
		id:     bus.AddrAgentMain,
		bus:    eventBus,
		stream: false,
	}

	a.publishThinkingFallback(context.Background(), bus.AddrPlatformCLI, "reasoning text")

	msg1 := <-ch
	msg2 := <-ch

	if msg1.Type != bus.TypeThinkingChunk {
		t.Fatalf("expected first msg %s, got %s", bus.TypeThinkingChunk, msg1.Type)
	}
	if msg2.Type != bus.TypeThinkingEnd {
		t.Fatalf("expected second msg %s, got %s", bus.TypeThinkingEnd, msg2.Type)
	}
	if msg1.Payload != "reasoning text" || msg2.Payload != "reasoning text" {
		t.Fatalf("unexpected payloads: %#v %#v", msg1.Payload, msg2.Payload)
	}
}

func TestPublishThinkingFallbackSkipsStreaming(t *testing.T) {
	eventBus := bus.New()
	ch := make(chan bus.Msg, 2)
	eventBus.Observe(ch)

	a := &Agent{
		id:     bus.AddrAgentMain,
		bus:    eventBus,
		stream: true,
	}

	a.publishThinkingFallback(context.Background(), bus.AddrPlatformCLI, "reasoning text")

	select {
	case msg := <-ch:
		t.Fatalf("expected no messages, got %#v", msg)
	default:
	}
}
