package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type WebNavItem struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
}

type WebIndexItem struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Label   string `json:"label"`
	Meta    string `json:"meta,omitempty"`
	Active  bool   `json:"active"`
}

type WebIndexGroup struct {
	Title string         `json:"title"`
	Items []WebIndexItem `json:"items"`
}

type WebMessage struct {
	Role        string          `json:"role"`
	Sender      string          `json:"sender"`
	Descriptor  string          `json:"descriptor,omitempty"`
	Time        string          `json:"time,omitempty"`
	Content     string          `json:"content,omitempty"`
	Reasoning   string          `json:"reasoning,omitempty"`
	ToolCalls   []WebToolCall   `json:"toolCalls,omitempty"`
	ToolResults []WebToolResult `json:"toolResults,omitempty"`
}

type WebToolCall struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

type WebToolResult struct {
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

type WebCard struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Meta     string `json:"meta,omitempty"`
}

type WebContextBlock struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type WebState struct {
	Workspace           string            `json:"workspace"`
	Section             string            `json:"section"`
	Nav                 []WebNavItem      `json:"nav"`
	IndexTitle          string            `json:"indexTitle"`
	IndexGroups         []WebIndexGroup   `json:"indexGroups"`
	IndexAction         string            `json:"indexAction,omitempty"`
	IndexActionLabel    string            `json:"indexActionLabel,omitempty"`
	HeaderTitle         string            `json:"headerTitle"`
	HeaderSubtitle      string            `json:"headerSubtitle,omitempty"`
	HeaderMeta          []string          `json:"headerMeta,omitempty"`
	Messages            []WebMessage      `json:"messages,omitempty"`
	Cards               []WebCard         `json:"cards,omitempty"`
	ContextTitle        string            `json:"contextTitle,omitempty"`
	ContextBlocks       []WebContextBlock `json:"contextBlocks,omitempty"`
	ComposerLabel       string            `json:"composerLabel"`
	ComposerPlaceholder string            `json:"composerPlaceholder"`
	ComposerDisabled    bool              `json:"composerDisabled"`
	EmptyHint           string            `json:"emptyHint,omitempty"`
}

type WebCallbacks struct {
	State       func() (WebState, error)
	Select      func(section, id string) error
	SendMessage func(text string) error
	NewSession  func() error
}

type Web struct {
	addr      string
	staticDir string
	cb        WebCallbacks
	server    *http.Server
	mu        sync.Mutex

	subMu       sync.Mutex
	nextSubID   int
	subscribers map[int]chan struct{}
}

func NewWeb(addr string, cb WebCallbacks) *Web {
	if strings.TrimSpace(addr) == "" {
		addr = "127.0.0.1:7788"
	}
	return &Web{
		addr:        addr,
		cb:          cb,
		subscribers: make(map[int]chan struct{}),
	}
}

func (w *Web) SetStaticDir(dir string) { w.staticDir = dir }

func (w *Web) ID() string { return "web" }

func (w *Web) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.server != nil {
		return nil
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/state", w.handleState)
	mux.HandleFunc("/api/events", w.handleEvents)
	mux.HandleFunc("/api/select", w.handleSelect)
	mux.HandleFunc("/api/message", w.handleMessage)
	mux.HandleFunc("/api/action", w.handleAction)

	if w.staticDir != "" {
		staticFS := os.DirFS(w.staticDir)
		fileServer := http.FileServer(http.FS(staticFS))
		mux.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
			path := strings.TrimPrefix(req.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticFS, path); err != nil {
				path = "index.html"
			}
			req.URL.Path = "/" + path
			fileServer.ServeHTTP(rw, req)
		})
	} else {
		mux.HandleFunc("/", w.handleIndex)
	}

	srv := &http.Server{
		Addr:              w.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	w.server = srv

	go func() {
		<-ctx.Done()
		_ = w.Stop()
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("web server error: %v\n", err)
		}
	}()

	fmt.Printf("Web UI listening on http://%s\n", w.addr)
	return nil
}

