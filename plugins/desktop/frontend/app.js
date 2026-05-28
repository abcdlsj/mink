const api = {
  state: () => fetch("/api/state").then(r => r.json()),
  channels: () => fetch("/api/channels").then(r => r.json()),
  threads: () => fetch("/api/threads").then(r => r.json()),
  agents: () => fetch("/api/agents").then(r => r.json()),
  channel: (id) => fetch("/api/channel?id=" + encodeURIComponent(id)).then(r => r.json()),
  thread: (id) => fetch("/api/thread?id=" + encodeURIComponent(id)).then(r => r.json()),
  participants: (channelID, threadID) => {
    const q = new URLSearchParams();
    if (channelID) q.set("channel", channelID);
    if (threadID) q.set("thread", threadID);
    return fetch("/api/participants?" + q).then(r => r.json());
  },
  models: () => fetch("/api/models").then(r => r.json()),
  tools: () => fetch("/api/tools").then(r => r.json()),
  commands: () => fetch("/api/commands").then(r => r.json()),
};

const view = {
  mode: "channel",
  activeChannel: null,
  activeThread: null,
  activeAgent: null,
};

const data = {
  state: null,
  channels: [],
  threads: [],
  agents: [],
  models: [],
  tools: [],
  commands: [],
  detail: null,
  participants: null,
};

const $ = (sel) => document.querySelector(sel);

function relTime(t) {
  const d = new Date(t);
  const s = Math.floor((Date.now() - d.getTime()) / 1000);
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.floor(s/60) + "m ago";
  if (s < 86400) return Math.floor(s/3600) + "h ago";
  return Math.floor(s/86400) + "d ago";
}

function el(tag, opts = {}, children = []) {
  const e = document.createElement(tag);
  if (opts.class) e.className = opts.class;
  if (opts.text != null) e.textContent = opts.text;
  if (opts.html != null) e.innerHTML = opts.html;
  if (opts.onclick) e.onclick = opts.onclick;
  if (opts.attrs) for (const [k,v] of Object.entries(opts.attrs)) {
    if (v === true) e.setAttribute(k, "");
    else if (v != null && v !== false) e.setAttribute(k, v);
  }
  for (const c of children) if (c) e.appendChild(c);
  return e;
}

function icon(name) {
  return el("span", { class: "icon", attrs: { "data-icon": name }});
}

function initials(s) {
  return (s || "?").trim().slice(0, 2).toUpperCase();
}

// ========== Top bar ==========

function renderTopbar() {
  const s = data.state;
  if (!s) return;
  const stateLabel = s.ready ? "Ready" : "Not configured";
  $("#status").innerHTML = `${s.model} · ${stateLabel}`;
}

// ========== Left pane ==========

function navItem({ icon: ic, name, meta, badge, running, active, onclick, plus }) {
  const item = el("div", { class: "nav-item" + (active ? " active" : ""), onclick });
  item.appendChild(icon(ic));
  const nameWrap = el("div", { class: "nav-name-wrap" });
  if (running) nameWrap.appendChild(el("span", { class: "dot running nav-running" }));
  nameWrap.appendChild(el("span", { class: "nav-name", text: name }));
  item.appendChild(nameWrap);
  const right = el("div", { class: "nav-meta" });
  if (badge) right.appendChild(el("span", { class: "badge", text: String(badge) }));
  if (meta) right.appendChild(el("span", { text: meta }));
  if (plus) right.appendChild(icon("plus"));
  item.appendChild(right);
  return item;
}

function renderChannels() {
  const list = $("#channels");
  list.innerHTML = "";
  data.channels.forEach(c => {
    const li = el("li");
    li.appendChild(navItem({
      icon: "hash",
      name: c.name,
      badge: c.unread_count > 0 ? c.unread_count : null,
      running: c.has_running,
      active: view.mode === "channel" && view.activeChannel === c.id,
      onclick: () => openChannel(c.id),
    }));
    list.appendChild(li);
  });
}

