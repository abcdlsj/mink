package desktop

import "sync"

type fanout struct {
	mu   sync.Mutex
	next int
	subs map[int]chan BusEvent
}

func newFanout() *fanout {
	return &fanout{subs: map[int]chan BusEvent{}}
}

func (f *fanout) subscribe(size int) (<-chan BusEvent, func()) {
	if size <= 0 {
		size = 64
	}
	ch := make(chan BusEvent, size)
	f.mu.Lock()
	id := f.next
	f.next++
	f.subs[id] = ch
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
		close(ch)
	}
}

func (f *fanout) publish(ev BusEvent) {
	f.mu.Lock()
	for _, ch := range f.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	f.mu.Unlock()
}
