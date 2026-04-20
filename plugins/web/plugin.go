package web

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abcdlsj/mink/app"
	"github.com/abcdlsj/mink/bus"
	"github.com/abcdlsj/mink/msg"
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

func (s *server) state() (state, error) {
	sessions, err := s.app.ListSessions()
	if err != nil {
		return state{}, err
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	current, err := s.app.CurrentSession(source)
	if err != nil {
		return state{}, err
	}
	items := make([]sessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, sessionItem{
			ID:      sess.ID,
			Title:   blank(sess.Title, "(untitled)"),
			Updated: sess.UpdatedAt.Format("2006-01-02 15:04"),
			Active:  current != nil && sess.ID == current.ID,
		})
	}
	msgs := []message{}
	if current != nil {
		for _, m := range current.Messages {
			msgs = append(msgs, renderMessage(m))
		}
	}
	cur := currentState{Title: "(untitled)"}
	if current != nil {
		cur = currentState{
			ID:      current.ID,
			Title:   blank(current.Title, "(untitled)"),
			Summary: current.Summary,
			Source:  current.Source,
		}
	}
	s.mu.Lock()
	notice := s.notice
	s.mu.Unlock()
	return state{
		Workspace: s.app.Workspace(),
		Model:     s.app.CurrentModel(),
		Notice:    notice,
		Sessions:  items,
		Current:   cur,
		Messages:  msgs,
	}, nil
}

func renderMessage(m msg.Message) message {
	out := message{
		Role:      m.Role,
		Content:   m.Content,
		Reasoning: m.Reasoning,
		Time:      m.Timestamp.Format("15:04:05"),
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]toolCall, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			calls = append(calls, toolCall{
				Name: tc.Name,
				Args: strings.TrimSpace(string(tc.Args)),
			})
		}
		out.ToolCalls = calls
	}
	if len(m.ToolResults) > 0 {
		results := make([]toolResult, 0, len(m.ToolResults))
		for _, tr := range m.ToolResults {
			results = append(results, toolResult{
				Content: tr.Content,
				Error:   tr.Error,
			})
		}
		out.ToolResults = results
	}
	return out
}

func (s *server) handleIndex(rw http.ResponseWriter, _ *http.Request) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(indexHTML))
}

func (s *server) handleState(rw http.ResponseWriter, _ *http.Request) {
	state, err := s.state()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, state)
}

func (s *server) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id, ch := s.subscribe()
	defer s.unsubscribe(id)
	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	fmt.Fprint(rw, "event: state\ndata: refresh\n\n")
	flusher.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case <-ch:
			fmt.Fprint(rw, "event: state\ndata: refresh\n\n")
			flusher.Flush()
		}
	}
}