function renderAgents() {
  const list = $("#agents");
  list.innerHTML = "";
  data.agents.forEach(a => {
    const li = el("li");
    const item = navItem({
      icon: "at",
      name: a.display,
      meta: a.status === "running" ? "running" : null,
      active: view.mode === "agent" && view.activeAgent === a.id,
      onclick: () => openAgent(a.id),
    });
    list.appendChild(li);
    li.appendChild(item);
  });
}

function renderThreads() {
  const list = $("#threads");
  list.innerHTML = "";
  data.threads.forEach(t => {
    const channel = data.channels.find(c => c.id === t.channel_id);
    const item = el("div", {
      class: "thread-item" + (view.mode === "thread" && view.activeThread === t.id ? " active" : ""),
      onclick: () => openThread(t.id),
    });
    const title = el("div", { class: "ti-title" }, [
      icon("thread"),
      el("span", { text: t.title }),
    ]);
    const meta = el("div", {
      class: "ti-meta",
      text: (channel ? "#" + channel.name : "") + " · " + relTime(t.updated_at) + (t.has_running ? " · running" : "")
    });
    item.appendChild(title);
    item.appendChild(meta);
    const li = el("li");
    li.appendChild(item);
    list.appendChild(li);
  });
}

// ========== Center: messages & events ==========

function eventBlock(ev, idx) {
  if (ev.kind === "service_notice") {
    const wrap = el("div", { class: "notice" });
    wrap.appendChild(icon("info"));
    wrap.appendChild(el("span", { text: ev.output || "" }));
    return wrap;
  }
  const status = ev.status || "idle";
  const wrap = el("div", { class: "event collapsed " + status + (ev.kind === "reasoning" ? " reasoning" : "") });
  const header = el("div", { class: "event-header" });
  const chev = icon("chevron_right");
  chev.classList.add("event-chevron");
  header.appendChild(chev);

  const evIcon = icon(ev.kind === "reasoning" ? "brain" : "terminal");
  evIcon.classList.add("event-icon");
  header.appendChild(evIcon);

  if (ev.kind === "reasoning") {
    header.appendChild(el("span", { class: "event-label", text: "Reasoning" }));
    header.appendChild(el("span", { class: "event-name", text: "" }));
  } else {
    header.appendChild(el("span", { class: "event-label", text: "Tool" }));
    header.appendChild(el("span", { class: "event-name", text: ev.tool_name || "" }));
  }
  header.appendChild(el("span"));
  let metaText = ev.kind === "reasoning" ? "view" : (status + (ev.duration_ms ? " · " + ev.duration_ms + "ms" : ""));
  header.appendChild(el("span", { class: "event-meta", text: metaText }));
  wrap.appendChild(header);

  const body = el("div", { class: "event-body" });
  if (ev.kind === "reasoning") {
    body.textContent = ev.output || "(no reasoning text)";
  } else {
    if (ev.args) body.appendChild(el("div", { class: "kv", text: ev.args }));
    if (ev.output) body.appendChild(el("div", { text: ev.output }));
    if (ev.err) body.appendChild(el("div", { class: "err", text: ev.err }));
  }
  wrap.appendChild(body);

  header.onclick = () => {
    const collapsed = wrap.classList.toggle("collapsed");
    chev.innerHTML = "";
    chev.setAttribute("data-icon", collapsed ? "chevron_right" : "chevron_down");
    chev.removeAttribute("data-injected");
    injectIcons(chev);
  };
  return wrap;
}