func (w *Web) Stop() error {
	w.mu.Lock()
	srv := w.server
	w.server = nil
	w.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func (w *Web) NotifyStateChanged() {
	w.subMu.Lock()
	subs := make([]chan struct{}, 0, len(w.subscribers))
	for _, ch := range w.subscribers {
		subs = append(subs, ch)
	}
	w.subMu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (w *Web) subscribe() (int, chan struct{}) {
	w.subMu.Lock()
	defer w.subMu.Unlock()
	id := w.nextSubID
	w.nextSubID++
	ch := make(chan struct{}, 1)
	w.subscribers[id] = ch
	return id, ch
}

func (w *Web) unsubscribe(id int) {
	w.subMu.Lock()
	ch, ok := w.subscribers[id]
	if ok {
		delete(w.subscribers, id)
		close(ch)
	}
	w.subMu.Unlock()
}

func (w *Web) handleIndex(rw http.ResponseWriter, _ *http.Request) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(webPage))
}

func (w *Web) handleState(rw http.ResponseWriter, _ *http.Request) {
	if w.cb.State == nil {
		http.Error(rw, "state unavailable", http.StatusServiceUnavailable)
		return
	}
	state, err := w.cb.State()
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(rw, state)
}

func (w *Web) handleEvents(rw http.ResponseWriter, req *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")
	rw.Header().Set("X-Accel-Buffering", "no")

	id, ch := w.subscribe()
	defer w.unsubscribe(id)

	fmt.Fprint(rw, "event: state\ndata: ready\n\n")
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-keepAlive.C:
			fmt.Fprint(rw, ": ping\n\n")
			flusher.Flush()
		case _, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprint(rw, "event: state\ndata: refresh\n\n")
			flusher.Flush()
		}
	}
}

