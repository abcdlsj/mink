package desktop

import (
	"testing"
	"time"

	"github.com/abcdlsj/sumi/delivery"
	"github.com/abcdlsj/sumi/persona"
	"github.com/abcdlsj/sumi/space"
	taskpkg "github.com/abcdlsj/sumi/task"
)

func TestRetryMessageRequeuesAsyncDelivery(t *testing.T) {
	b, a := newBackendWithApp(t)

	if _, err := a.Personas().Create("bob", persona.Meta{Runtime: "stub"}, "# Bob"); err != nil {
		t.Fatal(err)
	}
	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	trig, err := a.Spaces().AppendUserMessage(ch.ID, "please delegate this", nil)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := a.Tasks().Create(taskpkg.CreateTaskInput{
		SpaceID:          ch.ID,
		TriggerMessageID: trig.ID,
		InitiatorID:      "user",
		WorkerID:         "bob",
		Title:            "subtask",
		Source:           "desktop:channel:work",
		ExecutionIntent:  &taskpkg.ExecutionIntent{Input: "do the thing", Runtime: "stub", ShareContext: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := a.EnqueueAsyncDelegate(tk)
	if err != nil {
		t.Fatal(err)
	}

	deliveries := a.Deliveries()
	_, fence, err := deliveries.ClaimNextInLane(d.SpaceID, d.ParentMessageID, d.AgentID, "owner-A", time.Now(), 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	ph, _, err := a.Spaces().EnsureDeliveryPlaceholder(d.SpaceID, d.ID, d.AgentID, d.ParentMessageID, nil)
	if err != nil {
		t.Fatal(err)
	}
	bound, _, err := deliveries.BindResultMessage(d.ID, fence, ph.ID, time.Now())
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := a.Spaces().FailDeliveryMessage(d.SpaceID, bound.ResultMessageID, d.ID, fence.OwnerID, fence.Version, time.Now(), "model exploded"); err != nil {
		t.Fatalf("fail message: %v", err)
	}
	if _, err := deliveries.Fail(d.ID, fence, "model exploded", time.Now()); err != nil {
		t.Fatalf("fail delivery: %v", err)
	}

	if _, err := b.RetryMessage(RetryMessageRequest{SpaceID: ch.ID, MessageID: ph.ID}); err != nil {
		t.Fatalf("RetryMessage: %v", err)
	}

	got, err := deliveries.Get(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != delivery.StatusPending {
		t.Fatalf("delivery status after retry = %q, want pending (same delivery requeued)", got.Status)
	}
	if got.ResultMessageID != ph.ID {
		t.Fatalf("delivery ResultMessageID = %q, want %q preserved", got.ResultMessageID, ph.ID)
	}
	sp, err := a.Spaces().LoadSpace(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	var replay *space.Message
	count := 0
	for i := range sp.Messages {
		if sp.Messages[i].DeliveryID == d.ID {
			replay = &sp.Messages[i]
			count++
		}
	}
	if count != 1 {
		t.Fatalf("placeholders for delivery = %d, want exactly 1 reused", count)
	}
	if replay.Status != "pending" {
		t.Fatalf("placeholder status after retry = %q, want pending", replay.Status)
	}
	if replay.Error != "" {
		t.Fatalf("placeholder error after retry = %q, want cleared", replay.Error)
	}
}

func TestRetryMessageNonAsyncUnaffected(t *testing.T) {
	b, a := newBackendWithApp(t)

	ch, err := a.Spaces().EnsureSpace(space.KindChannel, "work", space.PersonaInfo{})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := a.Spaces().AppendAgentMessage(ch.ID, space.PersonaInfo{ID: "assistant", Display: "assistant"}, "failed reply", "", nil, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Spaces().UpdateMessage(ch.ID, orphan.ID, func(m *space.Message) {
		m.Status = "failed"
		m.Error = "boom"
	}); err != nil {
		t.Fatal(err)
	}

	_, err = b.RetryMessage(RetryMessageRequest{SpaceID: ch.ID, MessageID: orphan.ID})
	if err == nil {
		t.Fatalf("RetryMessage on non-async failed reply with no user parent returned nil error; want the unchanged non-async failure path")
	}
}
