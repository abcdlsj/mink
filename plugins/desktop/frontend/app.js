const api = {
  async state() { return fetch("/api/state").then(r => r.json()); },
  async sessions() { return fetch("/api/sessions").then(r => r.json()); },
  async session(id) { return fetch("/api/session?id=" + encodeURIComponent(id)).then(r => r.json()); },
  async personas() { return fetch("/api/personas").then(r => r.json()); },
  async models() { return fetch("/api/models").then(r => r.json()); },
  async tools() { return fetch("/api/tools").then(r => r.json()); },
  async commands() { return fetch("/api/commands").then(r => r.json()); },
};

const state = {
  sessions: [],
  session: null,
  personas: [],
  models: [],
  tools: [],
  commands: [],
  workspace: null,
  activeId: null,
};

const el = (sel) => document.querySelector(sel);
const els = (sel) => Array.from(document.querySelectorAll(sel));

function relTime(t) {
  const d = new Date(t);
  const s = Math.floor((Date.now() - d.getTime()) / 1000);
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.floor(s/60) + "m ago";
  if (s < 86400) return Math.floor(s/3600) + "h ago";
  return Math.floor(s/86400) + "d ago";
}

function renderTopbar() {
  if (!state.workspace) return;
  const w = state.workspace;
  el("#status").innerHTML = `${w.runtime} · ${w.model} · <span class="dot ${w.ready ? "done" : "error"}"></span>`;
}

function sessionItem(s) {
  const li = document.createElement("li");
  li.className = "session-item" + (s.id === state.activeId ? " active" : "");
  li.dataset.id = s.id;
  if (s.running) {
    const dot = document.createElement("span");
    dot.className = "si-running";
    li.appendChild(dot);
  }
  const title = document.createElement("div");
  title.className = "si-title";
  title.textContent = s.title;
  const meta = document.createElement("div");
  meta.className = "si-meta";
  meta.textContent = (s.persona_name || "Default") + " · " + s.runtime;
  const stat = document.createElement("div");
  stat.className = "si-stat";
  stat.textContent = relTime(s.updated_at) + " · " + s.event_count + " events";
  li.append(title, meta, stat);
  li.onclick = () => selectSession(s.id);
  return li;
}

function renderSessions() {
  const pinned = state.sessions.filter(s => s.pinned);
  const recent = state.sessions.filter(s => !s.pinned);
  const pe = el("#pinned");
  const re = el("#recent");
  pe.innerHTML = "";
  re.innerHTML = "";
  if (pinned.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "No pinned sessions";
    pe.appendChild(empty);
  }
  pinned.forEach(s => pe.appendChild(sessionItem(s)));
  recent.forEach(s => re.appendChild(sessionItem(s)));
}

function eventBlock(ev) {
  const wrap = document.createElement("div");
  const cls = (ev.kind === "service_notice") ? "notice" : "event " + (ev.status || "idle") + (ev.kind === "reasoning" ? " reasoning" : "");
  wrap.className = cls;
  if (ev.kind === "service_notice") {
    const dot = document.createElement("span"); dot.className = "dot";
    const txt = document.createElement("span"); txt.textContent = ev.output || "";
    wrap.append(dot, txt);
    return wrap;
  }
  wrap.classList.add("collapsed");
  const header = document.createElement("div");
  header.className = "event-header";
  const chev = document.createElement("span"); chev.className = "event-chevron"; chev.textContent = "▸";
  const label = document.createElement("span"); label.className = "event-label";
  const name = document.createElement("span"); name.className = "event-name";
  const meta = document.createElement("span"); meta.className = "event-meta";
  if (ev.kind === "reasoning") {
    label.textContent = "Reasoning";
    name.textContent = "";
    meta.textContent = "view";
  } else if (ev.kind === "tool_call") {
    label.textContent = "Tool";
    name.textContent = ev.tool_name || "";
    let m = ev.status || "";
    if (ev.duration_ms) m += " · " + ev.duration_ms + "ms";
    meta.textContent = m;
  }
  header.append(chev, label, name, meta);
  const body = document.createElement("div");
  body.className = "event-body";
  if (ev.kind === "reasoning") {
    body.textContent = ev.output || "(no reasoning text)";
  } else {
    if (ev.args) {
      const k = document.createElement("div"); k.className = "kv"; k.textContent = ev.args;
      body.appendChild(k);
    }
    if (ev.output) {
      const o = document.createElement("div"); o.textContent = ev.output;
      body.appendChild(o);
    }
    if (ev.err) {
      const e = document.createElement("div"); e.className = "err"; e.textContent = ev.err;
      body.appendChild(e);
    }
  }
  header.onclick = () => {
    wrap.classList.toggle("collapsed");
    chev.textContent = wrap.classList.contains("collapsed") ? "▸" : "▾";
  };
  wrap.append(header, body);
  return wrap;
}

