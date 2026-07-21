package driver

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrQueueFull = errors.New("driver command queue is full")
	ErrClosed    = errors.New("driver owner is closed")
)

type request struct {
	command Command
	result  chan response
}

type response struct {
	result TurnResult
	err    error
}

type Owner struct {
	engine Engine
	queue  chan request
	events chan Event
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	closed bool
	once   sync.Once
}

func NewOwner(engine Engine, queueSize int) (*Owner, error) {
	if engine == nil {
		return nil, errors.New("driver engine is required")
	}
	if queueSize < 1 {
		return nil, errors.New("driver queue size must be positive")
	}
	ctx, cancel := context.WithCancel(context.Background())
	owner := &Owner{
		engine: engine,
		queue:  make(chan request, queueSize),
		events: make(chan Event, queueSize),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go owner.run()
	return owner, nil
}

func (o *Owner) Submit(ctx context.Context, command Command) (TurnResult, error) {
	if ctx == nil {
		return TurnResult{}, errors.New("submit context is required")
	}
	if err := command.validate(); err != nil {
		return TurnResult{}, err
	}
	request := request{command: command, result: make(chan response, 1)}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return TurnResult{}, ErrClosed
	}
	select {
	case o.queue <- request:
		o.mu.Unlock()
	case <-ctx.Done():
		o.mu.Unlock()
		return TurnResult{}, ctx.Err()
	default:
		o.mu.Unlock()
		return TurnResult{}, ErrQueueFull
	}
	select {
	case result := <-request.result:
		return result.result, result.err
	case <-ctx.Done():
		return TurnResult{}, ctx.Err()
	}
}

func (o *Owner) Events() <-chan Event {
	return o.events
}

func (o *Owner) Close() error {
	o.once.Do(func() {
		o.mu.Lock()
		o.closed = true
		o.mu.Unlock()
		o.cancel()
	})
	<-o.done
	return nil
}

func (o *Owner) run() {
	defer close(o.done)
	defer close(o.events)
	var sequence uint64
	sink := eventSink{publish: func(event Event) error {
		if err := event.validate(); err != nil {
			return err
		}
		sequence++
		event.Sequence = sequence
		select {
		case o.events <- event:
		default:
		}
		return nil
	}}
	for {
		select {
		case <-o.ctx.Done():
			o.failQueued(o.ctx.Err())
			return
		case request := <-o.queue:
			result, err := o.engine.Execute(o.ctx, request.command, sink)
			request.result <- response{result: result, err: err}
		}
	}
}

func (o *Owner) failQueued(err error) {
	for {
		select {
		case request := <-o.queue:
			request.result <- response{err: err}
		default:
			return
		}
	}
}

type eventSink struct {
	publish func(Event) error
}

func (s eventSink) Publish(event Event) error {
	return s.publish(event)
}