function messageBlock(m) {
  const wrap = el("div", { class: "msg " + (m.role === "user" ? "user" : "agent") });
  const av = el("div", { class: "msg-avatar" });
  const seed = m.role === "user" ? "user" : (m.author_id || m.author_name || "agent");
  const kind = m.role === "user" ? "user" : "agent";
  av.innerHTML = identiconSVG(seed, kind);
  wrap.appendChild(av);

  const body = el("div");
  const authorRow = el("div", { class: "msg-author-row" });
  authorRow.appendChild(el("span", { class: "msg-author", text: m.role === "user" ? "You" : (m.author_name || "Sumi") }));
  if (m.role !== "user") {
    const ag = data.agents.find(a => a.id === m.author_id);
    if (ag && ag.role) authorRow.appendChild(el("span", { class: "msg-role-tag", text: ag.role }));
  }
  authorRow.appendChild(el("span", { class: "msg-time", text: relTime(m.time) }));
  body.appendChild(authorRow);

  if (m.content) body.appendChild(el("div", { class: "msg-content", text: m.content }));

  const events = [];
  if (m.reasoning) events.push({ kind: "reasoning", status: "done", output: m.reasoning });
  if (m.events) events.push(...m.events);
  if (events.length) {
    const ec = el("div", { class: "events" });
    events.forEach((ev, i) => ec.appendChild(eventBlock(ev, i)));
    body.appendChild(ec);
  }

  if (m.thread_id) {
    const tl = el("div", {
      class: "thread-link",
      onclick: () => openThread(m.thread_id),
    }, [
      icon("corner_down_right"),
      el("span", { text: m.thread_summary || "Open thread" }),
    ]);
    body.appendChild(tl);
  }

  wrap.appendChild(body);
  return wrap;
}

function renderConvHead() {
  const detail = data.detail;
  if (!detail) return;
  const item = detail.item;
  $("#stop-btn").hidden = !item.running;

  $("#conv-title").innerHTML = "";
  if (view.mode === "channel") {
    $("#conv-title").appendChild(icon("hash"));
    $("#conv-title").appendChild(el("span", { text: data.channels.find(c => c.id === view.activeChannel)?.name || "channel" }));
    $("#conv-meta").textContent = (detail.summary || "") + (item.running ? " · agents running" : "");
  } else if (view.mode === "thread") {
    $("#conv-title").appendChild(icon("thread"));
    $("#conv-title").appendChild(el("span", { text: item.title }));
    $("#conv-meta").textContent = detail.summary || "";
  } else {
    $("#conv-title").appendChild(icon("at"));
    const ag = data.agents.find(a => a.id === view.activeAgent);
    $("#conv-title").appendChild(el("span", { text: ag?.display || view.activeAgent }));
    $("#conv-meta").textContent = ag?.role || "";
  }
  injectIcons($("#conv-head"));
}

function renderMessages() {
  const m = $("#messages");
  m.innerHTML = "";
  if (!data.detail) return;
  data.detail.messages.forEach(msg => m.appendChild(messageBlock(msg)));
  injectIcons(m);
  m.scrollTop = m.scrollHeight;
}

// ========== Right pane ==========

function insSection(label, body, hint) {
  const sec = el("div", { class: "ins-section" });
  sec.appendChild(el("div", { class: "ins-label", text: label }));
  if (typeof body === "string") sec.appendChild(el("div", { class: "ins-value", text: body }));
  else sec.appendChild(body);
  if (hint) sec.appendChild(el("div", { class: "ins-hint", text: hint }));
  return sec;
}

function participantRow(p) {
  const w = el("div", { class: "participant" + (p.role === "user" ? " user" : "") });
  const av = el("div", { class: "av" });
  av.innerHTML = identiconSVG(p.id || p.display, p.role === "user" ? "user" : "agent");
  w.appendChild(av);
  const name = el("div");
  name.appendChild(el("span", { class: "name", text: p.display }));
  if (p.role) name.appendChild(el("span", { class: "role", text: p.role }));
  w.appendChild(name);
  if (p.status) {
    const s = el("span", { class: "dot " + (p.status === "running" ? "running" : (p.status === "done" ? "done" : "")) });
    w.appendChild(s);
  } else {
    w.appendChild(el("span"));
  }
  return w;
}

function runCard(r) {
  const w = el("div", { class: "run-card " + (r.status || "idle") });
  const ag = data.agents.find(a => a.id === r.agent_id);
  w.appendChild(el("div", { class: "run-title", text: r.title }));
  w.appendChild(el("div", {
    class: "run-meta",
    text: (ag?.display || r.agent_id) + " · " + (r.status || "") + " · " + relTime(r.time),
  }));
  return w;
}

