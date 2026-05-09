package web

var indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sumi</title>
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
    .queue { color:var(--muted); font-size:12px; padding:2px 0; }
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
        <h1>Sumi</h1>
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
        <div class="queue" id="queue"></div>
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
      qs('#queue').textContent = st.queued ? (st.queued === 1 ? '1 message queued' : st.queued + ' messages queued') : ''
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
