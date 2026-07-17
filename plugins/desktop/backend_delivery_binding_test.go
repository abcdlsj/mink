package desktop

import (
	"strings"
	"testing"

	"github.com/abcdlsj/sumi/bus"
	"github.com/abcdlsj/sumi/space"
)

// TestTrackTurnEventBindsToWorkerPlaceholder is the Desktop half of Iris review
// point ① / ③. On the durable worker path, the worker appends exactly ONE
// pending assistant placeholder per DeliveryID and stamps every turn event with
// that DeliveryID plus the placeholder's stable MessageID. The Desktop backend
// consumes the same bus. It MUST bind to that placeholder (locate by DeliveryID)
// rather than append a second pending message, and it MUST NOT delete the
// placeholder on TurnFinished — the worker owns the placeholder's terminal state
// through the Delivery. Empty DeliveryID keeps the original direct projection.
func TestTrackTurnEventBindsToWorkerPlaceholder(t *testing.T) {
	b, a := newBackendWithApp(t)

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}

	// The worker's placeholder: one pending assistant message carrying the
	// DeliveryID, exactly as EnsureDeliveryPlaceholder writes it.
	const deliveryID = "dlv-bind-1"
	placeholder, _, err := a.Spaces().EnsureDeliveryPlaceholder(ch.ID, deliveryID, "bob", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	stream := "stream-bind-1"
	// The worker stamps DeliveryID + the placeholder MessageID onto every event.
	b.trackTurnEvent(bus.Event{
		Type:       bus.TurnStarted,
		SpaceID:    ch.ID,
		StreamID:   stream,
		AgentID:    "bob",
		DeliveryID: deliveryID,
		MessageID:  placeholder.ID,
	})
	b.trackTurnEvent(bus.Event{
		Type:       bus.TurnChunk,
		SpaceID:    ch.ID,
		StreamID:   stream,
		AgentID:    "bob",
		DeliveryID: deliveryID,
		MessageID:  placeholder.ID,
		Text:       "hello from worker",
	})
	b.trackTurnEvent(bus.Event{
		Type:       bus.TurnFinished,
		SpaceID:    ch.ID,
		StreamID:   stream,
		AgentID:    "bob",
		DeliveryID: deliveryID,
		MessageID:  placeholder.ID,
	})

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Exactly one message must carry this DeliveryID — the worker's placeholder.
	// A second appended pending message (ignoring DeliveryID) is the dual-write
	// bug this guards against.
	var withDelivery, agentMsgs int
	var bound *space.Message
	for i := range sp.Messages {
		if sp.Messages[i].DeliveryID == deliveryID {
			withDelivery++
			bound = &sp.Messages[i]
		}
		if sp.Messages[i].AuthorKind == space.ParticipantAgent {
			agentMsgs++
		}
	}
	if withDelivery != 1 {
		t.Fatalf("messages carrying delivery %q = %d, want exactly 1 (bind, no dual-write)", deliveryID, withDelivery)
	}
	if agentMsgs != 1 {
		t.Fatalf("agent messages = %d, want exactly 1 (no second appended placeholder)", agentMsgs)
	}
	// no-delete on TurnFinished: the placeholder must still exist, and the worker
	// (not the desktop tracker) owns finalizing its content/status.
	if bound == nil {
		t.Fatalf("worker placeholder was deleted on TurnFinished, want it preserved for the worker to finalize")
	}
	if bound.ID != placeholder.ID {
		t.Fatalf("bound message id = %q, want the worker placeholder %q", bound.ID, placeholder.ID)
	}
	// The streamed chunk must land ON the worker placeholder (proving the tracker
	// bound to it), not on a throwaway message that gets deleted — otherwise the
	// user sees streaming in a duplicate bubble that then vanishes.
	if !strings.Contains(bound.Content, "hello from worker") {
		t.Fatalf("bound placeholder content = %q, want the streamed chunk (chunk went to a throwaway)", bound.Content)
	}
}

// TestTrackTurnErrorBindsToWorkerPlaceholder guards the failure half: a TurnError
// on the durable worker path must mark the worker's own placeholder failed in
// place (located by DeliveryID), never append a separate failed message.
func TestTrackTurnErrorBindsToWorkerPlaceholder(t *testing.T) {
	b, a := newBackendWithApp(t)

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}

	const deliveryID = "dlv-bind-err"
	placeholder, _, err := a.Spaces().EnsureDeliveryPlaceholder(ch.ID, deliveryID, "bob", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	stream := "stream-bind-err"
	b.trackTurnEvent(bus.Event{
		Type:       bus.TurnStarted,
		SpaceID:    ch.ID,
		StreamID:   stream,
		AgentID:    "bob",
		DeliveryID: deliveryID,
		MessageID:  placeholder.ID,
	})
	b.trackTurnEvent(bus.Event{
		Type:       bus.TurnError,
		SpaceID:    ch.ID,
		StreamID:   stream,
		AgentID:    "bob",
		DeliveryID: deliveryID,
		MessageID:  placeholder.ID,
		Err:        "boom: worker turn failed",
	})

	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}

	var withDelivery, agentMsgs int
	var bound *space.Message
	for i := range sp.Messages {
		if sp.Messages[i].DeliveryID == deliveryID {
			withDelivery++
			bound = &sp.Messages[i]
		}
		if sp.Messages[i].AuthorKind == space.ParticipantAgent {
			agentMsgs++
		}
	}
	if withDelivery != 1 {
		t.Fatalf("messages carrying delivery %q = %d, want exactly 1 (bind, no separate failed append)", deliveryID, withDelivery)
	}
	if agentMsgs != 1 {
		t.Fatalf("agent messages = %d, want exactly 1", agentMsgs)
	}
	if bound == nil {
		t.Fatalf("worker placeholder missing after TurnError")
	}
	if bound.Status != "failed" {
		t.Fatalf("bound placeholder status = %q, want failed", bound.Status)
	}
	if !strings.Contains(bound.Error, "boom") {
		t.Fatalf("bound placeholder error = %q, want the worker failure text", bound.Error)
	}
}