function messageBlock(m) {
  const wrap = document.createElement("div");
  wrap.className = "message " + m.role;
  const role = document.createElement("div");
  role.className = "message-role";
  role.textContent = m.role === "user" ? "You" : "Sumi";
  const content = document.createElement("div");
  content.className = "message-content";
  content.textContent = m.content || "";
  wrap.append(role, content);

  const events = [];
  if (m.reasoning) events.push({kind: "reasoning", status: "done", output: m.reasoning});
  if (m.events) events.push(...m.events);
  if (events.length) {
    const ec = document.createElement("div");
    ec.className = "events";
    events.forEach(ev => ec.appendChild(eventBlock(ev)));
    wrap.appendChild(ec);
  }
  return wrap;
}

function renderSession() {
  if (!state.session) return;
  const s = state.session;
  el("#session-title").textContent = s.item.title;
  el("#session-meta").textContent =
    (s.item.persona_name || "Default") + " · " + s.item.model + " · " + relTime(s.item.updated_at);
  el("#stop-btn").classList.toggle("hidden", !s.item.running);
  const m = el("#messages");
  m.innerHTML = "";
  s.messages.forEach(msg => m.appendChild(messageBlock(msg)));
  m.scrollTop = m.scrollHeight;

  el("#ins-persona").textContent = s.item.persona_name || "Default";
  el("#ins-model").textContent = s.item.model;
  el("#ins-runtime").textContent = s.item.runtime;
  const tl = el("#ins-tools");
  tl.innerHTML = "";
  state.tools.forEach(t => {
    const li = document.createElement("li");
    li.innerHTML = `<span>${t.name}</span><span class="ok">${t.enabled ? "on" : "off"}</span>`;
    tl.appendChild(li);
  });
  el("#ins-runlog").textContent = s.item.event_count + " events";
}

async function selectSession(id) {
  state.activeId = id;
  state.session = await api.session(id);
  renderSessions();
  renderSession();
}

function renderComposer() {
  const ps = el("#persona-select");
  ps.innerHTML = "";
  const def = document.createElement("option");
  def.value = ""; def.textContent = "Persona: Default";
  ps.appendChild(def);
  state.personas.forEach(p => {
    const o = document.createElement("option");
    o.value = p.id; o.textContent = "Persona: " + p.display;
    ps.appendChild(o);
  });
  const ms = el("#model-select");
  ms.innerHTML = "";
  state.models.forEach(m => {
    const o = document.createElement("option");
    o.value = m.name; o.textContent = "Model: " + m.model;
    ms.appendChild(o);
  });
}

const palette = {
  open: false,
  query: "",
  selected: 0,
  rows: [],
};

function fuzzyMatch(text, q) {
  if (!q) return true;
  return text.toLowerCase().includes(q.toLowerCase());
}