func (s *server) handleSelect(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.app.SwitchSession(source, in.ID); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	s.notify()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (s *server) handleMessage(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		http.Error(rw, "text is required", http.StatusBadRequest)
		return
	}
	go func() {
		if _, err := s.app.HandleInput(context.Background(), source, text); err != nil {
			s.app.PublishNotice(source, err.Error())
		}
	}()
	s.notify()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (s *server) handleAction(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	switch strings.TrimSpace(in.Name) {
	case "new_session":
		if _, err := s.app.NewSession(source); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.Error(rw, "unknown action", http.StatusBadRequest)
		return
	}
	s.notify()
	writeJSON(rw, map[string]bool{"ok": true})
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

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(rw).Encode(v)
}

func blank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

var indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Mink</title>
  <style>
    :root { color-scheme: light; --bg:#f3efe5; --panel:#fffdf8; --line:#d9cfbf; --ink:#201a14; --muted:#6f6558; --accent:#c45b2d; }
    * { box-sizing:border-box; }
    body { margin:0; font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace; color:var(--ink); background:linear-gradient(180deg,#efe4d0,#f7f3eb); }
    .app { display:grid; grid-template-columns:280px 1fr; min-height:100vh; }
    aside, main { padding:20px; }
    aside { border-right:1px solid var(--line); background:rgba(255,253,248,.7); }
    .brand { margin-bottom:20px; }
    .brand h1 { margin:0; font-size:24px; }
    .brand p { margin:4px 0 0; color:var(--muted); }
    button, textarea { font:inherit; }
    button { border:1px solid var(--line); background:var(--panel); padding:8px 12px; cursor:pointer; }
    button.primary { background:var(--accent); color:white; border-color:var(--accent); }
    .sessions { display:flex; flex-direction:column; gap:8px; margin-top:16px; }
    .session { border:1px solid var(--line); background:var(--panel); padding:10px; cursor:pointer; }
    .session.active { border-color:var(--accent); box-shadow:0 0 0 1px var(--accent) inset; }
    .session .meta { color:var(--muted); font-size:12px; }
    main { display:grid; grid-template-rows:auto 1fr auto; gap:16px; }
    .top { display:flex; justify-content:space-between; gap:16px; align-items:flex-end; }
    .title { font-size:28px; margin:0; }
    .meta { color:var(--muted); }
    .notice { padding:10px 12px; background:#fff4dc; border:1px solid #efc17c; }
    .messages { border:1px solid var(--line); background:rgba(255,253,248,.86); padding:16px; overflow:auto; min-height:50vh; }
    .msg { padding:10px 0; border-bottom:1px dashed var(--line); white-space:pre-wrap; }
    .msg:last-child { border-bottom:none; }
    .msg .role { color:var(--accent); }
    .msg .time { color:var(--muted); font-size:12px; }
    .tools { margin-top:8px; color:var(--muted); font-size:12px; }
    .composer { display:grid; gap:8px; }
    textarea { width:100%; min-height:120px; padding:12px; resize:vertical; border:1px solid var(--line); background:var(--panel); }
    @media (max-width: 900px) { .app { grid-template-columns:1fr; } aside { border-right:none; border-bottom:1px solid var(--line); } }
  </style>
</head>
<body>
  <div class="app">
    <aside>
      <div class="brand">
        <h1>Mink</h1>
        <p id="workspace"></p>
      </div>
      <button class="primary" id="new-session">New Session</button>
      <div class="sessions" id="sessions"></div>
    </aside>
    <main>
      <div>
        <div class="top">
          <div>
            <h2 class="title" id="title">Session</h2>
            <div class="meta" id="model"></div>
          </div>
        </div>
        <div id="notice"></div>
      </div>
      <div class="messages" id="messages"></div>
      <div class="composer">
        <textarea id="text" placeholder="Send a message"></textarea>
        <div><button class="primary" id="send">Send</button></div>
      </div>
    </main>
  </div>
  <script>
    const qs = s => document.querySelector(s)
    async function load() {
      const res = await fetch('/api/state')
      const st = await res.json()
      qs('#workspace').textContent = st.workspace
      qs('#title').textContent = st.current && st.current.title ? st.current.title : 'Session'
      qs('#model').textContent = st.model
      qs('#notice').innerHTML = st.notice ? '<div class="notice">'+escapeHtml(st.notice)+'</div>' : ''
      qs('#sessions').innerHTML = (st.sessions || []).map(s =>
        '<button class="session'+(s.active?' active':'')+'" data-id="'+s.id+'"><div>'+escapeHtml(s.title)+'</div><div class="meta">'+escapeHtml(s.updated)+'</div></button>'
      ).join('')
      qs('#messages').innerHTML = (st.messages || []).map(m => renderMessage(m)).join('') || '<div class="meta">No messages yet.</div>'
      qs('#messages').scrollTop = qs('#messages').scrollHeight
      document.querySelectorAll('.session').forEach(el => el.onclick = async () => {
        await fetch('/api/select', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({id:el.dataset.id})})
        load()
      })
    }
    function renderMessage(m) {
      const tools = []
      ;(m.tool_calls || []).forEach(t => tools.push('call: '+escapeHtml(t.name + (t.args ? ' ' + t.args : ''))))
      ;(m.tool_results || []).forEach(t => tools.push('result: '+escapeHtml(t.error || t.content || '')))
      return '<div class="msg"><div><span class="role">'+escapeHtml(m.role)+'</span> <span class="time">'+escapeHtml(m.time || '')+'</span></div>'
        +(m.content ? '<div>'+escapeHtml(m.content)+'</div>' : '')
        +(m.reasoning ? '<div class="meta">'+escapeHtml(m.reasoning)+'</div>' : '')
        +(tools.length ? '<div class="tools">'+tools.join('<br>')+'</div>' : '')
        +'</div>'
    }
    function escapeHtml(s) {
      const d = document.createElement('div')
      d.innerText = s || ''
      return d.innerHTML
    }
    qs('#send').onclick = async () => {
      const text = qs('#text').value.trim()
      if (!text) return
      qs('#text').value = ''
      await fetch('/api/message', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({text})})
      load()
    }
    qs('#new-session').onclick = async () => {
      await fetch('/api/action', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name:'new_session'})})
      load()
    }
    new EventSource('/api/events').addEventListener('state', load)
    load()
  </script>
</body>
</html>`