function renderRight() {
  const right = $("#right");
  right.innerHTML = "";

  const modelSec = insSection("Current Model", data.state?.model || "—", "Used by all new agent runs");

  const toolsList = el("ul", { class: "ins-list" });
  data.tools.forEach(t => {
    const li = el("li", { class: "participant" });
    li.appendChild(el("div", { class: "av", text: initials(t.name) }));
    const name = el("div");
    name.appendChild(el("span", { class: "name", text: t.name }));
    li.appendChild(name);
    li.appendChild(el("span", { class: "dot done" }));
    toolsList.appendChild(li);
  });

  if (view.mode === "channel" && data.detail) {
    const channel = data.channels.find(c => c.id === view.activeChannel);
    const summary = el("div", { class: "ins-value", text: channel?.topic || "" });
    right.appendChild(insSection("Channel", channel?.name ? "#" + channel.name : "—"));
    if (channel?.topic) right.appendChild(insSection("Topic", channel.topic));

    const partsList = el("div", { class: "ins-list" });
    (data.participants?.agents || []).forEach(p => partsList.appendChild(participantRow(p)));
    right.appendChild(insSection("Participants", partsList));

    const runs = data.participants?.active_runs || [];
    if (runs.length) {
      const runsWrap = el("div");
      runs.forEach(r => runsWrap.appendChild(runCard(r)));
      right.appendChild(insSection("Active Runs", runsWrap));
    }

    const threadsInChannel = data.threads.filter(t => t.channel_id === view.activeChannel).slice(0, 4);
    if (threadsInChannel.length) {
      const tlist = el("div");
      threadsInChannel.forEach(t => {
        const card = el("div", { class: "thread-card", onclick: () => openThread(t.id) });
        card.appendChild(el("div", { class: "t-title", text: t.title }));
        card.appendChild(el("div", { class: "t-meta", text: t.event_count + " events · " + relTime(t.updated_at) + (t.has_running ? " · running" : "") }));
        tlist.appendChild(card);
      });
      right.appendChild(insSection("Recent Threads", tlist));
    }

    right.appendChild(modelSec);
    right.appendChild(insSection("Execution", "Local", "Configured in settings"));
    right.appendChild(insSection("Tools", toolsList));
  } else if (view.mode === "thread" && data.detail) {
    const item = data.detail.item;
    right.appendChild(insSection("Thread", item.title));
    right.appendChild(insSection("Status", item.running ? "running" : "open"));

    const runs = data.participants?.active_runs || [];
    if (runs.length) {
      const w = el("div");
      runs.forEach(r => w.appendChild(runCard(r)));
      right.appendChild(insSection("Active Run", w));
    }

    const partsList = el("div", { class: "ins-list" });
    (data.participants?.agents || []).forEach(p => partsList.appendChild(participantRow(p)));
    right.appendChild(insSection("Participants", partsList));

    const recent = data.participants?.recent_runs || [];
    if (recent.length) {
      const w = el("div");
      recent.forEach(r => w.appendChild(runCard(r)));
      right.appendChild(insSection("Recent Subtasks", w));
    }

    right.appendChild(modelSec);
    right.appendChild(insSection("Tools", toolsList));
  } else if (view.mode === "agent") {
    const ag = data.agents.find(a => a.id === view.activeAgent);
    right.appendChild(insSection("Agent", ag?.display || "—"));
    right.appendChild(insSection("Status", ag?.status === "running" ? "running" : "idle"));
    if (ag?.role) right.appendChild(insSection("Role", ag.role));

    const involvedThreads = data.threads.slice(0, 4);
    if (involvedThreads.length) {
      const tlist = el("div");
      involvedThreads.forEach(t => {
        const ch = data.channels.find(c => c.id === t.channel_id);
        const card = el("div", { class: "thread-card", onclick: () => openThread(t.id) });
        card.appendChild(el("div", { class: "t-title", text: t.title }));
        card.appendChild(el("div", { class: "t-meta", text: (ch ? "#" + ch.name + " · " : "") + relTime(t.updated_at) + (t.has_running ? " · running" : "") }));
        tlist.appendChild(card);
      });
      right.appendChild(insSection("Recent Threads", tlist));
    }

    right.appendChild(modelSec);
    right.appendChild(insSection("Execution", "Local", "Configured in settings"));
    right.appendChild(insSection("Tools", toolsList));
  }

  injectIcons(right);
}

