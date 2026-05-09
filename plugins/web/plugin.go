package web

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/sumi/app"
	"github.com/abcdlsj/sumi/bus"
)

const source = "web"

func Plugin() app.Plugin {
	return func(a *app.App) error {
		a.RegisterEntrypoint("web", run)
		return nil
	}
}

func run(ctx context.Context, a *app.App, args []string) error {
	addr := a.Config().WebAddr
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.StringVar(&addr, "addr", addr, "web bind address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s := newServer(a, addr)
	return s.Run(ctx)
}

type server struct {
	app    *app.App
	addr   string
	notice string

	mu   sync.Mutex
	next int
	subs map[int]chan struct{}
}

type state struct {
	Workspace string        `json:"workspace"`
	Model     string        `json:"model"`
	Notice    string        `json:"notice,omitempty"`
	Queued    int           `json:"queued,omitempty"`
	Sessions  []sessionItem `json:"sessions"`
	Current   currentState  `json:"current"`
	Messages  []message     `json:"messages"`
}

type sessionItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	Active  bool   `json:"active"`
}

type currentState struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Source  string `json:"source"`
}

type message struct {
	Role        string       `json:"role"`
	Content     string       `json:"content,omitempty"`
	Reasoning   string       `json:"reasoning,omitempty"`
	Time        string       `json:"time,omitempty"`
	ToolCalls   []toolCall   `json:"tool_calls,omitempty"`
	ToolResults []toolResult `json:"tool_results,omitempty"`
}

type toolCall struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

type toolResult struct {
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

func newServer(a *app.App, addr string) *server {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:7788"
	}
	return &server{
		app:  a,
		addr: addr,
		subs: map[int]chan struct{}{},
	}
}

func (s *server) Run(ctx context.Context) error {
	events, cancel := s.app.Bus().Subscribe(256)
	defer cancel()
	go s.watch(ctx, events)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/select", s.handleSelect)
	mux.HandleFunc("/api/message", s.handleMessage)
	mux.HandleFunc("/api/action", s.handleAction)

	srv := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	fmt.Printf("web ui listening on http://%s\n", s.addr)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *server) watch(ctx context.Context, events <-chan bus.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Source != source {
				continue
			}
			if ev.Type == bus.ServiceNotice {
				s.mu.Lock()
				s.notice = strings.TrimSpace(ev.Text)
				s.mu.Unlock()
			}
			s.notify()
		}
	}
}

func (s *server) subscribe() (int, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	ch := make(chan struct{}, 1)
	s.subs[id] = ch
	return id, ch
}

func (s *server) unsubscribe(id int) {
	s.mu.Lock()
	ch := s.subs[id]
	delete(s.subs, id)
	s.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (s *server) notify() {
	s.mu.Lock()
	subs := make([]chan struct{}, 0, len(s.subs))
	for _, ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