function buildPaletteRows(query) {
  let q = query.trim();
  let scope = null;
  if (q.startsWith("/")) { scope = "command"; q = q.slice(1); }
  else if (q.startsWith("session ")) { scope = "session"; q = q.slice(8); }
  else if (q.startsWith("persona ") || q.startsWith("@")) { scope = "persona"; q = q.replace(/^persona |^@/, ""); }
  else if (q.startsWith("model ")) { scope = "model"; q = q.slice(6); }
  const rows = [];
  if (scope === null || scope === "session") {
    const ss = state.sessions
      .filter(s => fuzzyMatch(s.title, q))
      .sort((a,b) => (b.running - a.running) || (b.pinned - a.pinned))
      .slice(0, 5);
    if (ss.length) {
      rows.push({label: "Sessions", group: true});
      ss.forEach(s => rows.push({type: "session", id: s.id, title: s.title, meta: (s.persona_name || "Default") + " · " + relTime(s.updated_at)}));
    }
  }
  if (scope === null || scope === "command") {
    const cs = state.commands.filter(c => fuzzyMatch(c.name, q)).slice(0, 3);
    if (cs.length) {
      rows.push({label: "Commands", group: true});
      cs.forEach(c => rows.push({type: "command", title: c.name, meta: c.summary || ""}));
    }
  }
  if (scope === null || scope === "persona") {
    const ps = state.personas.filter(p => fuzzyMatch(p.display, q)).slice(0, 2);
    if (ps.length) {
      rows.push({label: "Personas", group: true});
      ps.forEach(p => rows.push({type: "persona", id: p.id, title: p.display, meta: p.runtime}));
    }
  }
  if (scope === null || scope === "model") {
    const ms = state.models.filter(m => fuzzyMatch(m.model, q)).slice(0, 2);
    if (ms.length) {
      rows.push({label: "Models", group: true});
      ms.forEach(m => rows.push({type: "model", id: m.name, title: m.model, meta: m.ready ? "ready" : "not ready"}));
    }
  }
  return rows;
}

function renderPalette() {
  const c = el("#palette-results");
  c.innerHTML = "";
  palette.rows = buildPaletteRows(palette.query);
  if (palette.rows.length === 0) {
    const e = document.createElement("div");
    e.className = "empty";
    e.textContent = "No results · try session title, /command, persona name, or model name.";
    c.appendChild(e);
    return;
  }
  let actionIndex = -1;
  palette.rows.forEach((row, i) => {
    if (row.group) {
      const g = document.createElement("div");
      g.className = "palette-group-label";
      g.textContent = row.label;
      c.appendChild(g);
      return;
    }
    actionIndex++;
    const r = document.createElement("div");
    r.className = "palette-row" + (actionIndex === palette.selected ? " selected" : "");
    r.innerHTML = `<span>${row.title}</span><span class="meta">${row.meta || ""}</span>`;
    r.onclick = () => paletteActivate(row);
    c.appendChild(r);
  });
}

function paletteActivate(row) {
  closePalette();
  if (row.type === "session") selectSession(row.id);
}

function openPalette() {
  palette.open = true;
  palette.query = "";
  palette.selected = 0;
  el("#palette").classList.remove("hidden");
  el("#palette-input").value = "";
  el("#palette-input").focus();
  renderPalette();
}

function closePalette() {
  palette.open = false;
  el("#palette").classList.add("hidden");
}

function bindGlobalKeys() {
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      palette.open ? closePalette() : openPalette();
    } else if (palette.open) {
      if (e.key === "Escape") { closePalette(); }
      else if (e.key === "ArrowDown") {
        const actionRows = palette.rows.filter(r => !r.group).length;
        palette.selected = Math.min(palette.selected + 1, actionRows - 1);
        renderPalette();
        e.preventDefault();
      } else if (e.key === "ArrowUp") {
        palette.selected = Math.max(palette.selected - 1, 0);
        renderPalette();
        e.preventDefault();
      } else if (e.key === "Enter") {
        const actions = palette.rows.filter(r => !r.group);
        if (actions[palette.selected]) paletteActivate(actions[palette.selected]);
        e.preventDefault();
      }
    }
  });
  el("#palette-input").addEventListener("input", (e) => {
    palette.query = e.target.value;
    palette.selected = 0;
    renderPalette();
  });
  el("#open-palette").onclick = openPalette;
}

async function main() {
  const [state0, sessions, personas, models, tools, commands] = await Promise.all([
    api.state(), api.sessions(), api.personas(), api.models(), api.tools(), api.commands()
  ]);
  state.workspace = state0;
  state.sessions = sessions;
  state.personas = personas;
  state.models = models;
  state.tools = tools;
  state.commands = commands;
  state.activeId = sessions[0]?.id;

  renderTopbar();
  renderSessions();
  renderComposer();
  if (state.activeId) selectSession(state.activeId);
  bindGlobalKeys();
}

main();