// ========== Open views ==========

async function openChannel(id) {
  view.mode = "channel";
  view.activeChannel = id;
  view.activeThread = null;
  view.activeAgent = null;
  data.detail = await api.channel(id);
  data.participants = await api.participants(id, "");
  $("#input").placeholder = "Message #" + (data.channels.find(c => c.id === id)?.name || "channel") + "...";
  renderChannels(); renderAgents(); renderThreads();
  renderConvHead(); renderMessages(); renderRight();
}

async function openThread(id) {
  view.mode = "thread";
  view.activeThread = id;
  view.activeAgent = null;
  data.detail = await api.thread(id);
  data.participants = await api.participants(view.activeChannel, id);
  $("#input").placeholder = "Reply in thread...";
  renderChannels(); renderAgents(); renderThreads();
  renderConvHead(); renderMessages(); renderRight();
}

async function openAgent(id) {
  view.mode = "agent";
  view.activeAgent = id;
  view.activeChannel = null;
  view.activeThread = null;
  const ag = data.agents.find(a => a.id === id);
  data.detail = {
    item: { id, title: "@" + (ag?.display || id), running: ag?.status === "running" },
    messages: [],
    summary: ag?.role || "",
  };
  data.participants = null;
  $("#input").placeholder = "Message @" + (ag?.display || id) + "...";
  renderChannels(); renderAgents(); renderThreads();
  renderConvHead(); renderAgentBody(id); renderRight();
}

function renderAgentBody(id) {
  const m = $("#messages");
  m.innerHTML = "";
  const wrap = el("div", { class: "agent-empty" });
  const ag = data.agents.find(a => a.id === id);
  wrap.appendChild(el("div", { class: "ae-headline", text: "Direct conversation with @" + (ag?.display || id) }));
  if (ag?.role) wrap.appendChild(el("div", { class: "ae-sub", text: ag.role }));
  wrap.appendChild(el("div", { class: "ae-hint", text: "Send a message below to start. Threads and channels involving this agent appear on the right." }));
  m.appendChild(wrap);
  injectIcons(m);
}

// ========== Composer ==========

function renderComposer() {
  const ag = $("#agent-select");
  ag.innerHTML = "";
  ag.appendChild(el("option", { attrs: { value: "" }, text: "Persona: Default" }));
  data.agents.forEach(a => ag.appendChild(el("option", { attrs: { value: a.id }, text: "@" + a.display })));
  const ms = $("#model-select");
  ms.innerHTML = "";
  data.models.forEach(m => ms.appendChild(el("option", { attrs: { value: m.name }, text: m.model })));
}

// ========== Cmd+K ==========

const palette = { open: false, query: "", selected: 0, rows: [] };

function fuzzy(text, q) { return !q || text.toLowerCase().includes(q.toLowerCase()); }

function buildPaletteRows(query) {
  let q = query.trim();
  let scope = null;
  if (q === "thread" || q.startsWith("thread ")) { scope = "thread"; q = q.replace(/^thread ?/, ""); }
  else if (q === "model" || q.startsWith("model ")) { scope = "model"; q = q.replace(/^model ?/, ""); }
  else if (q.startsWith("#")) { scope = "channel"; q = q.slice(1); }
  else if (q.startsWith("@")) { scope = "agent"; q = q.slice(1); }
  else if (q.startsWith("/")) { scope = "command"; q = q.slice(1); }
  const rows = [];
  const add = (group, items, type, mapper) => {
    if (!items.length) return;
    rows.push({ group: true, label: group });
    items.forEach(it => rows.push({ type, ...mapper(it) }));
  };
  if (scope === null || scope === "channel") {
    add("Channels",
      data.channels.filter(c => fuzzy(c.name, q)).slice(0, 4),
      "channel",
      c => ({ id: c.id, title: "#" + c.name, meta: c.topic || "", icon: "hash" })
    );
  }
  if (scope === null || scope === "thread") {
    add("Threads",
      data.threads.filter(t => fuzzy(t.title, q)).slice(0, 4),
      "thread",
      t => {
        const ch = data.channels.find(c => c.id === t.channel_id);
        return { id: t.id, title: t.title, meta: (ch ? "#" + ch.name : "") + " · " + relTime(t.updated_at), icon: "thread" };
      }
    );
  }
  if (scope === null || scope === "agent") {
    add("Agents",
      data.agents.filter(a => fuzzy(a.display, q)).slice(0, 3),
      "agent",
      a => ({ id: a.id, title: "@" + a.display, meta: a.role || "", icon: "at" })
    );
  }
  if (scope === null || scope === "command") {
    add("Commands",
      data.commands.filter(c => fuzzy(c.name, q)).slice(0, 3),
      "command",
      c => ({ title: c.name, meta: c.summary || "", icon: "terminal" })
    );
  }
  if (scope === null || scope === "model") {
    add("Models",
      data.models.filter(m => fuzzy(m.model, q)).slice(0, 2),
      "model",
      m => ({ id: m.name, title: m.model, meta: m.ready ? "ready" : "not ready", icon: "info" })
    );
  }
  return rows;
}