func (w *Web) handleSelect(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.cb.Select == nil {
		http.Error(rw, "selection unavailable", http.StatusServiceUnavailable)
		return
	}
	var payload struct {
		Section string `json:"section"`
		ID      string `json:"id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if err := w.cb.Select(payload.Section, payload.ID); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	w.NotifyStateChanged()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (w *Web) handleMessage(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if w.cb.SendMessage == nil {
		http.Error(rw, "message send unavailable", http.StatusServiceUnavailable)
		return
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if err := w.cb.SendMessage(payload.Text); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	w.NotifyStateChanged()
	writeJSON(rw, map[string]bool{"ok": true})
}

func (w *Web) handleAction(rw http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	switch payload.Name {
	case "new_session":
		if w.cb.NewSession == nil {
			http.Error(rw, "action unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := w.cb.NewSession(); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
	default:
		http.Error(rw, "unknown action", http.StatusBadRequest)
		return
	}
	w.NotifyStateChanged()
	writeJSON(rw, map[string]bool{"ok": true})
}

func writeJSON(rw http.ResponseWriter, v any) {
	rw.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(rw).Encode(v)
}

const webPage = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Mink Web</title>
  <style>
    :root {
      --paper: #F6F1E8;
      --panel: #FBF8F2;
      --ink: #1E1B18;
      --text: #201C18;
      --muted: #7A6F64;
      --yellow: #F5D90A;
      --pink: #FF5FA2;
      --blue: #87B6FF;
      --orange: #FF7A45;
      --green: #7CCB5E;
      --code: #16181D;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      background: var(--paper);
      color: var(--text);
      font-family: "IBM Plex Sans", "Noto Sans SC", sans-serif;
    }
    .shell {
      height: 100vh;
      display: grid;
      grid-template-columns: 92px 320px minmax(0, 1fr) 320px;
      grid-template-rows: 72px minmax(0, 1fr) 88px;
    }
    .panel {
      border: 2px solid var(--ink);
      background: var(--panel);
    }
    .topbar {
      grid-column: 1 / -1;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 16px 20px;
      border-left: 0;
      border-right: 0;
      border-top: 0;
    }
    .workspace-title {
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 30px;
      font-weight: 700;
      letter-spacing: .02em;
      text-transform: uppercase;
    }
    .top-meta {
      color: var(--muted);
      font-size: 13px;
    }
    .rail {
      border-left: 0;
      border-bottom: 0;
      padding: 14px 10px;
      display: flex;
      flex-direction: column;
      gap: 8px;
    }
    .rail button, .index-item, .action-btn, .composer button {
      font: inherit;
    }
    .rail button {
      width: 100%;
      border: 2px solid var(--ink);
      background: transparent;
      padding: 10px 8px;
      text-align: left;
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 18px;
      cursor: pointer;
    }
    .rail button.active {
      background: var(--yellow);
    }
    .index {
      border-left: 0;
      border-bottom: 0;
      padding: 18px;
      overflow: auto;
    }
    .pane-title {
      display: flex;
      justify-content: space-between;
      align-items: center;
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 24px;
      font-weight: 700;
      text-transform: uppercase;
      margin-bottom: 14px;
    }
    .index-group + .index-group { margin-top: 18px; }
    .index-group h3 {
      margin: 0 0 8px;
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: .08em;
    }
    .index-item {
      width: 100%;
      display: block;
      border: 2px solid var(--ink);
      background: transparent;
      margin-bottom: 8px;
      padding: 10px 12px;
      text-align: left;
      cursor: pointer;
    }
    .index-item.active {
      background: var(--pink);
      color: #fff;
    }
    .index-item .meta {
      display: block;
      margin-top: 4px;
      color: var(--muted);
      font-size: 12px;
    }
    .index-item.active .meta { color: rgba(255,255,255,.86); }
    .main {
      border-left: 0;
      border-bottom: 0;
      display: grid;
      grid-template-rows: auto minmax(0,1fr);
      min-width: 0;
    }
    .main-header {
      border-bottom: 2px solid var(--ink);
      padding: 18px 20px;
    }
    .main-header h1 {
      margin: 0 0 6px;
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 30px;
      line-height: 1;
      text-transform: uppercase;
    }
    .main-header p {
      margin: 0;
      color: var(--muted);
      font-size: 14px;
    }
    .meta-row {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      margin-top: 10px;
    }
    .meta-chip {
      border: 2px solid var(--ink);
      padding: 4px 8px;
      font-size: 12px;
      background: #fff8d4;
    }
    .scroll {
      overflow: auto;
      padding: 20px;
    }
    .message {
      border: 2px solid var(--ink);
      padding: 14px;
      margin-bottom: 14px;
      background: #fff;
    }
    .message.user { background: #fff6d8; }
    .message.assistant { background: #ffffff; }
    .message.system {
      background: #f5eee0;
      color: var(--muted);
    }
    .message-head {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 8px;
      font-size: 13px;
    }
    .sender {
      font-weight: 700;
    }
    .descriptor, .time {
      color: var(--muted);
    }
    .reasoning {
      margin: 0 0 10px;
      padding: 10px 12px;
      border: 2px dashed var(--ink);
      background: #eef8fb;
      color: #285d66;
      white-space: pre-wrap;
      font-size: 14px;
    }
    .content {
      white-space: pre-wrap;
      line-height: 1.55;
      font-size: 15px;
    }
    .cards {
      display: grid;
      gap: 14px;
    }
    .card {
      border: 2px solid var(--ink);
      background: #fff;
      padding: 14px;
    }
    .card h3 {
      margin: 0 0 6px;
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 22px;
      text-transform: uppercase;
    }
    .card p {
      margin: 0 0 6px;
      color: var(--muted);
    }
    .context {
      border-left: 0;
      border-bottom: 0;
      padding: 18px;
      overflow: auto;
    }
    .context.empty {
      display: none;
    }
    .context-block {
      border: 2px solid var(--ink);
      background: #fff;
      padding: 12px;
      margin-bottom: 12px;
    }
    .context-block h3 {
      margin: 0 0 8px;
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 18px;
      text-transform: uppercase;
    }
    .context-block p {
      margin: 0;
      white-space: pre-wrap;
      line-height: 1.5;
      color: var(--muted);
    }
    .composer {
      grid-column: 1 / -1;
      border-left: 0;
      border-right: 0;
      border-bottom: 0;
      display: grid;
      grid-template-columns: 220px 1fr auto;
      gap: 14px;
      align-items: center;
      padding: 16px 20px;
    }
    .composer-label {
      font-family: "Barlow Condensed", "IBM Plex Sans Condensed", sans-serif;
      font-size: 22px;
      text-transform: uppercase;
    }
    .composer textarea {
      width: 100%;
      resize: none;
      min-height: 56px;
      max-height: 160px;
      border: 2px solid var(--ink);
      background: #fff;
      padding: 12px;
      font: inherit;
    }
    .composer button, .action-btn {
      border: 2px solid var(--ink);
      background: var(--yellow);
      padding: 12px 14px;
      cursor: pointer;
      font-weight: 700;
    }
    .action-btn {
      background: transparent;
      font-size: 13px;
      padding: 8px 10px;
    }
    .empty-hint {
      color: var(--muted);
      font-size: 14px;
    }
    @media (max-width: 1120px) {
      .shell {
        grid-template-columns: 84px 280px minmax(0, 1fr);
      }
      .context {
        display: none;
      }
    }
    @media (max-width: 840px) {
      .shell {
        grid-template-columns: 1fr;
        grid-template-rows: 72px auto auto minmax(0,1fr) 88px;
      }
      .rail, .index, .main, .context, .composer {
        grid-column: 1;
      }
      .rail, .index, .context {
        border-left: 0;
        border-right: 0;
      }
      .composer {
        grid-template-columns: 1fr;
      }
    }
  </style>
</head>
<body>
  <div class="shell">
    <div class="panel topbar">
      <div class="workspace-title">Mink Web</div>
      <div class="top-meta" id="workspaceMeta">loading…</div>
    </div>
    <aside class="panel rail" id="rail"></aside>
    <section class="panel index">
      <div class="pane-title">
        <span id="indexTitle">Loading</span>
        <button class="action-btn" id="indexAction" hidden></button>
      </div>
      <div id="indexGroups"></div>
    </section>
    <main class="panel main">
      <div class="main-header">
        <h1 id="headerTitle">Loading</h1>
        <p id="headerSubtitle"></p>
        <div class="meta-row" id="headerMeta"></div>
      </div>
      <div class="scroll" id="mainContent"></div>
    </main>
    <aside class="panel context" id="contextPane">
      <div class="pane-title"><span id="contextTitle">Context</span></div>
      <div id="contextBlocks"></div>
    </aside>
    <form class="panel composer" id="composer">
      <div class="composer-label" id="composerLabel">Message</div>
      <textarea id="composerInput" placeholder="Type message..."></textarea>
      <button type="submit">Send</button>
    </form>
  </div>
  <script>
    const state = { timer: null, busy: false };

    async function request(path, options = {}) {
      const res = await fetch(path, {
        headers: { 'Content-Type': 'application/json' },
        ...options,
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || res.statusText);
      }
      return res.json();
    }

    function el(tag, className, text) {
      const node = document.createElement(tag);
      if (className) node.className = className;
      if (text !== undefined) node.textContent = text;
      return node;
    }

    function render(stateData) {
      document.getElementById('workspaceMeta').textContent = stateData.workspace || '';

      const rail = document.getElementById('rail');
      rail.innerHTML = '';
      for (const item of stateData.nav || []) {
        const btn = el('button', item.active ? 'active' : '', item.label);
        btn.onclick = () => selectItem(item.id, '');
        rail.appendChild(btn);
      }

      document.getElementById('indexTitle').textContent = stateData.indexTitle || '';
      const actionBtn = document.getElementById('indexAction');
      if (stateData.indexAction && stateData.indexActionLabel) {
        actionBtn.hidden = false;
        actionBtn.textContent = stateData.indexActionLabel;
        actionBtn.onclick = () => runAction(stateData.indexAction);
      } else {
        actionBtn.hidden = true;
      }

      const groups = document.getElementById('indexGroups');
      groups.innerHTML = '';
      for (const group of stateData.indexGroups || []) {
        const wrap = el('div', 'index-group');
        wrap.appendChild(el('h3', '', group.title));
        for (const item of group.items || []) {
          const btn = el('button', 'index-item' + (item.active ? ' active' : ''));
          btn.onclick = () => selectItem(item.section || stateData.section, item.id);
          btn.appendChild(el('div', '', item.label));
          if (item.meta) btn.appendChild(el('span', 'meta', item.meta));
          wrap.appendChild(btn);
        }
        groups.appendChild(wrap);
      }

      document.getElementById('headerTitle').textContent = stateData.headerTitle || '';
      document.getElementById('headerSubtitle').textContent = stateData.headerSubtitle || '';
      const headerMeta = document.getElementById('headerMeta');
      headerMeta.innerHTML = '';
      for (const meta of stateData.headerMeta || []) {
        headerMeta.appendChild(el('div', 'meta-chip', meta));
      }

      const main = document.getElementById('mainContent');
      main.innerHTML = '';
      if (stateData.messages && stateData.messages.length > 0) {
        for (const message of stateData.messages) {
          const card = el('article', 'message ' + (message.role || 'assistant'));
          const head = el('div', 'message-head');
          const left = el('div');
          left.appendChild(el('div', 'sender', message.sender || 'Unknown'));
          if (message.descriptor) left.appendChild(el('div', 'descriptor', message.descriptor));
          head.appendChild(left);
          if (message.time) head.appendChild(el('div', 'time', message.time));
          card.appendChild(head);
          if (message.reasoning) card.appendChild(el('div', 'reasoning', message.reasoning));
          if (message.content) card.appendChild(el('div', 'content', message.content));
          main.appendChild(card);
        }
      } else if (stateData.cards && stateData.cards.length > 0) {
        const cards = el('div', 'cards');
        for (const item of stateData.cards) {
          const card = el('article', 'card');
          card.appendChild(el('h3', '', item.title));
          if (item.subtitle) card.appendChild(el('p', '', item.subtitle));
          if (item.meta) card.appendChild(el('p', 'empty-hint', item.meta));
          cards.appendChild(card);
        }
        main.appendChild(cards);
      } else {
        main.appendChild(el('div', 'empty-hint', stateData.emptyHint || 'Nothing here yet.'));
      }

      const contextPane = document.getElementById('contextPane');
      const contextTitle = document.getElementById('contextTitle');
      const contextBlocks = document.getElementById('contextBlocks');
      contextBlocks.innerHTML = '';
      if (stateData.contextBlocks && stateData.contextBlocks.length > 0) {
        contextPane.classList.remove('empty');
        contextTitle.textContent = stateData.contextTitle || 'Context';
        for (const block of stateData.contextBlocks) {
          const node = el('div', 'context-block');
          node.appendChild(el('h3', '', block.title));
          node.appendChild(el('p', '', block.body));
          contextBlocks.appendChild(node);
        }
      } else {
        contextPane.classList.add('empty');
      }

      document.getElementById('composerLabel').textContent = stateData.composerLabel || 'Message';
      const input = document.getElementById('composerInput');
      input.placeholder = stateData.composerPlaceholder || 'Type message...';
      input.disabled = !!stateData.composerDisabled || state.busy;
      document.querySelector('.composer button').disabled = !!stateData.composerDisabled || state.busy;
    }

    async function fetchState() {
      const data = await request('/api/state');
      render(data);
    }

    async function selectItem(section, id) {
      await request('/api/select', {
        method: 'POST',
        body: JSON.stringify({ section, id }),
      });
      await fetchState();
    }

    async function runAction(name) {
      await request('/api/action', {
        method: 'POST',
        body: JSON.stringify({ name }),
      });
      await fetchState();
    }

    document.getElementById('composer').addEventListener('submit', async (event) => {
      event.preventDefault();
      const input = document.getElementById('composerInput');
      const text = input.value.trim();
      if (!text || state.busy) return;
      state.busy = true;
      try {
        await request('/api/message', {
          method: 'POST',
          body: JSON.stringify({ text }),
        });
        input.value = '';
        await fetchState();
      } finally {
        state.busy = false;
      }
    });

    fetchState();
    state.timer = setInterval(fetchState, 1500);
  </script>
</body>
</html>`
