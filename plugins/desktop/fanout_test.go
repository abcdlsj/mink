package desktop

import (
	"sync"
	"testing"
	"time"
)

func TestFanoutPublishesToSubscribers(t *testing.T) {
	f := newFanout()
	a, cancelA := f.subscribe(4)
	b, cancelB := f.subscribe(4)
	defer cancelA()
	defer cancelB()

	ev := BusEvent{Type: "turn.chunk", Text: "hi"}
	f.publish(ev)

	select {
	case got := <-a:
		if got.Text != "hi" {
			t.Fatalf("a: want hi, got %q", got.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("a: timeout")
	}
	select {
	case got := <-b:
		if got.Text != "hi" {
			t.Fatalf("b: want hi, got %q", got.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("b: timeout")
	}
}

func TestFanoutCancelStopsDelivery(t *testing.T) {
	f := newFanout()
	ch, cancel := f.subscribe(2)
	cancel()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed")
	}
	f.publish(BusEvent{Type: "turn.chunk"})
}

func TestFanoutDropsWhenFull(t *testing.T) {
	f := newFanout()
	ch, cancel := f.subscribe(1)
	defer cancel()
	for i := 0; i < 5; i++ {
		f.publish(BusEvent{Type: "turn.chunk"})
	}
	select {
	case <-ch:
	default:
		t.Fatal("expected at least one event")
	}
}

func TestFanoutConcurrentPublishSubscribe(t *testing.T) {
	f := newFanout()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := f.subscribe(8)
			defer cancel()
			select {
			case <-ch:
			case <-time.After(time.Second):
			}
		}()
	}
	for i := 0; i < 32; i++ {
		f.publish(BusEvent{Type: "turn.chunk"})
	}
	wg.Wait()
}