function renderPalette() {
  const c = $("#palette-results");
  c.innerHTML = "";
  palette.rows = buildPaletteRows(palette.query);
  if (!palette.rows.length) {
    c.appendChild(el("div", { class: "empty", text: "No results · try #channel, thread, @agent, /command, or model name." }));
    return;
  }
  let actionIdx = -1;
  palette.rows.forEach(row => {
    if (row.group) {
      c.appendChild(el("div", { class: "palette-group-label", text: row.label }));
      return;
    }
    actionIdx++;
    const r = el("div", { class: "palette-row" + (actionIdx === palette.selected ? " selected" : ""), onclick: () => activate(row) });
    const label = el("div", { class: "label" });
    label.appendChild(icon(row.icon));
    label.appendChild(el("span", { class: "label-title", text: row.title }));
    r.appendChild(label);
    r.appendChild(el("span", { class: "meta", text: row.meta || "" }));
    c.appendChild(r);
  });
  injectIcons(c);
}

function activate(row) {
  closePalette();
  if (row.type === "channel") openChannel(row.id);
  else if (row.type === "thread") openThread(row.id);
  else if (row.type === "agent") openAgent(row.id);
}

function openPalette() {
  palette.open = true;
  palette.query = "";
  palette.selected = 0;
  $("#palette").hidden = false;
  $("#palette-input").value = "";
  $("#palette-input").focus();
  renderPalette();
}
function closePalette() {
  palette.open = false;
  $("#palette").hidden = true;
}

function bindGlobalKeys() {
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "k") {
      e.preventDefault();
      palette.open ? closePalette() : openPalette();
    } else if (palette.open) {
      if (e.key === "Escape") closePalette();
      else if (e.key === "ArrowDown") {
        const n = palette.rows.filter(r => !r.group).length;
        palette.selected = Math.min(palette.selected + 1, n - 1);
        renderPalette();
        e.preventDefault();
      } else if (e.key === "ArrowUp") {
        palette.selected = Math.max(palette.selected - 1, 0);
        renderPalette();
        e.preventDefault();
      } else if (e.key === "Enter") {
        const actions = palette.rows.filter(r => !r.group);
        if (actions[palette.selected]) activate(actions[palette.selected]);
        e.preventDefault();
      }
    }
  });
  $("#palette-input").addEventListener("input", e => {
    palette.query = e.target.value;
    palette.selected = 0;
    renderPalette();
  });
  $("#open-palette").onclick = openPalette;
}

// ========== Bootstrap ==========

async function main() {
  const [state, channels, threads, agents, models, tools, commands] = await Promise.all([
    api.state(), api.channels(), api.threads(), api.agents(), api.models(), api.tools(), api.commands()
  ]);
  data.state = state;
  data.channels = channels;
  data.threads = threads;
  data.agents = agents;
  data.models = models;
  data.tools = tools;
  data.commands = commands;
  renderTopbar();
  renderComposer();
  injectIcons();
  bindGlobalKeys();
  if (channels[0]) await openChannel(channels[0].id);
  injectIcons();
}

main();
