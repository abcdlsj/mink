package bus

import "testing"

func TestBusPublishesToSubscriber(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe(1)
	defer cancel()

	b.Publish(Event{Type: TurnStarted, Source: "cli"})

	ev := <-ch
	if ev.Type != TurnStarted {
		t.Fatalf("got %q", ev.Type)
	}
	if ev.Source != "cli" {
		t.Fatalf("got source %q", ev.Source)
	}
}
